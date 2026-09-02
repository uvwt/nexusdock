#!/usr/bin/env python3
"""Validate the current NexusDock product contract."""

from __future__ import annotations

import importlib.util
import pathlib
import re
import sys
from typing import Any

ROOT = pathlib.Path(__file__).resolve().parents[1]
CONTRACTS = ROOT / "contracts"
HTTP_SOURCE = ROOT / "internal" / "httpx"

REQUIRED_PATHS = {
    "/v1/recall",
    "/v1/recall/context-index",
    "/v1/runtime/nodes",
    "/v1/runtime/nodes/{nodeID}/tasks",
    "/v1/runtime/nodes/{nodeID}/skills",
    "/v1/runtime/nodes/{nodeID}/mcp",
    "/v1/workflow-templates",
}
FORBIDDEN_PATH_PREFIXES = (
    "/v1/backup",
    "/v1/recall/pack",
    "/v1/" + "arti" + "facts",
    "/v1/" + "arti" + "fact-fetches",
    "/v1/devices/{deviceId}/" + "arti" + "facts",
    "/v1/devices/{deviceId}/" + "arti" + "fact",
    "/v1/tasks",
    "/v1/runs",
    "/v1/skills",
    "/v1/skill-runs",
    "/v1/ops",
    "/v1/runtime/tasks",
    "/v1/runtime/skills",
    "/v1/runtime/mcp",
    "/v1/runtime/overview",
    "/v1/runtime/workflow-templates",
    "/v1/runtime/capabilities",
    "/v1/events",
    "/v1/schedules",
)
FORBIDDEN_FIELDS = {"recall_root"}
FORBIDDEN_ERROR_CODES = {
    "COMMAND_EXPIRED",
    "LEASE_EXPIRED",
    "SKILL_BLOCKED",
    "UNSUPPORTED_COMMAND",
}
ERROR_CODES = [
    "ADMIN_NOT_INITIALIZED",
	"AGENTDOCK_CONNECTION_UNAVAILABLE",
	"AGENTDOCK_DEVICE_TOKEN_FAILED",
    "AGENTDOCK_NODE_CREDENTIALS_UNAVAILABLE",
    "AGENTDOCK_NODE_DISABLED",
    "AGENTDOCK_NODE_EXISTS",
    "AGENTDOCK_NODE_LIST_FAILED",
	"AGENTDOCK_NODE_LOOKUP_FAILED",
    "AGENTDOCK_NODE_NOT_FOUND",
    "AGENTDOCK_NODE_OPERATION_FAILED",
    "AGENTDOCK_NODE_STORE_UNAVAILABLE",
	"AGENTDOCK_PAIRING_CODE_FAILED",
	"AGENTDOCK_PAIRING_CODE_INVALID",
	"AGENTDOCK_PAIRING_UNAVAILABLE",
    "AGENTDOCK_RUNTIME_BAD_RESPONSE",
    "AGENTDOCK_RUNTIME_REQUEST_FAILED",
    "AGENTDOCK_RUNTIME_UNAVAILABLE",
    "AGENTDOCK_RUNTIME_UNREACHABLE",
    "ARTIFACT_DOWNLOAD_BUSY",
    "ARTIFACT_NODE_OFFLINE",
    "ARTIFACT_NODE_RESPONSE_INVALID",
    "ARTIFACT_PROXY_FAILED",
    "ARTIFACT_SECRET_FAILED",
    "ARTIFACT_TOO_LARGE",
    "AUTH_STATUS_FAILED",
    "CAPTURE_CARD_FAILED",
    "CONFIRMATION_REQUIRED",
    "CONTEXT_INDEX_FAILED",
    "CREDENTIAL_POLICY_FAILED",
    "CREDENTIAL_UPDATE_FAILED",
    "CREDENTIAL_UPDATE_REQUIRED",
    "CSRF_REJECTED",
    "CURRENT_CREDENTIAL_INVALID",
    "DELETE_FAILED",
    "EMBEDDING_DISABLED",
    "EMBEDDING_REINDEX_FAILED",
    "EMBEDDING_SEARCH_FAILED",
    "EVOLUTION_NOT_CONFIGURED",
    "EVOLUTION_NOT_FOUND",
    "GIT_COMMIT_FAILED",
    "GIT_DIFF_FAILED",
    "GIT_LOG_FAILED",
    "GIT_VERSION_FAILED",
    "HTTPS_REQUIRED",
    "INTERNAL_ERROR",
    "INVALID_AGENTDOCK_NODE",
    "INVALID_CREDENTIALS",
	"INVALID_DEVICE_TOKEN",
    "INVALID_JSON",
    "INVALID_MCP_ACTION",
    "INVALID_MCP_NAME",
    "INVALID_PATH",
    "INVALID_PRIVATE_NOTE_MAINTENANCE_ACTION",
    "INVALID_PRIVATE_NOTE_PATH",
    "INVALID_PRIVATE_NOTE_STATUS_ACTION",
    "INVALID_QUERY",
    "INVALID_RUNTIME_SETTINGS",
    "INVALID_SKILL_FILE",
    "INVALID_SKILL_ID",
    "INVALID_TASK_ID",
    "INVALID_WORKFLOW_TEMPLATE",
    "LIFECYCLE_OPERATION_CONFLICT",
    "LIFECYCLE_POLICY_VERSION_CONFLICT",
    "LIFECYCLE_QUERY_FAILED",
    "LIFECYCLE_REVISION_CONFLICT",
    "LIFECYCLE_TRANSITION_FAILED",
    "LIST_CARDS_FAILED",
    "LIST_FAILED",
    "LOGIN_FAILED",
    "LOGIN_RATE_LIMITED",
    "LOGOUT_FAILED",
    "MCP_APPS_ENABLED_REQUIRED",
    "MCP_SETTINGS_READ_FAILED",
    "MCP_SETTINGS_UNAVAILABLE",
    "MCP_SETTINGS_UPDATE_FAILED",
    "MCP_TOKEN_RESET_FAILED",
    "MCP_TOKEN_UNAVAILABLE",
    "MISSING_CONTENT",
    "MISSING_PATH",
    "MISSING_QUERY",
    "MOVE_FAILED",
    "ORIGIN_REJECTED",
    "PATCH_FAILED",
    "PREVIEW_FAILED",
    "PRIVATE_NOTES_AGE_IDENTITY_INVALID",
    "PRIVATE_NOTES_AGE_RECIPIENT_INVALID",
    "PRIVATE_NOTES_AGE_RECIPIENT_MISSING",
    "PRIVATE_NOTES_ROOT_REQUIRED",
    "PRIVATE_NOTE_ENCRYPTED_MISSING",
    "PRIVATE_NOTE_EXISTS",
    "PRIVATE_NOTE_METADATA_INVALID",
    "PRIVATE_NOTE_METADATA_TOO_LARGE",
    "PRIVATE_NOTE_NOT_FOUND",
    "PRIVATE_NOTE_OPERATION_FAILED",
    "PRIVATE_NOTE_SYMLINK_REJECTED",
    "PRIVATE_NOTE_UNSAFE_FILE",
    "READ_FAILED",
    "REQUEST_TOO_LARGE",
    "SEARCH_CARDS_FAILED",
    "SEARCH_FAILED",
    "SESSION_LIST_FAILED",
    "SESSION_NOT_FOUND",
    "SESSION_REQUIRED",
    "SESSION_REVOKE_FAILED",
    "SETTINGS_READ_FAILED",
    "SETTINGS_UNAVAILABLE",
    "SETTINGS_UPDATE_FAILED",
    "TOOL_CONTRACT_MISMATCH",
    "UNAUTHORIZED",
    "USE_LOGOUT",
    "WORKFLOW_LIST_FAILED",
    "WORKFLOW_MATCH_FAILED",
    "WORKFLOW_PUBLISH_FAILED",
    "WORKFLOW_REGISTRY_FAILED",
    "WORKFLOW_REINDEX_FAILED",
    "WORKFLOW_RETIRE_FAILED",
    "WORKFLOW_RETIRE_OLD_FAILED",
    "WORKFLOW_TEMPLATE_NOT_ACTIVE",
    "WORKFLOW_TEMPLATE_NOT_FOUND",
    "WORKFLOW_VECTOR_INDEX_INVALID",
    "WORKFLOW_VERSION_IMMUTABLE",
    "WRITE_CARD_FAILED",
    "WRITE_FAILED",
]
FORBIDDEN_SCHEMAS = {
    "DeviceCapability",
    "DeviceEnrollmentRequest",
    "DeviceEnrollmentResponse",
    "EnrollmentTokenCreateRequest",
    "EnrollmentTokenCreateResponse",
    "CommandLeaseAction",
    "DeviceTokenRotationResponse",
    "DeviceRevokeRequest",
    "DeviceCommandCreateRequest",
    "DeviceEnvActionRequest",
    "DeviceHeartbeat",
    "DeviceStatus",
    "DeviceCommand",
    "CommandLease",
    "CommandProgress",
    "CommandResult",
}


