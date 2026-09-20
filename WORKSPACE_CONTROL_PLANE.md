# Workspace Control Plane

This document describes the workspace-first Runtime controls introduced by the workspace-control-plane upgrade. These controls are additive: existing routed AgentDock calls that provide node_id but omit workspace_id keep their legacy behavior.

## Scope and invariants

- AgentDock remains the execution layer. NexusDock does not replace node-local files, commands, Skills, dynamic MCP servers, browser state, or tasks.
- Node selection remains explicit. This release does not automatically choose an AgentDock node.
- Project Workspaces are opt-in per tool call through workspace_id.
- Strict Workspace mode is fail-closed when NexusDock cannot prove that a boundary is safe.
- Runtime Workspace management APIs are protected management APIs and use the existing signed-in administrator session.
- No push, merge, or deployment behavior is implied by Workspace creation.

## Workspace model

A Runtime Workspace contains:

| Field | Purpose |
| --- | --- |
| id | Stable 2-63 character lowercase identifier using letters, digits, and hyphens |
| name | Human-readable project name |
| node_id | AgentDock node bound to the Workspace |
| project_root | Primary project filesystem root; writes must stay here |
| domain | Optional allowed site domain for URL-bearing arguments |
| allowed_mcp | Dynamic MCP server allowlist |
| context_roots | Additional read-only filesystem roots |
| design_authorities | Declared design/content authority files for clients and workflows; stored as metadata in this release |
| route_authority | Optional route authority file, relative to project_root or located under a readable Workspace root |

Example:

    {
      "id": "example-project",
      "name": "Example Project",
      "node_id": "node_xxx",
      "project_root": "D:\\website\\Example",
      "domain": "example.com",
      "allowed_mcp": ["example-wordpress"],
      "context_roots": ["D:\\website\\Example"],
      "design_authorities": ["DESIGN.md"],
      "route_authority": "website_link.md"
    }

Workspace CRUD endpoints:

| Method | Endpoint |
| --- | --- |
| GET | /v1/runtime/workspaces |
| POST | /v1/runtime/workspaces |
| GET | /v1/runtime/workspaces/{workspaceID} |
| PATCH | /v1/runtime/workspaces/{workspaceID} |
| DELETE | /v1/runtime/workspaces/{workspaceID} |

The configured node must already exist in NexusDock.

## Strict Workspace execution

Every routed AgentDock tool continues to require node_id. Supplying workspace_id enables strict Workspace checks before the call is forwarded.

| Boundary | Strict Workspace behavior |
| --- | --- |
| Node | node_id must match the Workspace-bound node |
| Dynamic MCP | mcp_tool_call / mcp_tool_inspect require a qualified tool from an allowed server; mcp_tool_search requires an explicit allowed server; unrestricted MCP listing is denied |
| Filesystem reads | Must stay under project_root or context_roots; skill:// reads remain allowed |
| Filesystem writes | file_edit writes and moves must stay under project_root |
| Domain | URL/URI/domain/host/endpoint-like arguments must stay on the configured domain or its subdomains |
| Route Authority | Route-like arguments on mcp_tool_call must exist in the configured authority file |
| Command execution | exec_command is denied in strict Workspace mode today |

The exec_command restriction is intentional. Current AgentDock does not expose a Nexus-verifiable OS sandbox capability, so NexusDock cannot prove that an arbitrary shell command remains inside project_root. Calls without workspace_id retain the legacy command behavior.

The Route Authority file itself must resolve inside project_root or context_roots. Relative parent traversal and absolute paths outside those readable roots are rejected.

Before forwarding a successful strict call, NexusDock removes node_id and workspace_id from the argument object so Nexus-only routing metadata is not leaked to the AgentDock tool implementation.

## Route Authority

Route Authority is optional. When configured, NexusDock reads the authority file from the Workspace-bound node immediately before a route-bearing dynamic MCP call.

Route candidates include common URL/route fields such as url, page_url, post_url, target_url, permalink, route, path, slug, and fields ending in _permalink, _route, or _slug.

Important boundaries:

- The Route Authority guard currently applies to mcp_tool_call.
- It validates route candidates, not arbitrary free-form content.
- External URLs are handled by the Workspace domain policy.
- If the bound node cannot provide a Workspace-safe filesystem.read capability, or the authority file cannot be read, the call fails closed.
- The configured authority file must be inside project_root or context_roots.

## Audit

Every routed AgentDock tool call is recorded in the existing append-only audit_events store.

The Runtime read endpoint is:

    GET /v1/runtime/audit

Supported query parameters:

| Parameter | Values |
| --- | --- |
| limit | 1-500, default 100 |
| workspace_id | Exact Workspace ID |
| node_id | Exact node ID |
| risk | low, medium, high |
| result | succeeded, failed |

The audit record includes actor, node/workspace/tool identity, risk, result, request ID, duration, and redacted metadata. NexusDock does not persist full tool arguments, content, patches, passwords, secrets, tokens, or API keys in this tool audit path.

Current risk classification:

