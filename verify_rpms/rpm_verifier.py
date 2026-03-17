#!/usr/bin/env python3

"""Verify RPMs are signed"""

import json
import sys
import tempfile
from collections import Counter
from concurrent.futures import ThreadPoolExecutor
from dataclasses import dataclass
from pathlib import Path
from subprocess import CalledProcessError, run
from typing import Any, Callable, Iterable, Tuple

import click


@dataclass(frozen=True)
class ProcessedImage:
    """
    A class to hold data about rpms for single image:
    Image name,
    Unsigned RPMs,
    Keys used to sign RPMs,
    Errors
    Output
    Results
    """

    image: str
    unsigned_rpms: list[str]
    signed_rpms_keys: list[str]
    results: dict[str, Any]
    error: str = ""
    output: str = ""


def get_rpmdb(container_image: str, target_dir: Path, runner: Callable = run) -> Path:
    """
    Extract RPM DB from a given container image reference
    :param container_image: the image to extract
    :param target_dir: the directory to extract the DB to
    :param runner: subprocess.run to run CLI commands
    :return: Path of the directory the DB extracted to
    """
    runner(
        [
            "oc",
            "image",
            "extract",
            container_image,
            "--path",
            f"/var/lib/rpm/:{target_dir}",
        ],
        capture_output=True,
        text=True,
        check=True,
    )
    return target_dir


def get_rpms_data(rpmdb: Path, runner: Callable = run) -> list[str]:
    """
    Get RPMs data from RPM DB
    :param rpmdb: path to RPM DB folder
    :param runner: subprocess.run to run CLI commands
    :return: list of RPMs with their signature information
    """
    rpm_strs = runner(
        [  # pylint: disable=duplicate-code
            "rpm",
            "-qa",
            "--qf",
            (
                "%{NAME}-%{VERSION}-%{RELEASE} "
                + "%|DSAHEADER?{%{DSAHEADER:pgpsig}}:{%|RSAHEADER?{%{RSAHEADER:pgpsig}}:{(none)}|}|\n"  # pylint: disable=line-too-long
            ),
            "--dbpath",
            str(rpmdb),
        ],
        capture_output=True,
        text=True,
        check=True,
    ).stdout.splitlines()
    return rpm_strs


def get_unsigned_rpms(rpms: list[str]) -> list[str]:
    """
    Get all unsigned RPMs from the list of RPMs
    Filters out the `gpg-pubkey` and return the unsigned RPMs
    :param rpms: list of RPMs
    :return: list of unsigned RPMs
    """
    unsigned_rpms: list[str] = [
        rpm.split()[0]
        for rpm in rpms
        if "Key ID" not in rpm and not rpm.startswith("gpg-pubkey")
    ]
    return unsigned_rpms


def get_signed_rpms_keys(rpms: list[str]) -> list[str]:
    """
    Get the keys used to sign RPMs
    :param rpms: list of RPMs
    :return: list of keys used to sign RPMs
    """
    signed_rpms_keys: list[str] = [
        rpm.split(", Key ID ")[-1] for rpm in rpms if "Key ID" in rpm
    ]
    return signed_rpms_keys


def generate_image_results(
    error: str, signed_rpms_keys: list[str], unsigned_rpms: list[str]
) -> dict[str, Any]:
    """
    Generate the results dictionary for an image
    :param error: error message
    :param signed_rpms_keys: a list of signed rpms keys
    :param unsigned_rpms: a list of unsigned rpms
    :returns: dictionary with results for the image
    """
    results: dict[str, Any] = {}
    if error != "":
        results["error"] = error
    else:
        results["keys"] = dict(Counter(signed_rpms_keys).most_common())
        results["keys"]["unsigned"] = len(unsigned_rpms)
    return results


def inspect_image_ref(
    image_url: str, image_digest: str, runner: Callable = run
) -> dict[str, Any]:
    """
    Inspect the image reference by running Skopeo
    :param image_url: image url to inspect
    :param image_digest: image digest to inspect
    :param runner: subprocess.run to run CLI commands
    :return: dictionary containing the inspection details
    """
    image_with_digest = f"docker://{':'.join(image_url.split(':')[:-1])}@{image_digest}"
    inspect = runner(
        [  # pylint: disable=duplicate-code
            "skopeo",
            "inspect",
            "--raw",
            image_with_digest,
        ],
        capture_output=True,
        text=True,
        check=True,
    )
    return json.loads(inspect.stdout)