_openapi: dict[str, Any] | None = None


def current_openapi() -> dict[str, Any]:
    global _openapi
    if _openapi is None:
        path = ROOT / "scripts" / "generate-contracts.py"
        spec = importlib.util.spec_from_file_location("generate_contracts", path)
        if spec is None or spec.loader is None:
            raise RuntimeError("unable to load scripts/generate-contracts.py")
        module = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(module)
        _openapi = module.build_openapi(module.build_schemas())
    return _openapi


def collect_refs(value: Any) -> list[str]:
    refs: list[str] = []
    if isinstance(value, dict):
        for key, item in value.items():
            if key == "$ref" and isinstance(item, str):
                refs.append(item)
            refs.extend(collect_refs(item))
    elif isinstance(value, list):
        for item in value:
            refs.extend(collect_refs(item))
    return refs


def validate_descriptions(value: Any, path: str, errors: list[str]) -> None:
    if isinstance(value, dict):
        if "properties" in value:
            for name, prop in value["properties"].items():
                if not isinstance(prop, dict) or not prop.get("description") and "$ref" not in prop:
                    errors.append(f"{path}.properties.{name}: missing description")
        for key, item in value.items():
            validate_descriptions(item, f"{path}.{key}", errors)
    elif isinstance(value, list):
        for index, item in enumerate(value):
            validate_descriptions(item, f"{path}[{index}]", errors)


