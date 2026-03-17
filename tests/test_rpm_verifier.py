"""Test rpm_verifier.py end-to-end"""

# pylint: disable=too-many-lines

import json
from pathlib import Path
from subprocess import CalledProcessError, run
from textwrap import dedent
from typing import Any, Callable
from unittest.mock import MagicMock, create_autospec

import pytest
from pytest import MonkeyPatch

from verify_rpms import rpm_verifier
from verify_rpms.rpm_verifier import (
    ImageProcessor,
    ProcessedImage,
    aggregate_results,
    generate_image_output,
    generate_image_results,
    generate_processed_image_digests,
    get_images_from_inspection,
    get_rpmdb,
    get_rpms_data,
    get_signed_rpms_keys,
    get_unsigned_rpms,
    inspect_image_ref,
    set_output_and_status,
)


def test_get_rpmdb(tmp_path: Path) -> None:
    """Test get_rpmdb"""
    image = "my-image"
    mock_runner = create_autospec(run)
    out = get_rpmdb(
        container_image=image,
        target_dir=tmp_path,
        runner=mock_runner,
    )
    mock_runner.assert_called_once()
    assert mock_runner.call_args.args[0] == [
        "oc",
        "image",
        "extract",
        "my-image",
        "--path",
        f"/var/lib/rpm/:{tmp_path}",
    ]
    assert mock_runner.call_count == 1
    assert out == tmp_path


@pytest.mark.parametrize(
    ("test_input", "expected"),
    [
        pytest.param(
            dedent("""
                libssh-config-0.9.6-10.el8_8 RSA/SHA256, Tue 6 May , Key ID 1234567890
                python39-twisted-23.10.0-1.el8ap (none)
                libmodulemd-2.13.0-1.el8 RSA/SHA256, Wed 18 Aug , Key ID 1234567890
                gpg-pubkey-d4082792-5b32db75 (none)
                """).strip(),
            [
                "libssh-config-0.9.6-10.el8_8 RSA/SHA256, Tue 6 May , Key ID 1234567890",
                "python39-twisted-23.10.0-1.el8ap (none)",
                "libmodulemd-2.13.0-1.el8 RSA/SHA256, Wed 18 Aug , Key ID 1234567890",
                "gpg-pubkey-d4082792-5b32db75 (none)",
            ],
            id="Mix of signed and unsigned",
        ),
        pytest.param("", [], id="Empty list"),
    ],
)
def test_get_rpms_data(test_input: list[str], expected: list[str]) -> None:
    """Test get_rpms_data"""
    mock_runner = create_autospec(run)
    mock_runner.return_value.stdout = test_input
    result = get_rpms_data(rpmdb=Path("rpmdb_folder"), runner=mock_runner)
    mock_runner.assert_called_once()
    assert mock_runner.call_args.args[0] == [
        "rpm",
        "-qa",
        "--qf",
        (
            "%{NAME}-%{VERSION}-%{RELEASE} "
            + "%|DSAHEADER?{%{DSAHEADER:pgpsig}}:{%|RSAHEADER?{%{RSAHEADER:pgpsig}}:{(none)}|}|\n"
        ),
        "--dbpath",
        "rpmdb_folder",
    ]
    assert result == expected


@pytest.mark.parametrize(
    ("test_input", "expected"),
    [
        pytest.param(
            [
                "libssh-config-0.9.6-10.el8_8 RSA/SHA256, Tue 6 May , Key ID 1234567890",
                "python39-twisted-23.10.0-1.el8ap (none)",
                "libmodulemd-2.13.0-1.el8 RSA/SHA256, Wed 18 Aug , Key ID 1234567890",
                "gpg-pubkey-d4082792-5b32db75 (none)",
            ],
            ["python39-twisted-23.10.0-1.el8ap"],
            id="Mix of signed and unsigned",
        ),
        pytest.param(
            [
                "libssh-config-0.9.6-10.el8_8 RSA/SHA256, Tue 6 May , Key ID 1234567890",
                "libmodulemd-2.13.0-1.el8 RSA/SHA256, Wed 18 Aug , Key ID 1234567890",
            ],
            [],
            id="All signed",
        ),
        pytest.param(
            [
                "libssh-config-0.9.6-10.el8_8 (none)",
                "python39-twisted-23.10.0-1.el8ap (none)",
                "libmodulemd-2.13.0-1.el8 (none)",
            ],
            [
                "libssh-config-0.9.6-10.el8_8",
                "python39-twisted-23.10.0-1.el8ap",
                "libmodulemd-2.13.0-1.el8",
            ],
            id="All unsigned",
        ),
        pytest.param([], [], id="Empty list"),
    ],
)
def test_get_unsigned_rpms(test_input: list[str], expected: list[str]) -> None:
    """Test get_unsigned_rpms"""
    result = get_unsigned_rpms(rpms=test_input)
    assert result == expected