| Risk | Tools |
| --- | --- |
| high | file_edit, exec_command, mcp_tool_call, mcp_manage |
| medium | file_publish, task_manage, workflow_template_manage |
| low | Other routed AgentDock tools |

## Concurrency and resource locking

Routed AgentDock tool calls pass through a concurrency gate and deterministic resource locks before invocation.

Environment variables:

| Variable | Default | Valid range |
| --- | ---: | ---: |
| NEXUS_TOOL_CONCURRENCY_GLOBAL | 16 | 1-256 |
| NEXUS_TOOL_CONCURRENCY_PER_NODE | 4 | 1-64 |
| NEXUS_TOOL_CONCURRENCY_PER_WORKSPACE | 3 | 1-64 |
| NEXUS_TOOL_QUEUE_TIMEOUT_SECONDS | 30 | 1-300 |

Resource locks serialize conflicting operations on the same derived file, route, remote object, task/workflow, or MCP-configuration resource while allowing unrelated resources to remain concurrent. Acquisition order is deterministic to avoid lock-order deadlocks.

## Node health and capability discovery

The node status endpoint is:

    GET /v1/runtime/nodes/{nodeID}/status

It reports:

- health state: ready, offline, disabled, or incompatible;
- online/enabled/compatibility state and last-seen age;
- protocol compatibility details;
- logical capability resolutions;
- Workspace-safe capability resolutions;
- effective concurrency budgets;
- routing semantics.

This release intentionally reports:

    explicit_node_required: true
    automatic_node_selection: false

Capability resolution is based on protocol compatibility plus advertised capabilities, not on a hard-coded AgentDock runtime version. Provider aliases can therefore evolve without forcing upper layers to depend on AgentDock internals.

## Compatibility matrix

| Scenario | Behavior |
| --- | --- |
| Existing client sends node_id only | Compatible; legacy behavior is preserved |
| Client adds workspace_id | Strict Workspace policy is enabled for that call |
| AgentDock protocol equals the shared ConnectionProtocolVersion | Compatible |
| AgentDock runtime version changes but protocol/capabilities remain compatible | Supported by the compatibility abstraction |
| AgentDock protocol is missing or different | Node is incompatible and Workspace-safe routing is not offered |
| Node is disabled | Health state is disabled; not a routing candidate |
| Node is offline | Health state is offline; not a routing candidate |
| Strict Workspace + exec_command | Denied until a verifiable sandbox capability exists |
| Configured Route Authority is unreadable or resolves outside Workspace roots | Route-bearing dynamic MCP call fails closed |
| Existing data store from schema v3 | Migrates forward to schema v4 with runtime_workspaces |
| Existing calls without Workspace adoption | No forced migration |

The implementation was regression-tested against the current AgentDock 0.8.3 node in this development environment. Compatibility remains defined by the shared protocol contract and advertised capabilities rather than by the 0.8.3 version string itself.

## Recommended rollout

### Stage 0 - deploy with legacy routing unchanged

Deploy with the default concurrency values. Do not add workspace_id to existing clients yet. Verify health/readiness, current AgentDock connectivity, and normal legacy tool calls.

### Stage 1 - register Workspaces

Create one Workspace per project and bind it to the intended node. Start with accurate project_root, allowed_mcp, and domain values. Add context_roots only when a project genuinely needs read access outside project_root.

### Stage 2 - migrate read-heavy calls

Add workspace_id to read/list/search/image and other low-risk calls first. Confirm audit entries contain the expected workspace_id and that no required filesystem or MCP access is denied.

### Stage 3 - enable Route Authority and dynamic MCP writes

Configure route_authority for projects where URL ownership must be enforced. Migrate dynamic MCP calls and file writes only after the allowlist, roots, domain, and authority file are validated.

### Stage 4 - tune concurrency

Use /v1/runtime/audit and /v1/runtime/nodes/{nodeID}/status to observe failures, queue pressure, and effective limits. Adjust the four NEXUS_TOOL_* variables conservatively; avoid raising global and per-node limits aggressively at the same time.

### Stage 5 - keep shell execution legacy until sandbox support exists

Do not work around the strict exec_command denial by weakening Workspace filesystem checks. Keep command execution on the legacy node-only path when it is required, or wait for an AgentDock sandbox capability that NexusDock can negotiate and verify.

## Rollback

Workspace adoption is per call, so the operational rollback is simple:

1. Stop sending workspace_id from the affected client/workflow.
2. Leave Workspace records in place for later reuse, or delete them through the protected management API.
3. Restore concurrency environment variables to their defaults if tuning caused queue pressure.
4. Do not downgrade the data store without a tested database rollback path; schema v4 adds runtime_workspaces.

Removing workspace_id restores legacy routing behavior; it does not delete project data or modify AgentDock configuration.

## Release gate

Before merging or deploying this control-plane change, verify at minimum:

- go test ./...
- go vet ./...
- go build ./cmd/nexusdock
- contract generation/checks
- repository boundary checks
- migration/e2e/security tests
- git diff --check
- formatting/tidy checks in a line-ending-neutral checkout when the working repository contains historical CRLF files
- README, environment template, Compose passthrough, this compatibility matrix, and rollback guidance remain in sync