def generate_image_output(image: str, unsigned_rpms: list[str], error: str) -> str:
    """
    Generates output for each container according to the scan
    :param image: image name
    :param unsigned_rpms: list of unsigned RPMs
    :param error: error string
    :return: output string
    """
    header = f"Image: {image}\n"
    if error != "":
        output = f"{header}Error occurred:\n{error}\n"
    elif unsigned_rpms:
        output = f"{header}Found unsigned RPMs:\n{unsigned_rpms}\n"
    else:
        output = f"{header}No unsigned RPMs found\n"
    return output


def get_images_from_inspection(
    inspect_results: dict[str, Any], image_url: str, image_digest: str
) -> list[str]:
    """
    Analyze the inspection results and return all the images for the image reference
    The image reference may refer to an image index (AKA Manifest list)
    In this case, extract all the digests from the manifests and return the list of image references
    :param inspect_results: inspection results dictionary
    :param image_url: image url to inspect
    :param image_digest: image digest to inspect
    :return: list of images to scan
    """
    if "manifests" in inspect_results:
        manifests = inspect_results["manifests"]
        image_list = [
            f"{':'.join(image_url.split(':')[:-1])}@" + man["digest"]
            for man in manifests
        ]
        return image_list
    return [f"{':'.join(image_url.split(':')[:-1])}@{image_digest}"]


def set_output_and_status(
    processed_image_list: list[ProcessedImage],
) -> Tuple[str, bool]:
    """
    Set output and status for the all task
    Create an output combined of all the image outputs
    If one of the scans was failed (has an error) - the failures_occurred should be true
    :param processed_image_list: list of processed images
    :return: output as a string, failures_occurred as a boolean
    """
    output = ""
    failures_occurred = False
    for img in processed_image_list:
        output += f"{img.output}\n{img.results}\n====================================\n"
        if img.error != "":
            failures_occurred = True
    return output, failures_occurred


def aggregate_results(processed_image_list: list[ProcessedImage]) -> dict[str, Any]:
    """
    Aggregate results from all images
    :param processed_image_list: list of ProcessedImages
    :return: dictionary with the aggregated results
    """
    aggregated_rpms_keys: list[str] = []
    aggregated_unsigned_rpms: list[str] = []
    aggregated_results: dict[str, Any] = {}

    for image in processed_image_list:
        if image.error != "":
            aggregated_results["error"] = image.error
            return aggregated_results
        aggregated_rpms_keys += image.signed_rpms_keys
        aggregated_unsigned_rpms += image.unsigned_rpms

    aggregated_results["keys"] = dict(Counter(aggregated_rpms_keys).most_common())
    aggregated_results["keys"]["unsigned"] = len(aggregated_unsigned_rpms)
    return aggregated_results


def generate_processed_image_digests(
    processed_images: list[ProcessedImage], image_url: str, image_digest: str
) -> dict[str, Any]:
    """
    Generate a dictionary containing the list of digests of the processed images.
    If the image reference provided is of an Image Manifest, this list will contain one digest.
    If it was an Image Index, then the result will contain the digest of the Image Index and a
    digest for each of the Image Manifests listed in the processed_images argument.
    :param processed_images: a list pf processed images
    :param image_url: Image URL of the image reference
    :param image_digest: Image digest of the image reference
    :return: Dictionary with the list of digests processed
    """
    # set unique values in case image_digest is also the image processed
    digests_set = set(
        [image_digest] + [image.image.split("@")[-1] for image in processed_images]
    )
    return {"image": {"pullspec": image_url, "digests": list(digests_set)}}


