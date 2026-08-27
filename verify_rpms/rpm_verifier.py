#!/usr/bin/env python3

"""Verify RPMs are signed"""

import json
import sys
import tarfile
import tempfile
from collections import Counter
from concurrent.futures import ThreadPoolExecutor
from dataclasses import dataclass
from pathlib import Path
from subprocess import CalledProcessError, run
from typing import Any, Callable, Iterable, Tuple

import click
import tenacity
from tenacity import (
    retry,
    retry_if_exception,
    stop_after_attempt,
    wait_exponential_jitter,
)

TRANSIENT_PATTERNS = [
    "502 bad gateway",
    "503 service",
    "429",
    "connection reset",
    "connection refused",
    "could not resolve host",
    "unexpected end of json",
    "connection timed out",
    "tls handshake timeout",
    "etimedout",
]

# OLOT (OCI Layers On Top) adds model-data layers to container images.
# See https://github.com/containers/olot
OLOT_ANNOTATION_PREFIX = "olot.layer.content."

# No leading slash — matched against lstrip("/") paths in get_rpmdb_layer_indices.
# All known RPM DB directory paths across RHEL/Fedora/OSTree variants.
RPM_DB_PATHS = ("var/lib/rpm", "usr/lib/sysimage/rpm", "usr/share/rpm")

# Subset of RPM_DB_PATHS safe for ``oc image extract --path``.
# Excludes usr/share/rpm because OSTree images hardlink those files to
# /sysroot/ostree/repo/objects/ — ``oc image extract`` cannot resolve
# cross-path hardlinks and fails.
_OC_EXTRACT_DB_PATHS = ("var/lib/rpm", "usr/lib/sysimage/rpm")

_RPMDB_FILENAMES = frozenset({"rpmdb.sqlite", "Packages", "Packages.db"})


def _is_transient_error(exception: BaseException) -> bool:
    if isinstance(exception, CalledProcessError) and exception.stderr:
        stderr_str = (
            exception.stderr
            if isinstance(exception.stderr, str)
            else exception.stderr.decode("utf-8", errors="ignore")
        )
        return any(pattern in stderr_str.lower() for pattern in TRANSIENT_PATTERNS)
    return False


def _log_retry(retry_state: "tenacity.RetryCallState") -> None:
    sleep_time = retry_state.next_action.sleep if retry_state.next_action else 0
    exception = retry_state.outcome.exception() if retry_state.outcome else "unknown"
    print(
        f"WARNING: Transient error (attempt {retry_state.attempt_number}/4), "
        f"retrying in {sleep_time:.1f}s: "
        f"{exception}",
        file=sys.stderr,
    )


_retry_on_transient = retry(
    retry=retry_if_exception(_is_transient_error),
    stop=stop_after_attempt(4),
    wait=wait_exponential_jitter(initial=5, max=60, jitter=3),
    reraise=True,
    before_sleep=_log_retry,
)


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


def _has_rpmdb_files(directory: Path) -> bool:
    """Check whether a directory contains actual RPM database files.

    Returns False for empty directories or directories that only contain
    a symlink (e.g. when ``oc image extract`` copies a symlink instead of
    following it to the real database location).
    """
    if not directory.exists():
        return False
    for child in directory.iterdir():
        if child.is_symlink():
            continue
        if child.is_file() or child.is_dir():
            return True
    return False


def _rpmdb_tar_members() -> frozenset[str]:
    """Return all normalized tar member paths that could be RPM DB files."""
    paths: set[str] = set()
    for db_path in RPM_DB_PATHS:
        for fname in _RPMDB_FILENAMES:
            paths.add(f"{db_path}/{fname}")
    return frozenset(paths)


@_retry_on_transient
def _copy_image_oci(
    container_image: str,
    oci_dir: Path,
    runner: Callable = run,
) -> None:
    """Copy a container image to a local OCI layout directory."""
    runner(
        ["skopeo", "copy", f"docker://{container_image}", f"oci:{oci_dir}:latest"],
        capture_output=True,
        text=True,
        check=True,
    )


def _scan_layer_for_rpmdb(
    blob_path: Path,
    search_paths: frozenset[str],
    rpmdb_dir: Path,
) -> Path | None:
    """Scan a single layer tarball for RPM DB files.

    Handles hardlinks by resolving them to their target members within
    the same tar archive — this is the key capability that
    ``oc image extract`` lacks for OSTree images.
    """
    try:
        with tarfile.open(str(blob_path), "r:*") as tar:
            for member in tar.getmembers():
                name = member.name.lstrip("./")
                if name not in search_paths:
                    continue
                try:
                    fileobj = tar.extractfile(member)
                except (tarfile.StreamError, KeyError):
                    continue
                if fileobj is not None:
                    dest = rpmdb_dir / Path(name).name
                    dest.write_bytes(fileobj.read())
                    return rpmdb_dir
    except (tarfile.TarError, OSError) as exc:
        print(
            f"WARNING: Failed to scan layer blob {blob_path.name[:12]}: {exc}",
            file=sys.stderr,
        )
    return None


