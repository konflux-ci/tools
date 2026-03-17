"""test_odcs_fetcher.py - test odcs_fetcher"""

from pathlib import Path
from textwrap import dedent

import pytest
import responses

from generate_compose.odcs_fetcher import ODCSFetcher, ODCSResultReference
from generate_compose.odcs_requester import ODCSRequestReferences


@pytest.mark.parametrize(
    ("composes", "urls"),
    [
        pytest.param(
            [dedent("""
                    [odcs-111]
                    name=compose 1
                    """)],
            ["https://url1"],
            id="single compose",
        ),
        pytest.param(
            [
                dedent("""
                    [odcs-111]
                    name=compose 1
                    """),
                dedent("""
                    [odcs-222]
                    name=compose 2
                    """),
            ],
            ["https://url1", "https://url2"],
            id="multiple composes",
        ),
    ],
)
def test_odcs_fetcher(tmp_path: Path, composes: list[str], urls: list[str]) -> None:
    """test ODCSFetcher.__call__"""
    req_ref = ODCSRequestReferences(compose_urls=urls)
    fetcher = ODCSFetcher(compose_dir_path=tmp_path)

    with responses.RequestsMock() as mock:
        for compose, url in zip(composes, urls):
            mock.add(responses.GET, url, body=compose)
        out: ODCSResultReference = fetcher(request_reference=req_ref)

    files_content = [
        p.read_text(encoding="utf-8") for p in out.compose_dir_path.iterdir()
    ]
    extensions = [p.suffix for p in out.compose_dir_path.iterdir()]

    assert set(composes) == set(files_content)
    assert set(extensions) == set([".repo"])
