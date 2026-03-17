"""test_odcs_requester.py - test odcs_requester"""

from textwrap import dedent
from typing import Callable
from unittest.mock import MagicMock, call, create_autospec, sentinel

import pytest
from odcs.client.odcs import ODCS, ComposeSourceGeneric  # type: ignore
from requests.exceptions import HTTPError

from generate_compose.odcs_requester import ODCSRequester
from generate_compose.odcs_session import get_odcs_session
from generate_compose.protocols import (
    ODCSComposeConfig,
    ODCSComposesConfigs,
    ODCSRequestReferences,
)


class TestODCSRequester:
    """Test odcs_compose_generator.py"""

    @pytest.fixture()
    def compose_url(self) -> str:
        """Example compose-url, as close to the one expected"""
        return "http://download.eng.bos.redhat.com/odcs/prod/odcs-222222"

    @pytest.fixture()
    def create_odcs_mock(self, compose_url: str) -> Callable[[int, bool], MagicMock]:
        """Create an ODCS mock with specific results for the compose method"""

        def _mock_odcs_compose(
            num_of_composes: int = 1,
            exception: bool = False,
            num_of_failed: int = 0,
        ) -> MagicMock:
            """Mock for ODCS.compose"""
            mock: MagicMock = create_autospec(ODCS)
            if exception:
                mock.request_compose.side_effect = HTTPError
            else:
                mock.request_compose.side_effect = [
                    {
                        "result_repofile": "arbitrary-temp-value",
                        "id": i,
                        "state_name": "not-ready",
                    }
                    for i in range(1, num_of_composes + 1)
                ]
                mock.wait_for_compose.side_effect = [
                    {
                        "result_repofile": f"{compose_url}",
                        "id": i,
                        "state_name": (
                            "done" if i > num_of_failed else sentinel.fail_state
                        ),
                        "state_reason": (
                            "ok" if i > num_of_failed else sentinel.fail_reason
                        ),
                    }
                    for i in range(1, num_of_composes + 1)
                ]
            return mock

        return _mock_odcs_compose

    @pytest.fixture()
    def compose_source(self) -> MagicMock:
        """Creates a ComposeSource with dynamic typing"""
        mock: MagicMock = create_autospec(ComposeSourceGeneric)
        return mock

    @pytest.mark.parametrize(
        "composes_configs",
        [
            pytest.param(
                ODCSComposesConfigs([ODCSComposeConfig(spec=compose_source)]),
                id="single compose",
            ),
            pytest.param(
                ODCSComposesConfigs(
                    [
                        ODCSComposeConfig(spec=compose_source),
                        ODCSComposeConfig(spec=compose_source),
                    ]
                ),
                id="multiple composes",
            ),
        ],
    )
    def test_odcs_requester(
        self,
        create_odcs_mock: Callable,
        compose_url: str,
        composes_configs: ODCSComposesConfigs,
    ) -> None:
        """test ODCSRequester.__call__"""
        num_of_composes: int = len(composes_configs.configs)
        mock_odcs = create_odcs_mock(num_of_composes)
        odcs_session_getter = create_autospec(get_odcs_session, return_value=mock_odcs)
        odcs_requester = ODCSRequester(
            client_id="some-client",
            client_secret="some-secret",
            odcs_session_getter=odcs_session_getter,
        )
        req_ref = odcs_requester(compose_configs=composes_configs)

        expected_calls = [
            call(composes_configs.configs[0].spec) for _ in range(num_of_composes)
        ]
        assert mock_odcs.request_compose.call_args_list == expected_calls

        assert mock_odcs.request_compose.call_count == num_of_composes
        expected_calls = [
            call(compose_id) for compose_id in range(1, num_of_composes + 1)
        ]
        mock_odcs.wait_for_compose.assert_has_calls(expected_calls, any_order=False)
        assert mock_odcs.wait_for_compose.call_count == num_of_composes

        expected_req_ref = ODCSRequestReferences(
            compose_urls=[compose_url] * num_of_composes
        )
        assert req_ref == expected_req_ref

    def test_odcs_requester_compose_failure_should_raise(
        self, create_odcs_mock: Callable, compose_source: ComposeSourceGeneric
    ) -> None:
        """test ODCSRequester.__call__ raise an exception when the compose fails"""
        mock_odcs = create_odcs_mock(exception=True)
        odcs_session_getter = create_autospec(get_odcs_session, return_value=mock_odcs)
        odcs_requester = ODCSRequester(
            client_id="some-client",
            client_secret="some-secret",
            odcs_session_getter=odcs_session_getter,
        )
        composes_config = ODCSComposesConfigs([ODCSComposeConfig(spec=compose_source)])
        with pytest.raises(HTTPError):
            odcs_requester(compose_configs=composes_config)
        assert mock_odcs.wait_for_compose.call_count == 0

    def test_odcs_requester_compose_timeout_failure_should_raise(
        self, create_odcs_mock: Callable, compose_source: ComposeSourceGeneric
    ) -> None:
        """test ODCSRequester.__call__ raise an exception when
        waiting for compose throws an exception due to a timeout"""

        mock_odcs = create_odcs_mock(exception=False)
        mock_odcs.wait_for_compose.side_effect = RuntimeError

        odcs_session_getter = create_autospec(get_odcs_session, return_value=mock_odcs)
        odcs_requester = ODCSRequester(
            client_id="some-client",
            client_secret="some-secret",
            odcs_session_getter=odcs_session_getter,
        )
        composes_config = ODCSComposesConfigs([ODCSComposeConfig(spec=compose_source)])

        with pytest.raises(RuntimeError):
            odcs_requester(compose_configs=composes_config)
        assert mock_odcs.request_compose.call_count == 1
        assert mock_odcs.wait_for_compose.call_count == 1

    @pytest.mark.parametrize(
        ("composes_configs", "num_of_failed", "expected_output"),
        [
            pytest.param(
                ODCSComposesConfigs([ODCSComposeConfig(spec=compose_source)]),
                1,
                dedent("""
                    Failed to generate some composes:
                    1: state=sentinel.fail_state reason=sentinel.fail_reason
                    """).strip(),
                id="single compose - failed",
            ),
            pytest.param(
                ODCSComposesConfigs(
                    [
                        ODCSComposeConfig(spec=compose_source),
                        ODCSComposeConfig(spec=compose_source),
                    ]
                ),
                1,
                dedent("""
                    Failed to generate some composes:
                    1: state=sentinel.fail_state reason=sentinel.fail_reason
                    """).strip(),
                id="multiple composes - some failed",
            ),
            pytest.param(
                ODCSComposesConfigs(
                    [
                        ODCSComposeConfig(spec=compose_source),
                        ODCSComposeConfig(spec=compose_source),
                    ]
                ),
                2,
                dedent("""
                    Failed to generate some composes:
                    1: state=sentinel.fail_state reason=sentinel.fail_reason
                    2: state=sentinel.fail_state reason=sentinel.fail_reason
                    """).strip(),
                id="multiple composes - all failed",
            ),
        ],
    )
    def test_odcs_requester_failed_generating(
        self,
        create_odcs_mock: Callable,
        composes_configs: ODCSComposesConfigs,
        num_of_failed: int,
        expected_output: str,
    ) -> None:
        """test ODCSRequester.__call__ in a scenario in which no exceptions were
        raised during compose creation, but some of the composes did not finish
        successfully"""

        mock_odcs = create_odcs_mock(num_of_composes=2, num_of_failed=num_of_failed)
        odcs_session_getter = create_autospec(get_odcs_session, return_value=mock_odcs)
        odcs_requester = ODCSRequester(
            client_id="some-client",
            client_secret="some-secret",
            odcs_session_getter=odcs_session_getter,
        )

        with pytest.raises(RuntimeError) as ex:
            odcs_requester(compose_configs=composes_configs)
        assert str(ex.value) == expected_output