def validate_openapi(errors: list[str]) -> None:
    document = current_openapi()
    if document.get("openapi") != "3.1.0":
        errors.append("OpenAPI version must be 3.1.0")
    schemas = document.get("components", {}).get("schemas", {})
    paths = document.get("paths", {})
    if not schemas:
        errors.append("OpenAPI components.schemas is empty")
    missing_paths = sorted(REQUIRED_PATHS - set(paths))
    if missing_paths:
        errors.append("missing current product paths: " + ", ".join(missing_paths))
    for route in paths:
        if route.startswith(FORBIDDEN_PATH_PREFIXES):
            errors.append(f"retired product path is still public: {route}")
    retired_schemas = sorted(FORBIDDEN_SCHEMAS & set(schemas))
    if retired_schemas:
        errors.append("retired product schemas are still public: " + ", ".join(retired_schemas))
    for schema_name, schema in schemas.items():
        properties = schema.get("properties", {}) if isinstance(schema, dict) else {}
        retired_fields = sorted(FORBIDDEN_FIELDS & set(properties))
        if retired_fields:
            errors.append(f"{schema_name}: retired fields still public: " + ", ".join(retired_fields))
    for reference in collect_refs(document):
        prefix = "#/components/schemas/"
        if reference.startswith(prefix) and reference[len(prefix):] not in schemas:
            errors.append(f"unresolved OpenAPI ref: {reference}")
    validate_descriptions({"schemas": schemas}, "components", errors)
    operation_ids: set[str] = set()
    for route, path_item in paths.items():
        for method, operation in path_item.items():
            operation_id = operation.get("operationId")
            if not operation_id:
                errors.append(f"{method.upper()} {route}: missing operationId")
            elif operation_id in operation_ids:
                errors.append(f"duplicate operationId: {operation_id}")
            else:
                operation_ids.add(operation_id)


def validate_openapi_references(errors: list[str]) -> None:
    document = current_openapi()
    components = document.get("components", {})

    def walk(value: object, location: str) -> None:
        if isinstance(value, dict):
            reference = value.get("$ref")
            if isinstance(reference, str) and reference.startswith("#/components/"):
                parts = reference.split("/")
                if len(parts) != 4 or parts[2] not in components or parts[3] not in components[parts[2]]:
                    errors.append(f"OpenAPI reference is unresolved at {location}: {reference}")
            for key, item in value.items():
                walk(item, f"{location}/{key}")
        elif isinstance(value, list):
            for index, item in enumerate(value):
                walk(item, f"{location}/{index}")

    walk(document, "$")


def normalized_route(path: str) -> str:
    """Ignore wildcard names while preserving the HTTP resource shape."""
    return re.sub(r"\{[^}]+\}", "{}", path)


def go_function_body(text: str, function_name: str) -> str:
    match = re.search(rf"func \(s \*Server\) {re.escape(function_name)}\([^)]*\) \{{", text)
    if match is None:
        return ""
    start = match.end() - 1
    depth = 0
    for index in range(start, len(text)):
        if text[index] == "{":
            depth += 1
        elif text[index] == "}":
            depth -= 1
            if depth == 0:
                return text[start + 1:index]
    return ""