@pytest.mark.parametrize(
    ("test_input", "expected"),
    [
        pytest.param(
            [
                "libssh-config-0.9.6-10.el8_8 RSA/SHA256, Tue 6 May , Key ID 1234567890",
                "python39-twisted-23.10.0-1.el8ap (none)",
                "libmodulemd-2.13.0-1.el8 RSA/SHA256, Wed 18 Aug , Key ID 1234567890",
                "gpg-pubkey-d4082792-5b32db75 (none)",
                "libtest1-2.13.0-1.el8 RSA/SHA256, Wed 18 Aug , Key ID 0987654321",
            ],
            ["1234567890", "1234567890", "0987654321"],
            id="Mix of signed and unsigned",
        ),
        pytest.param(
            [
                "python39-twisted-23.10.0-1.el8ap (none)",
                "gpg-pubkey-d4082792-5b32db75 (none)",
            ],
            [],
            id="All unsigned",
        ),
        pytest.param([], [], id="Empty list"),
    ],
)
def test_get_signed_rpms_keys(test_input: list[str], expected: list[str]) -> None:
    """Test get_signed_rpms_keys"""
    result = get_signed_rpms_keys(rpms=test_input)
    assert result == expected


@pytest.mark.parametrize(
    ("error", "signed_rpms_keys", "unsigned_rpms", "expected_results"),
    [
        pytest.param(
            "",
            ["1234", "1234", "5678"],
            [],
            {"keys": {"1234": 2, "5678": 1, "unsigned": 0}},
            id="No unsigned RPMs + no errors",
        ),
        pytest.param(
            "",
            [],
            ["my-rpm", "another-rpm"],
            {"keys": {"unsigned": 2}},
            id="An image with multiple unsigned RPMs",
        ),
        pytest.param(
            "Failed to run command",
            [],
            [],
            {"error": "Failed to run command"},
            id="Error when running command, return error",
        ),
    ],
)
def test_generate_image_results(
    error: str,
    signed_rpms_keys: list[str],
    unsigned_rpms: list[str],
    expected_results: dict[str, Any],
) -> None:
    """Test generate_results"""
    results = generate_image_results(
        error=error, signed_rpms_keys=signed_rpms_keys, unsigned_rpms=unsigned_rpms
    )
    assert results == expected_results


def test_inspect_image_ref() -> None:
    """Test inspect_image_ref"""
    mock_runner = create_autospec(run)
    mock_runner.return_value.stdout = '{"inspect": "success"}'
    image_url = "quay.io/test/image:tag"
    image_digest = "sha256:1234567890"
    inspect_image_ref(
        image_url=image_url, image_digest=image_digest, runner=mock_runner
    )
    mock_runner.assert_called_once()
    assert mock_runner.call_args.args[0] == [
        "skopeo",
        "inspect",
        "--raw",
        f"docker://{':'.join(image_url.split(':')[:-1])}@{image_digest}",
    ]
    assert mock_runner.call_count == 1


@pytest.mark.parametrize(
    ("image", "unsigned_rpms", "error", "expected_print"),
    [
        pytest.param(
            "image1",
            [],
            "",
            dedent("""
                Image: image1
                No unsigned RPMs found
                """).strip(),
            id="No unsigned RPMs + no errors. Do not report failures",
        ),
        pytest.param(
            "image1",
            ["my-rpm"],
            "",
            dedent("""
                Image: image1
                Found unsigned RPMs:
                ['my-rpm']
                """).strip(),
            id="Unsigned RPM + no errors. Report failures",
        ),
        pytest.param(
            "image1",
            ["my-rpm", "another-rpm"],
            "",
            dedent("""
                Image: image1
                Found unsigned RPMs:
                ['my-rpm', 'another-rpm']
                """).strip(),
            id="An image with multiple unsigned RPMs Report failure",
        ),
        pytest.param(
            "image1",
            [],
            "Failed to run command",
            dedent("""
                Image: image1
                Error occurred:
                Failed to run command
                """).strip(),
            id="Error when running command, Report failure",
        ),
    ],
)
def test_generate_image_output(
    image: str,
    unsigned_rpms: list[str],
    error: str,
    expected_print: str,
) -> None:
    """Test generate_output"""
    print_out = generate_image_output(
        image=image, unsigned_rpms=unsigned_rpms, error=error
    )
    assert print_out == f"{expected_print}\n"