def _extract_rpmdb_from_layers(
    container_image: str,
    target_dir: Path,
    runner: Callable = run,
) -> Path | None:
    """Scan OCI layer tarballs to find and extract the RPM database.

    Used as a fallback when ``oc image extract`` cannot locate the DB
    (e.g. OSTree images where files are hardlinked to content-addressed
    objects that ``oc`` cannot resolve with path-filtered extraction).

    :param container_image: the image reference to scan
    :param target_dir: parent directory for the extraction
    :param runner: subprocess.run to run CLI commands
    :return: Path to directory containing extracted DB, or None
    """
    oci_dir = target_dir / "_oci"
    oci_dir.mkdir(exist_ok=True)

    _copy_image_oci(container_image, oci_dir, runner)

    index = json.loads((oci_dir / "index.json").read_text())
    manifest_digest = index["manifests"][0]["digest"]
    algo, hex_digest = manifest_digest.split(":", 1)
    manifest = json.loads((oci_dir / "blobs" / algo / hex_digest).read_text())

    search_paths = _rpmdb_tar_members()
    rpmdb_dir = target_dir / "_rpmdb"
    rpmdb_dir.mkdir(exist_ok=True)

    for layer in reversed(manifest.get("layers", [])):
        algo, hex_digest = layer["digest"].split(":", 1)
        blob_path = oci_dir / "blobs" / algo / hex_digest
        if not blob_path.exists():
            continue
        result = _scan_layer_for_rpmdb(blob_path, search_paths, rpmdb_dir)
        if result is not None:
            return result

    return None


@_retry_on_transient
def get_rpmdb(
    container_image: str,
    target_dir: Path,
    runner: Callable = run,
    layer_selectors: list[str] | None = None,
) -> Path:
    """
    Extract RPM DB from a given container image reference.

    Uses a single ``oc image extract`` call with multiple ``--path``
    flags to check both the legacy ``/var/lib/rpm`` and RHEL 10+
    ``/usr/lib/sysimage/rpm`` in one pass (one blob download).

    If neither path yields database files (e.g. OSTree images where the
    DB is at ``/usr/share/rpm`` behind hardlinks), falls back to
    downloading the image via ``skopeo`` and scanning layer tarballs
    directly with Python's ``tarfile`` module.

    :param container_image: the image to extract
    :param target_dir: the directory to extract the DB to
    :param runner: subprocess.run to run CLI commands
    :param layer_selectors: optional oc layer selectors (e.g. ["[0]", "[3]"])
    :return: Path of the directory the DB extracted to
    """
    if layer_selectors:
        image_args = [f"{container_image}{s}" for s in layer_selectors]
    else:
        image_args = [container_image]

    subdirs: dict[str, Path] = {}
    path_args: list[str] = []
    for db_path in _OC_EXTRACT_DB_PATHS:
        safe_name = db_path.replace("/", "_")
        subdir = target_dir / safe_name
        subdir.mkdir(exist_ok=True)
        subdirs[db_path] = subdir
        path_args.extend(["--path", f"/{db_path}/:{subdir}"])

    runner(
        ["oc", "image", "extract", *image_args, *path_args],
        capture_output=True,
        text=True,
        check=True,
    )

    for db_path in _OC_EXTRACT_DB_PATHS:
        if _has_rpmdb_files(subdirs[db_path]):
            return subdirs[db_path]

    print(
        f"WARNING: No RPM DB found via oc image extract for {container_image}, "
        "attempting direct layer scan",
        file=sys.stderr,
    )
    fallback = _extract_rpmdb_from_layers(container_image, target_dir, runner)
    if fallback is not None:
        return fallback

    print(
        f"WARNING: No RPM DB found in any known location for {container_image}",
        file=sys.stderr,
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


def _pullspec_without_tag(image_url: str) -> str:
    """Strip the trailing :tag from an image pullspec."""
    return image_url.rsplit(":", 1)[0]


def inspect_image_ref(
    image_url: str, image_digest: str, runner: Callable = run
) -> dict[str, Any]:
    """
    Inspect the image reference and return its raw manifest.
    :param image_url: image url to inspect
    :param image_digest: image digest to inspect
    :param runner: subprocess.run to run CLI commands
    :return: dictionary containing the manifest
    """
    image_ref = f"{_pullspec_without_tag(image_url)}@{image_digest}"
    return inspect_raw_manifest(image_ref, runner)


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
            f"{_pullspec_without_tag(image_url)}@{man['digest']}" for man in manifests
        ]
        return image_list
    return [f"{_pullspec_without_tag(image_url)}@{image_digest}"]


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

    for image in processed_image_list:
        if image.error != "":
            return generate_image_results(image.error, [], [])
        aggregated_rpms_keys += image.signed_rpms_keys
        aggregated_unsigned_rpms += image.unsigned_rpms

    return generate_image_results("", aggregated_rpms_keys, aggregated_unsigned_rpms)


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