def source_query_parameters() -> set[tuple[str, str, str]]:
    result: set[tuple[str, str, str]] = set()
    registration = re.compile(r'HandleFunc\("([A-Z]+) ([^"]+)",[^\n]*?s\.([A-Za-z0-9_]+)')
    query_patterns = (
        re.compile(r'r\.URL\.Query\(\)\.Get\("([^"]+)"\)'),
        re.compile(r'queryInt\(r,\s*"([^"]+)"'),
    )
    for path in sorted(HTTP_SOURCE.glob("*.go")):
        if path.name.endswith("_test.go"):
            continue
        text = path.read_text(encoding="utf-8")
        for method, route, handler in registration.findall(text):
            if not route.startswith("/v1/"):
                continue
            body = go_function_body(text, handler)
            for pattern in query_patterns:
                for name in pattern.findall(body):
                    result.add((method, normalized_route(route), name))
    return result


def validate_query_parameter_coverage(errors: list[str]) -> None:
    document = current_openapi()
    contract = {
        (method.upper(), normalized_route(route), parameter["name"])
        for route, path_item in document.get("paths", {}).items()
        for method, operation in path_item.items()
        for parameter in operation.get("parameters", [])
        if isinstance(parameter, dict) and parameter.get("in") == "query"
    }
    source = source_query_parameters()
    for method, route, name in sorted(source - contract):
        errors.append(f"HTTP query parameter is missing from OpenAPI: {method} {route} ?{name}")
    for method, route, name in sorted(contract - source):
        errors.append(f"OpenAPI query parameter has no handler read: {method} {route} ?{name}")


def validate_source_route_coverage(errors: list[str]) -> None:
    document = current_openapi()
    contract_operations = {
        (method.upper(), normalized_route(route))
        for route, path_item in document.get("paths", {}).items()
        for method in path_item
    }

    source_operations: set[tuple[str, str]] = set()
    route_pattern = re.compile(r'HandleFunc\("([A-Z]+) ([^"]+)"')
    for path in sorted(HTTP_SOURCE.glob("*.go")):
        if path.name.endswith("_test.go"):
            continue
        for method, route in route_pattern.findall(path.read_text(encoding="utf-8")):
            if route == "/health" or route.startswith("/v1/"):
                if route != "/v1/":
                    source_operations.add((method, normalized_route(route)))

    for method, route in sorted(source_operations - contract_operations):
        errors.append(f"HTTP route is missing from OpenAPI: {method} {route}")
    for method, route in sorted(contract_operations - source_operations):
        errors.append(f"OpenAPI operation has no HTTP route: {method} {route}")


def validate_retired_contract_dirs(errors: list[str]) -> None:
    if CONTRACTS.exists():
        errors.append("retired contracts directory remains")
    if (ROOT / "generated").exists():
        errors.append("retired generated client directory remains")


def source_public_error_codes() -> set[str]:
    codes: set[str] = set()
    for path in sorted(HTTP_SOURCE.glob("*.go")):
        if path.name.endswith("_test.go"):
            continue
        text = path.read_text(encoding="utf-8")
        codes.update(re.findall(r'(?:writeError|writeAuthError)\([^\n]*?"([A-Z][A-Z0-9_]+)"', text))
        codes.update(re.findall(r'Code:\s*"([A-Z][A-Z0-9_]+)"', text))
        codes.update(re.findall(r'code\s*:=\s*"([A-Z][A-Z0-9_]+)"', text))

    private_notes = ROOT / "internal" / "privatenotes"
    for path in sorted(private_notes.glob("*.go")):
        if path.name.endswith("_test.go"):
            continue
        text = path.read_text(encoding="utf-8")
        codes.update(re.findall(r'coded\("([A-Z][A-Z0-9_]+)"', text))
        if path.name == "store.go":
            codes.update(re.findall(r'return\s+"([A-Z][A-Z0-9_]+)"', text))
    return codes


def validate_error_code_catalog(errors: list[str]) -> None:
    if len(ERROR_CODES) != len(set(ERROR_CODES)):
        errors.append("error code catalog contains duplicate codes")
    if ERROR_CODES != sorted(ERROR_CODES):
        errors.append("error code catalog must be sorted")
    catalog = set(ERROR_CODES)
    for code in sorted(source_public_error_codes() - catalog):
        errors.append(f"public source error code is missing from catalog: {code}")
    for code in sorted(FORBIDDEN_ERROR_CODES & catalog):
        errors.append(f"retired public error code remains in catalog: {code}")


def main() -> int:
    errors: list[str] = []
    validate_openapi(errors)
    validate_openapi_references(errors)
    validate_source_route_coverage(errors)
    validate_query_parameter_coverage(errors)
    validate_retired_contract_dirs(errors)
    validate_error_code_catalog(errors)
    if errors:
        for error in errors:
            print(f"ERROR: {error}", file=sys.stderr)
        return 1
    print("contracts valid: current Nexus paths, schemas and product boundary")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