@dataclass(frozen=True)
class ImageProcessor:
    """
    Populate RPMs data for an image
    """

    workdir: Path
    db_getter: Callable[[str, Path], Path] = get_rpmdb
    rpms_getter: Callable[[Path], list[str]] = get_rpms_data
    unsigned_rpms_getter: Callable[[list[str]], list[str]] = get_unsigned_rpms
    signed_rpms_keys_getter: Callable[[list[str]], list[str]] = get_signed_rpms_keys
    generate_image_output: Callable[[str, list[str], str], str] = generate_image_output
    generate_image_results: Callable[[str, list[str], list[str]], dict[str, Any]] = (
        generate_image_results
    )

    def __call__(self, img: str) -> ProcessedImage:
        with tempfile.TemporaryDirectory(
            dir=str(self.workdir), prefix="rpmdb"
        ) as tmpdir:
            try:
                rpms_db = self.db_getter(img, Path(tmpdir))
                rpms_data = self.rpms_getter(rpms_db)
                unsigned_rpms = self.unsigned_rpms_getter(rpms_data)
                signed_rpms_keys = self.signed_rpms_keys_getter(rpms_data)

            except CalledProcessError as err:
                return ProcessedImage(
                    image=img,
                    unsigned_rpms=[],
                    signed_rpms_keys=[],
                    error=err.stderr,
                    output=self.generate_image_output(
                        img,
                        [],
                        err.stderr,
                    ),
                    results=self.generate_image_results(err.stderr, [], []),
                )
            return ProcessedImage(
                image=img,
                unsigned_rpms=unsigned_rpms,
                signed_rpms_keys=signed_rpms_keys,
                output=self.generate_image_output(img, unsigned_rpms, ""),
                results=self.generate_image_results(
                    "",
                    signed_rpms_keys,
                    unsigned_rpms,
                ),
            )


@click.command()
@click.option(
    "--image-url",
    help="Reference to container image",
    type=str,
    required=True,
)
@click.option(
    "--image-digest",
    help="Image digest",
    type=str,
    required=True,
)
@click.option(
    "--workdir",
    help="Path in which temporary directories will be created",
    type=click.Path(path_type=Path),
    required=True,
)
def main(  # pylint: disable=too-many-locals
    image_url: str,
    image_digest: str,
    workdir: Path,
) -> None:
    """Verify RPMs are signed"""
    status_path: Path = workdir / "status"
    results_path: Path = workdir / "results"
    images_processed_path: Path = workdir / "images_processed"

    # Exit in case of failure to process the image reference,
    # Create the result and the images processed and write them
    try:
        process = inspect_image_ref(image_url=image_url, image_digest=image_digest)
        images = get_images_from_inspection(
            inspect_results=process, image_url=image_url, image_digest=image_digest
        )
    except CalledProcessError as err:
        out = generate_image_output(image=image_url, unsigned_rpms=[], error=err.stderr)
        result: dict[str, str] = {"error": err.stderr}

        status_path.write_text("ERROR")
        results_path.write_text(json.dumps(result))

        images_processed: dict[str, Any] = {
            "image": {"pullspec": image_url, "digests": [image_digest]}
        }
        images_processed_path.write_text(json.dumps(images_processed))

        # Exit with error in case of failure to inspect the image
        sys.exit(
            f"{out}\n"
            f"Final results:\n"
            f"{json.dumps(result)}\n"
            f"Images processed:\n"
            f"{images_processed}"
        )

    processor = ImageProcessor(workdir=workdir)
    with ThreadPoolExecutor() as executor:
        processed_images: Iterable[ProcessedImage] = executor.map(processor, images)
    processed_images_list = list(processed_images)
    output, failures_occurred = set_output_and_status(
        processed_image_list=processed_images_list
    )
    aggregated_results = aggregate_results(processed_image_list=processed_images_list)
    results_path.write_text(json.dumps(aggregated_results))

    images_processed = generate_processed_image_digests(
        processed_images=processed_images_list,
        image_url=image_url,
        image_digest=image_digest,
    )
    images_processed_path.write_text(json.dumps(images_processed))

    if failures_occurred:
        status_path.write_text("ERROR")
        sys.exit(
            f"{output}\n"
            f"Final results:\n"
            f"{json.dumps(aggregated_results)}\n"
            f"Images processed:\n"
            f"{json.dumps(images_processed)}"
        )
    else:
        status_path.write_text("SUCCESS")
        print(
            f"{output}\n"
            f"Final results:\n"
            f"{json.dumps(aggregated_results)}\n"
            f"Images processed:\n"
            f"{json.dumps(images_processed)}"
        )


if __name__ == "__main__":
    main()  # pylint: disable=no-value-for-parameter
