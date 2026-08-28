# NexusDock

**NexusDock 是 AgentDock 的自托管中心服务。** 你可以把多台 AgentDock 设备接入一个 Web 控制台，统一管理节点、长期记忆、工作流和 MCP，并让支持 MCP 的客户端通过一个入口访问整套设备能力。

适合已经在 Mac、Windows 或服务器上使用 AgentDock，希望获得统一管理入口、跨设备 Recall 和记忆能力的个人用户或可信小型环境。

- AgentDock：<https://github.com/uvwt/agentdock>
- Docker Hub：<https://hub.docker.com/r/agentdockio/nexusdock>
- GitHub：<https://github.com/uvwt/nexusdock>
- 当前稳定版本：`v0.1.0`

## 你可以用它做什么

- **统一管理 AgentDock 节点**：查看多台设备是否在线，以及各节点的任务、Skill、动态 MCP 和运行状态。
- **集中使用 Recall**：在 Web 中浏览、搜索、编辑 Markdown / 文本记忆，管理经验卡片、本地 Git 历史和向量召回。
- **统一 MCP 入口**：客户端只连接 NexusDock `/mcp`，中心工具只出现一次，设备工具通过 `node_id` 路由到具体 AgentDock。
- **回传节点 Artifact**：节点发布文件后由 NexusDock 返回临时签名 URL，并通过现有出站 WebSocket 分块代理下载，无需暴露节点端口。
- **管理工作流模板**：集中维护和匹配可复用的任务流程。
- **安全配对设备**：AgentDock 主动连接 NexusDock，不要求每台设备暴露公网入口。
- **桌面与手机访问**：Web 控制台针对桌面和移动端都做了适配。

NexusDock **不会替代 AgentDock**。真正的命令执行、文件操作、浏览器、Skill 和设备能力仍运行在各个 AgentDock 节点上；NexusDock 负责集中管理、记忆、路由和协调。

## 快速开始

普通用户不需要从源码构建，直接使用官方 Docker 镜像即可。

官方镜像同时发布到：

```text
agentdockio/nexusdock
ghcr.io/uvwt/nexusdock
```

推荐生产环境固定具体版本，例如 `0.1.0`。

### 1. 创建目录和配置

```bash
mkdir nexusdock
cd nexusdock
mkdir -p nexus-data recall
```

生成一个随机 API Token：

```bash
openssl rand -hex 32
```

新建 `.env`：

```dotenv
NEXUS_AUTH_TOKEN=<粘贴刚才生成的随机值>
NEXUS_PUBLIC_URL=
NEXUS_AUTH_ALLOW_INSECURE_HTTP=true
```

> `NEXUS_AUTH_ALLOW_INSECURE_HTTP=true` 只适合本机 `http://127.0.0.1` 首次试用。通过域名远程访问时应使用 HTTPS，并把它改成 `false`。

Linux 使用宿主机绑定目录时，首次启动前建议：

```bash
sudo chown -R 10001:10001 nexus-data recall
```

### 2. 创建 Compose 文件

新建 `compose.yaml`：

```yaml
services:
  nexusdock:
    image: agentdockio/nexusdock:0.1.0
    container_name: nexusdock
    restart: unless-stopped
    read_only: true
    cap_drop:
      - ALL
    security_opt:
      - no-new-privileges:true
    ports:
      - "127.0.0.1:18777:18777"
    tmpfs:
      - /tmp:rw,noexec,nosuid,size=64m,uid=10001,gid=10001,mode=0700
    volumes:
      - ./nexus-data:/var/lib/nexus
      - ./recall:/recall
    environment:
      NEXUS_AUTH_TOKEN: ${NEXUS_AUTH_TOKEN}
      NEXUS_REQUIRE_AUTH: "true"
      NEXUS_AUTH_ALLOW_INSECURE_HTTP: ${NEXUS_AUTH_ALLOW_INSECURE_HTTP:-false}
      NEXUS_PUBLIC_URL: ${NEXUS_PUBLIC_URL:-}
      NEXUS_DATA_DIR: /var/lib/nexus
      RECALL_REPO_DIR: /recall
      NEXUS_TRUSTED_PROXIES: "127.0.0.1,::1,172.16.0.0/12,192.168.0.0/16"
```

如果更喜欢 GHCR，只需要把镜像改成：