@pytest.mark.parametrize(
    ("inspect_results", "image_url", "image_digest", "expected_output"),
    [
        pytest.param(
            {
                "schemaVersion": 2,
                "mediaType": "application/vnd.docker.distribution.manifest.v2+json",
                "config": {
                    "mediaType": "application/vnd.docker.container.image.v1+json",
                    "size": 13889,
                    "digest": "sha256:001122334455",
                },
                "layers": [
                    {
                        "mediaType": "application/vnd.docker.image.rootfs.diff.tar.gzip",
                        "size": 39307955,
                        "digest": "sha256:554433221100",
                    },
                    {
                        "mediaType": "application/vnd.docker.image.rootfs.diff.tar.gzip",
                        "size": 123637579,
                        "digest": "sha256:9876543210",
                    },
                ],
            },
            "quay.io/image:5000/test:tag",
            "sha256:1234567890",
            ["quay.io/image:5000/test@sha256:1234567890"],
            id="Not an image index, image with port number, return image reference",
        ),
        pytest.param(
            {
                "schemaVersion": 2,
                "mediaType": "application/vnd.docker.distribution.manifest.v2+json",
                "config": {
                    "mediaType": "application/vnd.docker.container.image.v1+json",
                    "size": 13889,
                    "digest": "sha256:001122334455",
                },
                "layers": [
                    {
                        "mediaType": "application/vnd.docker.image.rootfs.diff.tar.gzip",
                        "size": 39307955,
                        "digest": "sha256:554433221100",
                    },
                    {
                        "mediaType": "application/vnd.docker.image.rootfs.diff.tar.gzip",
                        "size": 123637579,
                        "digest": "sha256:9876543210",
                    },
                ],
            },
            "quay.io/image/test:tag",
            "sha256:1234567890",
            ["quay.io/image/test@sha256:1234567890"],
            id="Not an image index, image without port number, return image reference",
        ),
        pytest.param(
            {
                "manifests": [
                    {
                        "digest": "sha256:amd64123456789",
                        "mediaType": "application/vnd.docker.distribution.manifest.v2+json",
                        "platform": {"architecture": "amd64", "os": "linux"},
                        "size": 429,
                    },
                    {
                        "digest": "sha256:arm64123456789",
                        "mediaType": "application/vnd.docker.distribution.manifest.v2+json",
                        "platform": {"architecture": "arm64", "os": "linux"},
                        "size": 429,
                    },
                    {
                        "digest": "sha256:ppc64le123456789",
                        "mediaType": "application/vnd.docker.distribution.manifest.v2+json",
                        "platform": {"architecture": "ppc64le", "os": "linux"},
                        "size": 429,
                    },
                    {
                        "digest": "sha256:s390x123456789",
                        "mediaType": "application/vnd.docker.distribution.manifest.v2+json",
                        "platform": {"architecture": "s390x", "os": "linux"},
                        "size": 429,
                    },
                ],
                "mediaType": "application/vnd.docker.distribution.manifest.list.v2+json",
                "schemaVersion": 2,
            },
            "quay.io/test/image:tag",
            "sha256:1234567890",
            [
                "quay.io/test/image@sha256:amd64123456789",
                "quay.io/test/image@sha256:arm64123456789",
                "quay.io/test/image@sha256:ppc64le123456789",
                "quay.io/test/image@sha256:s390x123456789",
            ],
            id="Image index, return list of images from manifests",
        ),
    ],
)
def test_get_images_from_inspection(
    inspect_results: dict[str, str],
    image_url: str,
    image_digest: str,
    expected_output: list[str],
) -> None:
    """Test get_images_from_inspection"""
    result = get_images_from_inspection(
        inspect_results=inspect_results, image_url=image_url, image_digest=image_digest
    )
    assert result == expected_output


@pytest.mark.parametrize(
    ("processed_image_list", "expected_output", "expected_failures"),
    [
        pytest.param(
            [
                ProcessedImage(
                    image="image1",
                    signed_rpms_keys=["123", "456"],
                    unsigned_rpms=[],
                    error="",
                    output="image1 output",
                    results={"keys": {"123": 1, "456": 1, "unsigned": 0}},
                ),
                ProcessedImage(
                    image="image2",
                    signed_rpms_keys=["321", "654"],
                    unsigned_rpms=[],
                    error="",
                    output="image2 output",
                    results={"keys": {"321": 1, "654": 1, "unsigned": 0}},
                ),
            ],
            dedent("""
         image1 output
         {'keys': {'123': 1, '456': 1, 'unsigned': 0}}
         ====================================
         image2 output
         {'keys': {'321': 1, '654': 1, 'unsigned': 0}}
         ====================================
         """).strip(),
            False,
            id="no errors, no unsigned",
        ),
        pytest.param(
            [
                ProcessedImage(
                    image="image1",
                    signed_rpms_keys=["123", "456"],
                    unsigned_rpms=["unsigned1", "unsigned2"],
                    error="",
                    output="image1 output",
                    results={"keys": {"123": 1, "456": 1, "unsigned": 2}},
                ),
                ProcessedImage(
                    image="image2",
                    signed_rpms_keys=["321", "654"],
                    unsigned_rpms=[],
                    error="",
                    output="image2 output",
                    results={"keys": {"321": 1, "654": 1, "unsigned": 0}},
                ),
            ],
            dedent("""
         image1 output
         {'keys': {'123': 1, '456': 1, 'unsigned': 2}}
         ====================================
         image2 output
         {'keys': {'321': 1, '654': 1, 'unsigned': 0}}
         ====================================
         """).strip(),
            False,
            id="no errors, image with unsigned, should not expect failure",
        ),
        pytest.param(
            [
                ProcessedImage(
                    image="image1",
                    signed_rpms_keys=[],
                    unsigned_rpms=[],
                    error="Error message",
                    output="image1 output",
                    results={"error": "Error message"},
                ),
                ProcessedImage(
                    image="image2",
                    signed_rpms_keys=["321", "654"],
                    unsigned_rpms=[],
                    error="",
                    output="image2 output",
                    results={"keys": {"321": 1, "654": 1, "unsigned": 0}},
                ),
            ],
            dedent("""
         image1 output
         {'error': 'Error message'}
         ====================================
         image2 output
         {'keys': {'321': 1, '654': 1, 'unsigned': 0}}
         ====================================
         """).strip(),
            True,
            id="image with error, should expect failure",
        ),
    ],
)
def test_set_output_and_status(
    processed_image_list: list[ProcessedImage],
    expected_output: str,
    expected_failures: bool,
) -> None:
    """Test set_output_and _status"""
    out, failures = set_output_and_status(processed_image_list=processed_image_list)
    assert failures == expected_failures
    assert out == f"{expected_output}\n"


