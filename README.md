<div align="center">

English | [简体中文](./README.zh-CN.md)

# NexusDock

**The self-hosted control center for your AgentDock fleet.**

Bring the AgentDock instances running on your Macs, Windows PCs, servers, and containers into one Web console and one MCP endpoint. Manage devices, shared Recall, reusable workflows, and live runtime state without exposing every machine directly to the internet.

[AgentDock](https://github.com/uvwt/agentdock) · [Releases](https://github.com/uvwt/nexusdock/releases) · [Docker Hub](https://hub.docker.com/r/agentdockio/nexusdock) · [GHCR](https://github.com/uvwt/nexusdock/pkgs/container/nexusdock)

[![CI](https://github.com/uvwt/nexusdock/actions/workflows/ci.yml/badge.svg)](https://github.com/uvwt/nexusdock/actions/workflows/ci.yml)
[![GitHub Release](https://img.shields.io/github/v/release/uvwt/nexusdock?display_name=tag&logo=github)](https://github.com/uvwt/nexusdock/releases)
[![Docker Hub](https://img.shields.io/docker/pulls/agentdockio/nexusdock?logo=docker&label=Docker%20Hub)](https://hub.docker.com/r/agentdockio/nexusdock)
[![GHCR](https://img.shields.io/badge/GHCR-ghcr.io%2Fuvwt%2Fnexusdock-2496ED?logo=docker&logoColor=white)](https://github.com/uvwt/nexusdock/pkgs/container/nexusdock)

</div>

## What is NexusDock?

NexusDock is a self-hosted control center for people who run AgentDock on more than one device.

AgentDock remains the execution layer on each machine: it owns files, commands, Git, Skills, dynamic MCP servers, browser automation, tasks, and other device-local capabilities. NexusDock adds the shared layer above those nodes:

- one Web console for the fleet;
- one MCP endpoint for AI clients;
- centralized Recall and workflow templates;
- live views of node runtime state;
- pairing, routing, authentication, and shared settings.

NexusDock does not replace AgentDock and does not copy every node's runtime state into a second control plane. Nodes keep their own execution state and connect outbound to NexusDock.

```text
              ChatGPT / Claude / Codex
                        │
                        │ MCP + OAuth / Access Token
                        ▼
                 ┌─────────────┐
                 │  NexusDock  │
                 │ Web + MCP   │
                 └──────┬──────┘
                        │
              outbound node connections
          ┌─────────────┼─────────────┐
          ▼             ▼             ▼
   ┌───────────┐ ┌───────────┐ ┌───────────┐
   │ AgentDock │ │ AgentDock │ │ AgentDock │
   │    Mac    │ │  Windows  │ │   Server  │
   └───────────┘ └───────────┘ └───────────┘

        Shared Recall · Workflows · Settings
```

## What can NexusDock do?

- View the online state, version, and capabilities of multiple AgentDock nodes
- Pair, rename, disable, and remove devices from one place
- Inspect node-local tasks, Skills, and dynamic MCP servers without duplicating their state
- Store and search shared Recall, experience cards, and private notes
- Publish, retire, match, and reuse workflow templates across devices
- Expose one MCP endpoint that combines NexusDock-owned shared tools with routed AgentDock capabilities
- Authenticate MCP clients with OAuth or a dedicated MCP Access Token
- Configure optional Embedding and model providers for semantic Recall and workflow matching
- Generate temporary download links for files published by online AgentDock nodes

## Quick start

The easiest way to run NexusDock is Docker Compose. Create `compose.yaml`:

```yaml
services:
  nexusdock:
    image: agentdockio/nexusdock:latest
    restart: unless-stopped
    ports:
      - "127.0.0.1:18777:18777"
    volumes:
      - nexus-data:/var/lib/nexus
      - recall:/recall
volumes:
  nexus-data:
  recall:
```

Create the administrator and start NexusDock:

```bash
docker compose run --rm nexusdock admin init admin
docker compose up -d
```

Open `http://127.0.0.1:18777` and sign in with the account you just created. For remote access, HTTPS, API authentication, and backup settings, see [Security notes](#security-notes) and [Data, ports, and backup](#data-ports-and-backup).

The same image is also available from `ghcr.io/uvwt/nexusdock:latest`. Official images support `linux/amd64` and `linux/arm64`.

## Pair AgentDock devices

Sign in to the Web console, open **Settings → System & nodes**, and choose **Pair device**.

NexusDock creates a short-lived, single-use pairing code and shows a command similar to:

```bash
agentdock nexus pair --endpoint https://nexus.example.com --code pair_xxx
```

Run it on the target device and follow the prompt to restart AgentDock. The node then connects outbound to NexusDock, so you do not need to expose the AgentDock node itself to the public internet.

Pairing does not change the node's existing local MCP endpoint or authentication. Each AgentDock instance can still be used independently.

## Connect an AI client

NexusDock exposes MCP Streamable HTTP at:

```text
https://your-nexus-domain/mcp
```

Clients with OAuth support can connect directly and complete authorization in the browser.

For clients that need a fixed token, open **Settings → MCP access** and use the dedicated MCP Access Token:

```text
Authorization: Bearer <MCP Access Token>
```

The common credentials are intentionally separate:

| Credential | Purpose |
| --- | --- |
| Administrator username and password | Sign in to the Web console; protected management APIs are accessed through the signed-in session |
| MCP Access Token | Access to `/mcp` for clients that do not use OAuth |

Resetting the MCP Access Token invalidates the previous fixed token immediately and does not affect OAuth clients.

## Core capabilities

### Fleet and runtime

- Central device list with connection state, version, and capabilities
- Short-lived pairing codes and outbound AgentDock connections
- Explicit node selection before viewing node-local runtime state
- Live task, Skill, and dynamic MCP views routed to the selected AgentDock
- Temporary Artifact downloads through the existing node connection while the source node is online

### Recall and workflows

- Shared Markdown Recall workspace
- Keyword search and optional semantic retrieval
- Experience cards and vector recall
- Private-note vault with age-encrypted backup copies
- Central workflow template registry with versioned publish, retire, list, get, and match operations

NexusDock does not automatically configure or push a Git remote for Recall. Remote backup remains under the deployment owner's control.

### MCP gateway

- One MCP endpoint for NexusDock-owned shared tools and enabled AgentDock nodes
- OAuth authorization for compatible MCP clients
- Dedicated fixed MCP Access Token for clients without OAuth
- Node-aware routing without requiring inbound public AgentDock ports
- Shared protocol contracts with AgentDock through [`uvwt/agentdock-protocol`](https://github.com/uvwt/agentdock-protocol)

### AI and vector settings

Embedding and external model integrations are optional. Configure them from **Settings → AI & vectors** when you need semantic Recall, workflow vector matching, or related AI-assisted features.

Without an Embedding or model provider, device management, MCP routing, Recall file browsing, keyword search, and basic workflow operations continue to work.

## Data, ports, and backup

| Path | Purpose |
| --- | --- |
| `/var/lib/nexus` | NexusDock control-plane state, accounts, devices, sessions, settings, secrets, and workflow data |
| `/recall` | Shared Recall content and private-note data |

Default HTTP port:

```text
18777
```

Before upgrading, back up both persistent stores. The quick-start Compose file uses the Docker volumes `nexus-data` and `recall`; if you switch to bind mounts, back up both host directories in full.

```text
nexus-data/
recall/
```

Upgrade with:

```bash
docker compose pull
docker compose up -d
curl http://127.0.0.1:18777/health
```

Do not run two NexusDock instances that write to the same Nexus data store.

## Optional: repository Compose template

The Quick Start above is the default recommended path. You do not need to use the repository Compose file. If you clone the repository and want to manage additional site settings through `.env`, the repository Compose template reads these values:

| Variable | Example default | Description |
| --- | --- | --- |
| `NEXUS_DATA_DIR` | `./nexus-data` | Host directory mounted to `/var/lib/nexus` |
| `RECALL_REPO_DIR` | `./recall` | Host directory mounted to `/recall` |
| `NEXUS_PUBLIC_URL` | empty | Public HTTPS origin, for example `https://nexus.example.com` |
| `NEXUS_TRUSTED_PROXIES` | automatic | Trusts loopback by default; Linux containers also add the current container's single RFC1918 bridge gateway. An explicit value fully overrides the automatic set |
| `NEXUS_HTTP_BIND` | `127.0.0.1` | Host listen address; the container port is fixed at `18777` |
| `NEXUS_HTTP_PORT` | `18777` | Host port; the container port is fixed at `18777` |
| `NEXUS_IMAGE` | empty | Image tag pinned for production, for example `ghcr.io/uvwt/nexusdock:sha-<short SHA>`; falls back to `nexusdock:local` |

`NEXUS_DATA_DIR` and `RECALL_REPO_DIR` above are host bind-mount sources. Inside the official image, NexusDock always uses `/var/lib/nexus` and `/recall`.

See [`.env.example`](./.env.example) for the repository Compose values. Existing custom Compose deployments can keep using their own configuration; no migration to the repository template is required.

### Direct binary and advanced deployment

When running the NexusDock binary directly, these additional environment variables control the process itself:

| Variable | Default | Description |
| --- | --- | --- |
| `NEXUS_HOST` | `127.0.0.1` | HTTP listen address |
| `NEXUS_PORT` | `18777` | HTTP listen port |
| `NEXUS_LOG_LEVEL` | `info` | `debug`, `info`, `warn`, or `error` |

The four Compose variables above are also valid process environment variables when running the binary directly. In that case, `NEXUS_DATA_DIR` and `RECALL_REPO_DIR` are application data paths rather than Docker mount sources.

## Security notes

For a remote deployment, administrator login requires HTTPS. Direct `localhost` / loopback access is the only HTTP exception.

When host Nginx/Caddy proxies to the official Docker loopback port, you do not need to configure a Docker subnet manually. NexusDock automatically recognizes the current container's single host bridge gateway. The proxy must still forward the external scheme, for example with Nginx:

```nginx
location / {
    proxy_pass http://127.0.0.1:18777;
    proxy_set_header Host $host;
    proxy_set_header X-Forwarded-Host $host;
    proxy_set_header X-Forwarded-Proto $scheme;
    proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    proxy_set_header X-Real-IP $remote_addr;
}
```

Set `NEXUS_TRUSTED_PROXIES` explicitly only for more complex proxy chains; an explicit value disables automatic container-gateway trust. If you change `NEXUS_HTTP_BIND` to `0.0.0.0`, configure exact trusted proxies and restrict ingress with a firewall instead of relying on automatic gateway trust.

- keep the container port bound to loopback and publish the service through HTTPS;
- set `NEXUS_PUBLIC_URL` to the real HTTPS origin;
- trust only the reverse proxies that actually sit in front of NexusDock;
- use a strong administrator password and access protected management APIs through the signed-in session;
- restrict host access to `nexus-data` and `recall`;
- use the MCP Access Token only for `/mcp`, never for management APIs.

The official image runs as UID/GID `10001:10001` and is designed to work with a read-only root filesystem, dropped Linux capabilities, and `no-new-privileges`.

If you forget the administrator password:

```bash
docker compose run --rm nexusdock admin recover
```

## Development and contribution

Clone the repository and build both the Web UI and Go service:

```bash
git clone https://github.com/uvwt/nexusdock.git
cd nexusdock
make web-deps
make build
```

Run the normal checks before submitting code:

```bash
make check
```

Run the full CI-equivalent verification when preparing a complete change:

```bash
make ci
```

Changes to shared AgentDock/NexusDock contracts may also require coordinated updates to [`uvwt/agentdock-protocol`](https://github.com/uvwt/agentdock-protocol) and [`uvwt/agentdock`](https://github.com/uvwt/agentdock).

Submit bugs and feature requests through [GitHub Issues](https://github.com/uvwt/nexusdock/issues).

## Related links

- [AgentDock](https://github.com/uvwt/agentdock)
- [NexusDock Releases](https://github.com/uvwt/nexusdock/releases)
- [Docker Hub](https://hub.docker.com/r/agentdockio/nexusdock)
- [GitHub Container Registry](https://github.com/uvwt/nexusdock/pkgs/container/nexusdock)
- [agentdock-protocol](https://github.com/uvwt/agentdock-protocol)