```yaml
image: ghcr.io/uvwt/nexusdock:0.1.0
```

### 3. 创建管理员

```bash
docker compose run --rm nexusdock admin init owner
```

终端会要求输入并确认管理员密码。密码保存在 NexusDock 数据库中，不需要写进 `.env`。

### 4. 启动 NexusDock

```bash
docker compose up -d
```

检查健康状态：

```bash
curl http://127.0.0.1:18777/health
```

然后打开：

```text
http://127.0.0.1:18777
```

使用刚才创建的管理员账号登录。

## 连接 AgentDock

在 NexusDock Web 控制台进入设置页，点击 **配对设备**。复制页面生成的命令，在目标 AgentDock 设备上执行，例如：

```bash
agentdock nexus pair --endpoint https://nexus.example.com --code pair_xxx
```

重启 AgentDock 后，节点会主动建立到 NexusDock 的 WSS 连接。

这种模式的好处是：

- 只需要 NexusDock 有可访问的 HTTPS 地址；
- AgentDock 可以位于 NAT、家庭网络或没有入站公网的设备中；
- NexusDock 不需要保存 AgentDock 的公网地址或 AgentDock `/mcp` Token；
- 每台设备拥有独立的设备身份。

AgentDock 仍然可以继续独立使用；接入 NexusDock 不会改变原有的本地 MCP、认证和工具行为。

## 连接 MCP 客户端

支持 OAuth 的 MCP 客户端可以直接连接：

```text
https://你的-nexus-域名/mcp
```

并通过浏览器完成授权。

对于不支持 OAuth、需要固定 Token 的客户端，可以在 NexusDock 的 **设置 → MCP 接入** 中查看专用 Access Token，然后使用：

```text
Authorization: Bearer <Access Token>
```

这个 Token 只允许访问 `/mcp`，不能用于管理 API。重置后旧 Token 会立即失效。

## Recall

Recall 是 NexusDock 的长期记忆工作区，默认位于 `RECALL_REPO_DIR`。

你可以在 Web 控制台中：

- 新建、编辑、移动和删除 `.md`、`.markdown`、`.txt` 文件；
- 按关键词搜索记忆；
- 查看本地修改和 Git 版本历史；
- 把稳定经验整理成经验卡片；
- 配置 Embeddings 后使用向量召回。

Recall 本身仍然是普通 Git 仓库。NexusDock 负责本地文件与版本能力，**不会自动配置、读取或操作 Git remote**；远端备份策略由你自己决定。

### 启用向量召回

NexusDock 支持 OpenAI 兼容的 `/v1/embeddings` 接口：

```dotenv
RECALL_EMBEDDING_ENABLED=true
RECALL_EMBEDDING_ENDPOINT=http://embedding-service:8000/v1/embeddings
RECALL_EMBEDDING_MODEL=BAAI/bge-m3
RECALL_EMBEDDING_TIMEOUT_SECONDS=30
```

不配置 Embeddings 时，文件浏览、关键词搜索和本地 Git 历史仍然可以正常使用。

## 镜像版本

NexusDock 同步发布 Docker Hub 与 GHCR 多架构镜像，支持 `linux/amd64` 和 `linux/arm64`。

| 标签 | 用途 |
| --- | --- |
| `0.1.0` | 固定到当前具体版本，推荐生产使用 |
| `0.1` | 跟随 `0.1.x` 系列更新 |
| `latest` | 当前最新稳定版本 |
| `sha-<commit>` | 固定到某个 Git commit，适合精确回滚与排障 |

例如：

```bash
docker pull agentdockio/nexusdock:0.1.0
docker pull ghcr.io/uvwt/nexusdock:0.1.0
```

## 升级

如果 Compose 使用固定版本，先把 `image:` 改成想升级的版本，然后执行：

```bash
docker compose pull
docker compose up -d
curl http://127.0.0.1:18777/health
```

升级前建议备份 `nexus-data` 和 `recall`。需要回滚时，把 `image:` 改回上一个已验证版本，再重新 `pull` / `up -d`。

不要运行两个 NexusDock 实例同时写同一份 `nexus-data`。

## 数据与备份

默认需要持久化两类数据：

```text
nexus-data/
  nexus.db
  secrets/
    mcp-access-token
    artifact-url-secret

recall/
  .git/
  ...
```