@pytest.mark.parametrize(
    ("processed_image_list", "expected_output"),
    [
        pytest.param(
            [
                ProcessedImage(
                    image="image1",
                    signed_rpms_keys=["123", "456"],
                    unsigned_rpms=[],
                    error="",
                    output="image1 output",
                    results={},
                ),
                ProcessedImage(
                    image="image2",
                    signed_rpms_keys=["123", "654"],
                    unsigned_rpms=[],
                    error="",
                    output="image2 output",
                    results={},
                ),
            ],
            {"keys": {"123": 2, "456": 1, "654": 1, "unsigned": 0}},
            id="no errors, no unsigned",
        ),
        pytest.param(
            [
                ProcessedImage(
                    image="image1",
                    signed_rpms_keys=["123", "456"],
                    unsigned_rpms=["unsigned1", "unsigned2"],
                    error="",
                    output="image1 output",
                    results={},
                ),
                ProcessedImage(
                    image="image2",
                    signed_rpms_keys=["123", "654"],
                    unsigned_rpms=["unsigned1"],
                    error="",
                    output="image2 output",
                    results={},
                ),
            ],
            {"keys": {"123": 2, "456": 1, "654": 1, "unsigned": 3}},
            id="no errors, with unsigned",
        ),
        pytest.param(
            [
                ProcessedImage(
                    image="image1",
                    signed_rpms_keys=["123", "456"],
                    unsigned_rpms=["unsigned1", "unsigned2"],
                    error="",
                    output="image1 output",
                    results={},
                ),
                ProcessedImage(
                    image="image2",
                    signed_rpms_keys=["123", "654"],
                    unsigned_rpms=["unsigned1"],
                    error="This is an error",
                    output="image2 output",
                    results={},
                ),
            ],
            {"error": "This is an error"},
            id="with error",
        ),
    ],
)
def test_aggregate_results(
    processed_image_list: list[ProcessedImage],
    expected_output: dict[str, Any],
) -> None:
    """Test aggregate_results"""
    result = aggregate_results(processed_image_list=processed_image_list)
    assert result == expected_output


@pytest.mark.parametrize(
    ("processed_images_list", "image_url", "image_digest", "expected_output"),
    [
        pytest.param(
            [
                ProcessedImage(
                    image="image1@sha256:1234567890",
                    signed_rpms_keys=["123", "456"],
                    unsigned_rpms=[],
                    error="",
                    output="image1 output",
                    results={},
                ),
            ],
            "image1:tag",
            "sha256:1234567890",
            {
                "image": {
                    "pullspec": "image1:tag",
                    "digests": [
                        "sha256:1234567890",
                    ],
                }
            },
            id="image reference is not an image digest (has only one image)",
        ),
        pytest.param(
            [
                ProcessedImage(
                    image="image1@sha256:0987654321",
                    signed_rpms_keys=["123", "456"],
                    unsigned_rpms=[],
                    error="",
                    output="image1 output",
                    results={},
                ),
                ProcessedImage(
                    image="image1@sha256:1122334455",
                    signed_rpms_keys=["123", "456"],
                    unsigned_rpms=[],
                    error="",
                    output="image1 output",
                    results={},
                ),
                ProcessedImage(
                    image="image1@sha256:5544332211",
                    signed_rpms_keys=["123", "456"],
                    unsigned_rpms=[],
                    error="",
                    output="image1 output",
                    results={},
                ),
            ],
            "image1:tag",
            "sha256:1234567890",
            {
                "image": {
                    "pullspec": "image1:tag",
                    "digests": [
                        "sha256:1234567890",
                        "sha256:0987654321",
                        "sha256:1122334455",
                        "sha256:5544332211",
                    ],
                }
            },
            id="image reference is of image digest kind (has few images in manifest)",
        ),
    ],
)
def test_generate_images_processed_result(
    processed_images_list: list[ProcessedImage],
    image_url: str,
    image_digest: str,
    expected_output: dict[str, Any],
) -> None:
    """test generate_images_processed_result"""
    result: dict[str, Any] = generate_processed_image_digests(
        processed_images=processed_images_list,
        image_url=image_url,
        image_digest=image_digest,
    )
    # use set to ignore the order inside the list
    assert set(result["image"]["digests"]) == set(expected_output["image"]["digests"])


