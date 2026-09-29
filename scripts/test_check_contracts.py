#!/usr/bin/env python3
from __future__ import annotations

import importlib.util
import pathlib
import unittest

SCRIPT = pathlib.Path(__file__).with_name("check-contracts.py")
SPEC = importlib.util.spec_from_file_location("check_contracts", SCRIPT)
assert SPEC is not None and SPEC.loader is not None
CHECK = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(CHECK)


class ChiRouteParserTest(unittest.TestCase):
    def test_multiline_route_and_nested_handler_are_parsed(self) -> None:
        source = """
func routes(r chi.Router, s *Server) {
    r.Get(
        "/v1/recall/*",
        withWildcardPathValue(
            "path",
            s.readRecall,
        ),
    )
}
"""
        self.assertEqual(CHECK.source_route_operations(source), [("GET", "/v1/recall/*")])
        self.assertEqual(
            CHECK.source_route_registrations(source),
            [("GET", "/v1/recall/*", "readRecall")],
        )

    def test_route_examples_in_comments_and_strings_are_ignored(self) -> None:
        source = """
// r.Get("/v1/commented", s.commented)
var example = `r.Post("/v1/example", s.example)`
r.Get("/v1/real", s.real)
"""
        self.assertEqual(CHECK.source_route_operations(source), [("GET", "/v1/real")])
        self.assertEqual(
            CHECK.source_route_registrations(source),
            [("GET", "/v1/real", "real")],
        )

    def test_comments_and_parentheses_do_not_end_route_call_early(self) -> None:
        source = """
r.Post(
    "/v1/demo",
    // Keep handler wrapped so middleware remains local to this endpoint.
    wrap(func() http.HandlerFunc { return s.demo }()),
)
"""
        self.assertEqual(CHECK.source_route_operations(source), [("POST", "/v1/demo")])
        self.assertEqual(
            CHECK.source_route_registrations(source),
            [("POST", "/v1/demo", "demo")],
        )


class GeneratedAPIContractTest(unittest.TestCase):
    def test_openapi_hash_is_canonical(self) -> None:
        left = {"b": [2, 1], "a": {"label": "中文", "enabled": True}}
        right = {"a": {"enabled": True, "label": "中文"}, "b": [2, 1]}
        self.assertEqual(CHECK.openapi_contract_hash(left), CHECK.openapi_contract_hash(right))

    def test_generated_types_hash_marker_is_strict(self) -> None:
        digest = "a" * 64
        self.assertEqual(
            CHECK.generated_api_types_source_hash(f"// header\n// OpenAPI-SHA256: {digest}\n"),
            digest,
        )
        self.assertIsNone(CHECK.generated_api_types_source_hash("// OpenAPI-SHA256: not-a-hash\n"))


if __name__ == "__main__":
    unittest.main()