- `nexus-data`：账户、设备、系统状态和 NexusDock 自身密钥。
- `recall`：长期记忆与本地 Git 历史。

生产环境至少应同时备份这两个目录。可以使用宿主机快照、文件备份或自己的 Git 备份流程。

Artifact 下载 URL 依赖 `NEXUS_PUBLIC_URL`，且下载时目标 AgentDock 节点必须在线。URL 到期后会返回 `410 Gone`；NexusDock 不持久化节点文件内容。

## 常用配置

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `NEXUS_PORT` | `18777` | HTTP 服务端口 |
| `NEXUS_PUBLIC_URL` | 空 | 对外 HTTPS 地址，例如 `https://nexus.example.com` |
| `NEXUS_DATA_DIR` | `./nexus-data` | SQLite 与系统密钥目录；容器内使用 `/var/lib/nexus` |
| `NEXUS_AUTH_TOKEN` | 空 | 程序化 `/v1` API Bearer Token |
| `NEXUS_REQUIRE_AUTH` | `false` | 开启后，没有配置 API Token 时拒绝启动 |
| `NEXUS_AUTH_ALLOW_INSECURE_HTTP` | `false` | 允许通过 HTTP 提交浏览器登录；仅建议本机调试使用 |
| `NEXUS_TRUSTED_PROXIES` | `127.0.0.1,::1` | 允许提供 `X-Forwarded-*` 的反向代理地址 |
| `NEXUS_LOG_LEVEL` | `info` | `debug`、`info`、`warn` 或 `error` |
| `RECALL_REPO_DIR` | `./recall` | Recall 仓库目录；容器内使用 `/recall` |
| `RECALL_EMBEDDING_ENABLED` | `false` | 是否启用向量索引 |
| `RECALL_EMBEDDING_ENDPOINT` | 空 | OpenAI 兼容 Embeddings 地址 |
| `RECALL_EMBEDDING_MODEL` | `BAAI/bge-m3` | Embeddings 模型 |

仓库中的完整示例见 [`.env.example`](./.env.example)。

## 安全部署

NexusDock 面向个人和可信环境。远程使用时建议：

- Docker 端口继续只绑定 `127.0.0.1`；
- 使用 Caddy、Nginx、Traefik 或 Cloudflare Tunnel 提供 HTTPS；
- 设置正确的 `NEXUS_PUBLIC_URL`；
- 保持 `NEXUS_AUTH_ALLOW_INSECURE_HTTP=false`；
- 只把实际反向代理加入 `NEXUS_TRUSTED_PROXIES`；
- 使用高强度管理员密码和随机 `NEXUS_AUTH_TOKEN`；
- 限制 `nexus-data`、Recall 和其他凭据文件的宿主机权限。

官方镜像默认使用 UID/GID `10001:10001`，根文件系统只读，并丢弃全部 Linux capabilities。

## 管理员密码恢复

如果忘记管理员密码，在部署主机上执行：

```bash
docker compose run --rm nexusdock admin recover owner
```

恢复操作直接访问持久化数据库，不通过 Web 或远程 API 修改密码。

## 常见问题

**本机打开页面后无法登录？**

确认本机 HTTP 试用时设置了 `NEXUS_AUTH_ALLOW_INSECURE_HTTP=true`。正式远程部署请改回 `false` 并使用 HTTPS。

**Linux 启动时报 `permission denied`？**

确认 `nexus-data` 和 `recall` 对 UID/GID `10001:10001` 可写。

**AgentDock 配对后没有上线？**

确认 NexusDock 的 HTTPS/WSS 地址可从目标设备访问，然后重启目标 AgentDock。设备不需要开放入站端口。

**容器显示 unhealthy？**

先检查：

```bash
docker compose logs --tail=200 nexusdock
curl http://127.0.0.1:18777/health
```

## 从源码开发

这一部分只面向希望修改 NexusDock 本身的开发者。普通部署不需要安装 Go 或 Node.js。

```bash
git clone https://github.com/uvwt/nexusdock.git
cd nexusdock
make web-deps
make build
```

开发检查：

```bash
make check
make ci
```

仓库自带的 `docker-compose.yml` 默认从当前源码构建 `nexusdock:local`，用于本地开发和精确复现；普通用户建议使用上面的官方镜像部署方式。

更多开发约束见 [`AGENTS.md`](./AGENTS.md)。