class TestImageProcessor:
    """Test ImageProcessor's callable"""

    @pytest.fixture()
    def mock_db_getter(self) -> MagicMock:
        """mocked db_getter function"""
        return MagicMock()

    @pytest.fixture()
    def mock_rpms_getter(self) -> MagicMock:
        """mocked rpms_getter function"""
        return MagicMock()

    @pytest.fixture()
    def mock_unsigned_rpms_getter(self) -> MagicMock:
        """mocked unsigned_rpms_getter function"""
        return MagicMock()

    @pytest.fixture()
    def mock_signed_rpms_keys_getter(self) -> MagicMock:
        """mocked signed_rpms_keys_getter function"""
        return MagicMock()

    @pytest.fixture()
    def mock_generate_image_output(self) -> MagicMock:
        """mocked generate_image_output function"""
        return MagicMock()

    @pytest.fixture()
    def mock_generate_image_results(self) -> MagicMock:
        """mocked generate_image_output function"""
        return MagicMock()

    @pytest.mark.parametrize(
        (
            "rpms_data",
            "expected_unsigned",
            "expected_signed_keys",
            "expected_output",
            "expected_results",
        ),
        [
            # pytest.param([], [], [], id="No RPMs"),
            pytest.param(
                [
                    "libssh-config-0.9.6-10.el8_8 RSA/SHA256, Tue 6 May , Key ID 1234567890",
                    "python39-twisted-23.10.0-1.el8ap (none)",
                    "libmodulemd-2.13.0-1.el8 RSA/SHA256, Wed 18 Aug , Key ID 1234567890",
                    "gpg-pubkey-d4082792-5b32db75 (none)",
                ],
                ["python39-twisted-23.10.0-1.el8ap"],
                ["1234567890", "1234567890"],
                dedent("""
                Image: my-img
                Found unsigned RPMs:
                ['python39-twisted-23.10.0-1.el8ap']
                """).strip(),
                {"keys": {"1234567890": 2, "unsigned": 1}},
                id="one unsigned, two signed",
            ),
        ],
    )
    def test_call(  # pylint: disable=too-many-arguments,too-many-positional-arguments
        self,
        mock_db_getter: MagicMock,
        expected_unsigned: list[str],
        expected_signed_keys: list[str],
        expected_output: str,
        expected_results: dict[str, Any],
        mock_rpms_getter: MagicMock,
        tmp_path: Path,
        rpms_data: list[str],
    ) -> None:
        """Test ImageProcessor's callable"""
        mock_rpms_getter.return_value = rpms_data
        instance = ImageProcessor(
            workdir=tmp_path,
            db_getter=mock_db_getter,
            rpms_getter=mock_rpms_getter,
        )
        img = "my-img"
        out = instance(img)
        assert out == ProcessedImage(
            image=img,
            unsigned_rpms=expected_unsigned,
            signed_rpms_keys=expected_signed_keys,
            error="",
            output=f"{expected_output}\n",
            results=expected_results,
        )

    def test_call_db_getter_exception(
        # pylint: disable=too-many-arguments,too-many-positional-arguments
        self,
        mock_db_getter: MagicMock,
        mock_rpms_getter: MagicMock,
        mock_unsigned_rpms_getter: MagicMock,
        mock_signed_rpms_keys_getter: MagicMock,
        mock_generate_image_output: MagicMock,
        mock_generate_image_results: MagicMock,
        tmp_path: Path,
    ) -> None:
        """Test ImageProcessor exception in db_getter"""
        stderr = "Failed to run command"
        mock_db_getter.side_effect = CalledProcessError(
            stderr=stderr, returncode=1, cmd=""
        )
        mock_generate_image_results.return_value = {"results": "results"}
        mock_generate_image_output.return_value = "output"
        instance = ImageProcessor(
            workdir=tmp_path,
            db_getter=mock_db_getter,
            rpms_getter=mock_rpms_getter,
            unsigned_rpms_getter=mock_unsigned_rpms_getter,
            signed_rpms_keys_getter=mock_signed_rpms_keys_getter,
            generate_image_output=mock_generate_image_output,
            generate_image_results=mock_generate_image_results,
        )
        img = "my-img"
        out = instance(img)
        mock_db_getter.assert_called_once()
        mock_generate_image_output.assert_called_once()
        mock_generate_image_results.assert_called_once()
        mock_rpms_getter.assert_not_called()
        mock_unsigned_rpms_getter.assert_not_called()
        mock_signed_rpms_keys_getter.assert_not_called()
        assert out == ProcessedImage(
            image=img,
            unsigned_rpms=[],
            signed_rpms_keys=[],
            error=stderr,
            output="output",
            results={"results": "results"},
        )

    def test_call_rpm_getter_exception(
        # pylint: disable=too-many-arguments,too-many-positional-arguments
        self,
        mock_db_getter: MagicMock,
        mock_rpms_getter: MagicMock,
        mock_unsigned_rpms_getter: MagicMock,
        mock_signed_rpms_keys_getter: MagicMock,
        mock_generate_image_output: MagicMock,
        mock_generate_image_results: MagicMock,
        tmp_path: Path,
    ) -> None:
        """Test ImageProcessor exception in rpms_getter"""
        stderr = "Failed to run command"
        mock_rpms_getter.side_effect = CalledProcessError(
            stderr=stderr, returncode=1, cmd=""
        )
        mock_generate_image_results.return_value = {"results": "results"}
        mock_generate_image_output.return_value = "output"
        instance = ImageProcessor(
            workdir=tmp_path,
            db_getter=mock_db_getter,
            rpms_getter=mock_rpms_getter,
            unsigned_rpms_getter=mock_unsigned_rpms_getter,
            signed_rpms_keys_getter=mock_signed_rpms_keys_getter,
            generate_image_output=mock_generate_image_output,
            generate_image_results=mock_generate_image_results,
        )
        img = "my-img"
        out = instance(img)
        mock_db_getter.assert_called_once()
        mock_rpms_getter.assert_called_once()
        mock_generate_image_output.assert_called_once()
        mock_generate_image_results.assert_called_once()
        mock_unsigned_rpms_getter.assert_not_called()
        mock_signed_rpms_keys_getter.assert_not_called()
        assert out == ProcessedImage(
            image=img,
            unsigned_rpms=[],
            signed_rpms_keys=[],
            error=stderr,
            output="output",
            results={"results": "results"},
        )

    def test_call_unsigned_rpms_getter_exception(
        # pylint: disable=too-many-arguments,too-many-positional-arguments
        self,
        mock_db_getter: MagicMock,
        mock_rpms_getter: MagicMock,
        mock_unsigned_rpms_getter: MagicMock,
        mock_signed_rpms_keys_getter: MagicMock,
        mock_generate_image_output: MagicMock,
        mock_generate_image_results: MagicMock,
        tmp_path: Path,
    ) -> None:
        """Test ImageProcessor exception in unsigned_rpms_getter"""
        stderr = "Failed to run command"
        mock_unsigned_rpms_getter.side_effect = CalledProcessError(
            stderr=stderr, returncode=1, cmd=""
        )
        mock_generate_image_results.return_value = {"results": "results"}
        mock_generate_image_output.return_value = "output"
        instance = ImageProcessor(
            workdir=tmp_path,
            db_getter=mock_db_getter,
            rpms_getter=mock_rpms_getter,
            unsigned_rpms_getter=mock_unsigned_rpms_getter,
            signed_rpms_keys_getter=mock_signed_rpms_keys_getter,
            generate_image_output=mock_generate_image_output,
            generate_image_results=mock_generate_image_results,
        )
        img = "my-img"
        out = instance(img)
        mock_db_getter.assert_called_once()
        mock_rpms_getter.assert_called_once()
        mock_generate_image_output.assert_called_once()
        mock_generate_image_results.assert_called_once()
        mock_unsigned_rpms_getter.assert_called_once()
        mock_signed_rpms_keys_getter.assert_not_called()
        assert out == ProcessedImage(
            image=img,
            unsigned_rpms=[],
            signed_rpms_keys=[],
            error=stderr,
            output="output",
            results={"results": "results"},
        )

    def test_call_signed_rpms_keys_getter_exception(
        # pylint: disable=too-many-arguments,too-many-positional-arguments
        self,
        mock_db_getter: MagicMock,
        mock_rpms_getter: MagicMock,
        mock_unsigned_rpms_getter: MagicMock,
        mock_signed_rpms_keys_getter: MagicMock,
        mock_generate_image_output: MagicMock,
        mock_generate_image_results: MagicMock,
        tmp_path: Path,
    ) -> None:
        """Test ImageProcessor exception in unsigned_rpms_getter"""
        stderr = "Failed to run command"
        mock_signed_rpms_keys_getter.side_effect = CalledProcessError(
            stderr=stderr, returncode=1, cmd=""
        )
        mock_generate_image_results.return_value = {"results": "results"}
        mock_generate_image_output.return_value = "output"
        instance = ImageProcessor(
            workdir=tmp_path,
            db_getter=mock_db_getter,
            rpms_getter=mock_rpms_getter,
            unsigned_rpms_getter=mock_unsigned_rpms_getter,
            signed_rpms_keys_getter=mock_signed_rpms_keys_getter,
            generate_image_output=mock_generate_image_output,
            generate_image_results=mock_generate_image_results,
        )
        img = "my-img"
        out = instance(img)
        mock_db_getter.assert_called_once()
        mock_rpms_getter.assert_called_once()
        mock_generate_image_output.assert_called_once()
        mock_generate_image_results.assert_called_once()
        mock_unsigned_rpms_getter.assert_called_once()
        mock_signed_rpms_keys_getter.assert_called_once()
        assert out == ProcessedImage(
            image=img,
            unsigned_rpms=[],
            signed_rpms_keys=[],
            error=stderr,
            output="output",
            results={"results": "results"},
        )