@_retry_on_transient
def inspect_raw_manifest(image_ref: str, runner: Callable = run) -> dict[str, Any]:
    """
    Inspect an image reference and return its raw manifest.
    :param image_ref: full image reference (e.g. registry/repo@sha256:...)
    :param runner: subprocess.run to run CLI commands
    :return: parsed manifest dictionary
    """
    result = runner(
        [
            "skopeo",
            "inspect",
            "--raw",
            f"docker://{image_ref}",
        ],
        capture_output=True,
        text=True,
        check=True,
    )
    return json.loads(result.stdout)


def get_rpmdb_layer_indices(manifest: dict[str, Any]) -> list[int]:
    """
    Get the indices of layers that may contain RPM database files.

    Layers with olot annotations pointing to non-RPM paths (e.g. model files)
    are skipped. Layers without annotations are always included.
    :param manifest: parsed image manifest dictionary
    :return: list of zero-based layer indices that may contain RPM DB
    """
    indices: list[int] = []
    for i, layer in enumerate(manifest.get("layers", [])):
        annotations = layer.get("annotations") or {}
        if any(k.startswith(OLOT_ANNOTATION_PREFIX) for k in annotations):
            inlayerpath = annotations.get(f"{OLOT_ANNOTATION_PREFIX}inlayerpath", "")
            if inlayerpath and not any(
                inlayerpath.lstrip("/").startswith(p) for p in RPM_DB_PATHS
            ):
                continue
        indices.append(i)
    return indices


def compute_layer_selectors(
    images: list[str],
    runner: Callable = run,
    manifests: dict[str, dict[str, Any]] | None = None,
) -> dict[str, list[str]]:
    """
    Inspect each image manifest and compute layer selectors for images
    that have skippable layers (e.g. ModelCar model data layers).
    :param images: list of image references to inspect
    :param runner: subprocess.run to run CLI commands
    :param manifests: optional pre-fetched manifests to avoid redundant lookups
    :return: mapping of image ref to its layer selector strings
    """
    selectors: dict[str, list[str]] = {}
    for img in images:
        try:
            manifest = (manifests or {}).get(img)
            if manifest is None:
                manifest = inspect_raw_manifest(img, runner)
            total = len(manifest.get("layers", []))
            indices = get_rpmdb_layer_indices(manifest)
            if indices and len(indices) < total:
                selectors[img] = [f"[{i}]" for i in indices]
                print(
                    f"Selective extraction enabled for {img} "
                    f"(skipping {total - len(indices)} of {total} layers)",
                    file=sys.stderr,
                )
        except (
            CalledProcessError,
            json.JSONDecodeError,
            AttributeError,
            TypeError,
        ) as exc:
            print(
                f"Layer selector computation skipped for {img}: {exc}",
                file=sys.stderr,
            )
    return selectors


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


def _format_run_summary(
    output: str, results: dict[str, Any], images_processed: dict[str, Any]
) -> str:
    """Format the final summary message for output or sys.exit."""
    return (
        f"{output}\n"
        f"Final results:\n"
        f"{json.dumps(results)}\n"
        f"Images processed:\n"
        f"{json.dumps(images_processed)}"
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

        sys.exit(_format_run_summary(out, result, images_processed))

    # For single images (not indexes), process already contains the manifest
    # with layers — pass it to avoid a redundant network call.
    prefetched: dict[str, dict[str, Any]] | None = None
    if "layers" in process and "manifests" not in process:
        prefetched = {images[0]: process}

    layer_selectors = compute_layer_selectors(images, manifests=prefetched)

    def db_getter(image: str, target_dir: Path) -> Path:
        return get_rpmdb(
            container_image=image,
            target_dir=target_dir,
            runner=run,
            layer_selectors=layer_selectors.get(image),
        )

    processor = ImageProcessor(workdir=workdir, db_getter=db_getter)

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

    summary = _format_run_summary(output, aggregated_results, images_processed)
    if failures_occurred:
        status_path.write_text("ERROR")
        sys.exit(summary)
    else:
        status_path.write_text("SUCCESS")
        print(summary)


if __name__ == "__main__":
    main()  # pylint: disable=no-value-for-parameter