class TestMain:
    """Testing main"""

    @pytest.fixture()
    def mock_image_processor(self, monkeypatch: MonkeyPatch) -> MagicMock:
        """Mock ImageProcessor"""
        mock: MagicMock = create_autospec(
            ImageProcessor,
            return_value=MagicMock(return_value="some output"),
        )
        monkeypatch.setattr(rpm_verifier, ImageProcessor.__name__, mock)
        return mock

    @pytest.fixture()
    def mock_aggregate_results(self, monkeypatch: MonkeyPatch) -> MagicMock:
        """Mock aggregate_results"""
        mock: MagicMock = create_autospec(
            aggregate_results, return_value={"results": "res"}
        )
        monkeypatch.setattr(rpm_verifier, aggregate_results.__name__, mock)
        return mock

    @pytest.fixture()
    def mock_generate_images_processed_result(
        self, monkeypatch: MonkeyPatch
    ) -> MagicMock:
        """Mock generate_images_processed_result"""
        mock: MagicMock = create_autospec(
            generate_processed_image_digests, return_value={"image": "image_processed"}
        )
        monkeypatch.setattr(
            rpm_verifier, generate_processed_image_digests.__name__, mock
        )
        return mock

    @pytest.fixture()
    def mock_inspect_image_ref(self, monkeypatch: MonkeyPatch) -> MagicMock:
        """Mock inspect_image_ref"""
        mock: MagicMock = create_autospec(
            inspect_image_ref,
            return_value=MagicMock(
                return_value={
                    "schemaVersion": 2,
                    "mediaType": "application/vnd.docker.distribution.manifest.v2+json",
                    "config": {
                        "mediaType": "application/vnd.docker.container.image.v1+json",
                        "size": 13889,
                        "digest": "sha256:001122334455",
                    },
                    "layers": [
                        {
                            "mediaType": "application/vnd.docker.image.rootfs.diff.tar.gzip",
                            "size": 39307955,
                            "digest": "sha256:554433221100",
                        },
                        {
                            "mediaType": "application/vnd.docker.image.rootfs.diff.tar.gzip",
                            "size": 123637579,
                            "digest": "sha256:9876543210",
                        },
                    ],
                },
            ),
        )
        monkeypatch.setattr(rpm_verifier, inspect_image_ref.__name__, mock)
        return mock

    @pytest.fixture()
    def mock_get_images_from_inspection(self, monkeypatch: MonkeyPatch) -> MagicMock:
        """Mock get_images_from_inspection"""
        mock: MagicMock = create_autospec(
            get_images_from_inspection,
            return_value=MagicMock(
                return_value=["quay.io/test/image@sha256:1234567890"]
            ),
        )
        monkeypatch.setattr(rpm_verifier, get_images_from_inspection.__name__, mock)
        return mock

    @pytest.fixture()
    def create_set_output_and_status_mock(
        self, monkeypatch: MonkeyPatch
    ) -> Callable[[bool], MagicMock]:
        """Create a generate_output mock with different results according to
        the `fail_unsigned` flag"""

        def _mock_set_output_and_status(with_failures: bool = False) -> MagicMock:
            """Monkey-patched generate_output"""
            mock = create_autospec(
                set_output_and_status, return_value=("some output", with_failures)
            )
            monkeypatch.setattr(rpm_verifier, set_output_and_status.__name__, mock)
            return mock

        return _mock_set_output_and_status

    @pytest.fixture()
    def mock_generate_image_output(self, monkeypatch: MonkeyPatch) -> MagicMock:
        """Mock get_images_from_inspection"""
        mock: MagicMock = create_autospec(
            generate_image_output,
            return_value=MagicMock(return_value="some output"),
        )
        monkeypatch.setattr(rpm_verifier, generate_image_output.__name__, mock)
        return mock

    def test_main(  # pylint: disable=too-many-arguments,too-many-positional-arguments
        self,
        mock_image_processor: MagicMock,
        mock_inspect_image_ref: MagicMock,
        mock_get_images_from_inspection: MagicMock,
        create_set_output_and_status_mock: MagicMock,
        mock_aggregate_results: MagicMock,
        mock_generate_images_processed_result: MagicMock,
        tmp_path: Path,
    ) -> None:
        """Test call to rpm_verifier.py main function"""
        status_path = tmp_path / "status"
        results_path = tmp_path / "results"
        images_processed_path: Path = tmp_path / "images_processed"

        set_output_and_status_mock = create_set_output_and_status_mock(
            with_failures=False
        )

        rpm_verifier.main(  # pylint: disable=no-value-for-parameter
            args=[
                "--image-url",
                "quay.io/test/image:tag",
                "--image-digest",
                "sha256:1234567890",
                "--workdir",
                tmp_path,
            ],
            obj={},
            standalone_mode=False,
        )
        assert status_path.read_text() == "SUCCESS"
        assert results_path.read_text() == json.dumps(
            mock_aggregate_results.return_value
        )
        assert images_processed_path.read_text() == json.dumps(
            mock_generate_images_processed_result.return_value
        )
        mock_inspect_image_ref.assert_called_once()
        mock_get_images_from_inspection.assert_called_once()
        mock_image_processor.assert_called_once_with(tmp_path)
        set_output_and_status_mock.assert_called_once()
        mock_aggregate_results.assert_called_once()
        mock_generate_images_processed_result.assert_called_once()

    def test_main_fail_with_errors(
        # pylint: disable=too-many-arguments,too-many-positional-arguments
        self,
        create_set_output_and_status_mock: MagicMock,
        mock_image_processor: MagicMock,  # pylint: disable=unused-argument
        mock_inspect_image_ref: MagicMock,  # pylint: disable=unused-argument
        mock_get_images_from_inspection: MagicMock,  # pylint: disable=unused-argument
        mock_aggregate_results: MagicMock,
        mock_generate_images_processed_result: MagicMock,
        tmp_path: Path,
    ) -> None:
        """Test call to rpm_verifier.py main function fails
        when whe 'fail-unsigned' flag is used and there are unsigned RPMs
        """
        status_path = tmp_path / "status"
        results_path = tmp_path / "results"
        images_processed_path: Path = tmp_path / "images_processed"

        set_output_and_status_mock = create_set_output_and_status_mock(
            with_failures=True
        )

        with pytest.raises(SystemExit) as err:
            rpm_verifier.main(  # pylint: disable=no-value-for-parameter
                args=[
                    "--image-url",
                    "quay.io/test/image:tag",
                    "--image-digest",
                    "sha256:1234567890",
                    "--workdir",
                    tmp_path,
                ],
                obj={},
                standalone_mode=False,
            )
        assert status_path.read_text() == "ERROR"
        assert results_path.read_text() == json.dumps(
            mock_aggregate_results.return_value
        )
        assert images_processed_path.read_text() == json.dumps(
            mock_generate_images_processed_result.return_value
        )
        mock_image_processor.assert_called_once_with(tmp_path)
        set_output_and_status_mock.assert_called_once()

        assert (
            err.value.code == f"{set_output_and_status_mock.return_value[0]}\n"
            f"Final results:\n"
            f"{json.dumps(mock_aggregate_results.return_value)}\n"
            f"Images processed:\n"
            f"{json.dumps(mock_generate_images_processed_result.return_value)}"
        )

    def test_main_inspect_image_ref_exception(
        # pylint: disable=too-many-arguments,too-many-positional-arguments
        self,
        mock_image_processor: MagicMock,
        mock_inspect_image_ref: MagicMock,
        mock_get_images_from_inspection: MagicMock,
        create_set_output_and_status_mock: MagicMock,
        mock_generate_image_output: MagicMock,
        mock_aggregate_results: MagicMock,
        mock_generate_images_processed_result: MagicMock,
        tmp_path: Path,
    ) -> None:
        """Test call to rpm_verifier.py main function"""
        status_path = tmp_path / "status"
        results_path = tmp_path / "results"
        images_processed_path: Path = tmp_path / "images_processed"
        set_output_and_status_mock = create_set_output_and_status_mock(
            with_failures=False
        )
        mock_inspect_image_ref.side_effect = CalledProcessError(
            stderr="error", returncode=1, cmd=""
        )
        with pytest.raises(SystemExit):
            rpm_verifier.main(  # pylint: disable=no-value-for-parameter
                args=[
                    "--image-url",
                    "quay.io/test/image:tag",
                    "--image-digest",
                    "sha256:1234567890",
                    "--workdir",
                    tmp_path,
                ],
                obj={},
                standalone_mode=False,
            )

        assert status_path.read_text() == "ERROR"
        assert results_path.read_text() == json.dumps({"error": "error"})
        assert images_processed_path.read_text() == json.dumps(
            {
                "image": {
                    "pullspec": "quay.io/test/image:tag",
                    "digests": ["sha256:1234567890"],
                }
            }
        )
        mock_inspect_image_ref.assert_called_once()
        mock_get_images_from_inspection.assert_not_called()
        mock_image_processor.assert_not_called()
        set_output_and_status_mock.assert_not_called()
        mock_generate_image_output.assert_called_once()
        mock_aggregate_results.assert_not_called()
        mock_generate_images_processed_result.assert_not_called()

    def test_main_get_images_from_inspection_exception(
        # pylint: disable=too-many-arguments,too-many-positional-arguments
        self,
        mock_image_processor: MagicMock,
        mock_inspect_image_ref: MagicMock,
        mock_get_images_from_inspection: MagicMock,
        create_set_output_and_status_mock: MagicMock,
        mock_generate_image_output: MagicMock,
        mock_aggregate_results: MagicMock,
        mock_generate_images_processed_result: MagicMock,
        tmp_path: Path,
    ) -> None:
        """Test call to rpm_verifier.py main function"""
        status_path = tmp_path / "status"
        results_path = tmp_path / "results"
        images_processed_path: Path = tmp_path / "images_processed"

        set_output_and_status_mock = create_set_output_and_status_mock(
            with_failures=False
        )
        mock_get_images_from_inspection.side_effect = CalledProcessError(
            stderr="error", returncode=1, cmd=""
        )
        with pytest.raises(SystemExit):
            rpm_verifier.main(  # pylint: disable=no-value-for-parameter
                args=[
                    "--image-url",
                    "quay.io/test/image:tag",
                    "--image-digest",
                    "sha256:1234567890",
                    "--workdir",
                    tmp_path,
                ],
                obj={},
                standalone_mode=False,
            )

        assert status_path.read_text() == "ERROR"
        assert results_path.read_text() == json.dumps({"error": "error"})
        assert images_processed_path.read_text() == json.dumps(
            {
                "image": {
                    "pullspec": "quay.io/test/image:tag",
                    "digests": ["sha256:1234567890"],
                }
            }
        )
        mock_inspect_image_ref.assert_called_once()
        mock_get_images_from_inspection.assert_called_once()
        mock_image_processor.assert_not_called()
        set_output_and_status_mock.assert_not_called()
        mock_generate_image_output.assert_called_once()
        mock_aggregate_results.assert_not_called()
        mock_generate_images_processed_result.assert_not_called()
