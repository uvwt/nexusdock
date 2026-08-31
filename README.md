# NexusDock

**NexusDock 是 AgentDock 的自托管中心服务。** 你可以把多台 AgentDock 设备接入一个 Web 控制台，统一管理节点、Recall、Workflow 和 MCP，并让支持 MCP 的客户端通过一个入口访问整套设备能力。

它适合已经在 Mac、Windows 或服务器上使用 AgentDock，希望获得统一管理入口、跨设备长期记忆和中心化 MCP 接入的个人用户或可信小型环境。

- AgentDock：<https://github.com/uvwt/agentdock>
- Docker Hub：<https://hub.docker.com/r/agentdockio/nexusdock>
- GitHub：<https://github.com/uvwt/nexusdock>
- Releases：<https://github.com/uvwt/nexusdock/releases>
- 当前稳定版本：`v0.2.0`

## 你可以用它做什么

- **统一管理 AgentDock 节点**：查看设备在线状态、版本和能力，并进行配对、重命名、停用或移除。
- **查看节点运行时**：在 Runtime 中先选择一台 AgentDock，再查看该节点的任务、Skill 和动态 MCP；这些运行时状态仍保留在节点本机，不会被复制成 NexusDock 的另一套状态。
- **集中使用 Recall**：浏览和搜索长期记忆，管理经验卡片、Evolution、向量召回和本地 Git 版本历史。
- **管理 Workflow 模板**：集中维护可复用任务模板、执行步骤和匹配条件，并在配置 Embeddings 后使用语义匹配。
- **统一 MCP 入口**：客户端只连接 NexusDock `/mcp`；Nexus 中心工具只出现一次，设备工具通过 `node_id` 路由到具体 AgentDock。
- **回传节点 Artifact**：节点发布文件后，NexusDock 可以生成临时签名 URL，并通过节点已有的出站连接代理下载，不要求节点开放公网端口。
- **统一管理接入与 AI 设置**：Web 中可以管理管理员会话、MCP Access Token、Embedding、Stage 3 模型以及 AgentDock 节点。

NexusDock **不会替代 AgentDock**。真正的命令执行、文件操作、浏览器、Skill、动态 MCP 和设备能力仍运行在各个 AgentDock 节点上；NexusDock 负责中心管理、共享 Recall / Workflow、路由和访问入口。

## 快速开始

普通用户不需要从源码构建，直接使用官方 Docker 镜像即可。镜像同时发布到：

```text
agentdockio/nexusdock
ghcr.io/uvwt/nexusdock
```

生产环境建议固定具体版本，例如 `0.2.0`。

### 1. 创建目录和配置

```bash
mkdir nexusdock
cd nexusdock
mkdir -p nexus-data recall
```

生成一个随机的程序化 API Token：

```bash
openssl rand -hex 32
```

新建 `.env`。下面这组值适合**本机 HTTP 首次试用**：

```dotenv
NEXUS_AUTH_TOKEN=<粘贴刚才生成的随机值>
NEXUS_PUBLIC_URL=
NEXUS_AUTH_ALLOW_INSECURE_HTTP=true
NEXUS_TRUSTED_PROXIES=127.0.0.1,::1
```

> `NEXUS_AUTH_ALLOW_INSECURE_HTTP=true` 只用于本机 `http://127.0.0.1` 试用。通过域名远程访问时应使用 HTTPS，并改回 `false`。

Linux 使用宿主机绑定目录时，首次启动前建议：

```bash
sudo chown -R 10001:10001 nexus-data recall
```

### 2. 创建 Compose 文件

新建 `compose.yaml`：

```yaml
services:
  nexusdock:
    image: agentdockio/nexusdock:0.2.0
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
      NEXUS_TRUSTED_PROXIES: "${NEXUS_TRUSTED_PROXIES:-127.0.0.1,::1}"
      NEXUS_LOG_LEVEL: ${NEXUS_LOG_LEVEL:-info}
```

如果更喜欢 GHCR，只需要把镜像改成：

```yaml
image: ghcr.io/uvwt/nexusdock:0.2.0
```

### 3. 创建管理员

```bash
docker compose run --rm nexusdock admin init owner
```

终端会要求输入并确认管理员密码。密码至少需要 12 个字符，不能与用户名相同，也不能使用常见弱密码；密码不会以明文写入 `.env`。

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

## 远程访问与 HTTPS

远程使用时，建议继续让 Docker 只监听 `127.0.0.1:18777`，再通过 Caddy、Nginx、Traefik、Cloudflare Tunnel 等提供 HTTPS。

例如把 `.env` 调整为：

```dotenv
NEXUS_PUBLIC_URL=https://nexus.example.com
NEXUS_AUTH_ALLOW_INSECURE_HTTP=false
NEXUS_TRUSTED_PROXIES=127.0.0.1,::1
```

需要注意：

- `NEXUS_PUBLIC_URL` 必须是完整的 HTTPS Origin，例如 `https://nexus.example.com`，不能包含路径、查询参数、Fragment 或用户信息。
- 远程部署建议设置准确的 `NEXUS_PUBLIC_URL`；节点 Artifact 临时下载链接以及 MCP Recall 引用链接依赖它生成可访问 URL。
- `NEXUS_TRUSTED_PROXIES` 只应包含实际反向代理地址或必要网段。若反代通过 Docker 网络访问 NexusDock，需要把对应代理地址加入这里；不要为了省事信任整个不相关私网。
- NexusDock 只会信任受信任代理提供的 `X-Forwarded-*`。反代配置错误时，浏览器登录可能返回 `HTTPS_REQUIRED` 或 `ORIGIN_REJECTED`。

## Web 控制台

登录后主要分为三类页面：

- **Workspace**：总览、Recall、Workflow。总览展示节点与系统状态；Recall 管理资料库、经验卡片、Evolution、向量召回和版本历史；Workflow 管理可复用模板与匹配规则。
- **Runtime**：任务、Skill、MCP。顶部需要先选择具体 AgentDock 节点，再读取该节点的实时状态；节点离线时无法读取其实时运行时信息。
- **Settings**：账号与会话、MCP 接入、AI 与向量、系统与节点。这里可以管理登录会话、MCP 固定 Token、模型与 Embedding，以及 AgentDock 配对和节点状态。

动态 MCP 页面会直接转发 AgentDock 的管理能力，可添加 HTTP / stdio MCP、启停、刷新工具、移除服务并管理隔离环境变量；敏感环境值不会从节点回显。

## 连接 AgentDock

进入 **设置 → 系统与节点**，点击 **配对设备**。NexusDock 会生成一个 10 分钟内有效且只能使用一次的配对码，并根据当前浏览器地址自动生成类似命令：

```bash
agentdock nexus pair --endpoint https://nexus.example.com --code pair_xxx
```

在目标 AgentDock 设备执行命令并按提示重启 AgentDock 后，节点会主动建立到 NexusDock 的连接。

这种模式的好处是：

- 只需要 NexusDock 有可访问的 HTTPS 地址；
- AgentDock 可以位于 NAT、家庭网络或没有入站公网的设备中；
- NexusDock 不需要保存 AgentDock 的公网地址或 AgentDock `/mcp` Token；
- 每台设备拥有独立设备身份，可以单独停用或移除。

AgentDock 仍然可以独立使用；接入 NexusDock 不会改变原有的本地 MCP、认证和工具行为。

## 连接 MCP 客户端

统一 MCP 入口是：

```text
https://你的-nexus-域名/mcp
```

支持 OAuth 的 MCP 客户端可以直接连接并通过浏览器授权。对于不支持 OAuth、需要固定 Token 的客户端，可以在 **设置 → MCP 接入** 中查看专用 MCP Access Token，然后使用：

```text
Authorization: Bearer <Access Token>
```

三类凭据用途不同：

| 凭据 | 用途 |
| --- | --- |
| 管理员用户名和密码 | 登录 Web 控制台 |
| `NEXUS_AUTH_TOKEN` | 程序化访问受保护的 `/v1` 管理 API |
| MCP Access Token | 仅访问 `/mcp` |

重置 MCP Access Token 后，旧 Token 会立即失效；已通过 OAuth 授权的客户端不受影响。

通过统一 MCP 入口调用设备工具时，设备工具使用 `node_id` 指定目标 AgentDock。节点的 Task、Skill 和动态 MCP 仍属于节点运行时，NexusDock 不会复制这些状态。

## Recall 与 Workflow

Recall 是 NexusDock 的长期记忆工作区，默认位于 `RECALL_REPO_DIR`。你可以在 Web 控制台中：

- 新建、编辑、移动和删除 `.md`、`.markdown`、`.txt` 文件；
- 按关键词搜索长期记忆；
- 查看本地修改和 Git 版本历史；
- 管理经验卡片和 Evolution 信息；
- 配置 Embeddings 后使用语义召回。

Recall 本身仍然是普通 Git 仓库。NexusDock 负责本地文件与版本能力，**不会自动配置、读取或操作 Git remote**；远端备份策略由你自己决定。

Workflow 模板同样保存在 NexusDock 中，可维护步骤、完成条件和匹配规则。没有 Embeddings 时仍可使用关键词等规则匹配；配置 Embeddings 后可增加语义匹配能力。

## AI 与向量

`v0.2.0` 推荐直接在 **设置 → AI 与向量** 中配置：

- **Embedding**：兼容 OpenAI Embeddings API，供 Recall 语义召回与 Workflow 模板匹配共用；
- **Stage 3 模型**：可选的外部 Chat Completions 模型，用于低频语义辅助；
- **测试连接**：保存前后都可以验证服务是否可达；
- **重建索引**：重新生成 Recall 与 Workflow 的向量数据。

Web 保存的配置会持久化并立即应用，不需要重启容器。API Key 不会在读取设置时以明文返回。

环境变量仍可作为首次启动默认值或高级部署配置，例如：

```dotenv
RECALL_EMBEDDING_ENABLED=true
RECALL_EMBEDDING_ENDPOINT=http://embedding-service:8000/v1/embeddings
RECALL_EMBEDDING_MODEL=BAAI/bge-m3
RECALL_EMBEDDING_TIMEOUT_SECONDS=30
```

Stage 3 对应的启动配置使用 `NEXUS_MODEL_*` 与 `NEXUS_EVOLUTION_*` 环境变量。完整运行时配置以当前源码和 Web 设置页为准。

不配置 Embeddings 时，Recall 的文件浏览、关键词搜索、本地 Git 历史以及 Workflow 模板都可以正常使用。

## 节点 Artifact 下载

配置 `NEXUS_PUBLIC_URL` 后，NexusDock 可以为 AgentDock 发布的 Artifact 生成临时签名 URL。下载时：

- 文件内容通过节点已有的出站连接实时代理，NexusDock 不持久化节点文件；
- 目标 AgentDock 节点必须在线；
- URL 到期后返回 `410 Gone`；
- 单个代理文件上限为 512 MiB，每个节点最多同时代理 2 个下载。

因此备份 NexusDock 数据并不等于备份各节点发布过的 Artifact 原文件。

## 镜像版本

NexusDock 同步发布 Docker Hub 与 GHCR 多架构镜像，支持 `linux/amd64` 和 `linux/arm64`。

| 标签 | 用途 |
| --- | --- |
| `0.2.0` | 固定到当前具体版本，推荐生产使用 |
| `0.2` | 跟随 `0.2.x` 系列更新 |
| `latest` | 当前最新稳定版本 |
| `sha-<commit>` | 固定到某个 Git commit，适合精确回滚与排障 |

例如：

```bash
docker pull agentdockio/nexusdock:0.2.0
docker pull ghcr.io/uvwt/nexusdock:0.2.0
```

## 升级

如果 Compose 使用固定版本，先把 `image:` 改成要升级的版本，然后执行：

```bash
docker compose pull
docker compose up -d
curl http://127.0.0.1:18777/health
```

升级前建议完整备份 `nexus-data` 和 `recall`。需要回滚时，把 `image:` 改回上一个已验证版本，再重新 `pull` / `up -d`。

不要运行两个 NexusDock 实例同时写同一份 `nexus-data`。

## 数据与备份

默认需要持久化两类数据：

```text
nexus-data/
  nexus.db
  secrets/
    mcp-access-token
    artifact-url-secret
    runtime-ai-settings.key

recall/
  .git/
  ...
```

- `nexus-data`：管理员账户、会话、设备、系统状态、MCP Access Token、Artifact 签名密钥，以及 Web 保存的 AI 设置与加密密钥。
- `recall`：长期记忆、本地 Git 历史、经验卡片和相关索引数据。

生产环境应**完整备份这两个目录**，不要只挑选 `nexus.db` 或某几个密钥文件。尤其不要遗漏 `runtime-ai-settings.key`，否则迁移后数据库中已经保存的 AI API Key 将无法解密。

可以使用宿主机快照、文件备份或自己的 Git 备份流程。Recall 的 Git remote 仍由你自行维护。

## 常用配置

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `NEXUS_HOST` | `127.0.0.1` | HTTP 服务监听地址 |
| `NEXUS_PORT` | `18777` | HTTP 服务端口 |
| `NEXUS_PUBLIC_URL` | 空 | 对外 HTTPS Origin，例如 `https://nexus.example.com` |
| `NEXUS_DATA_DIR` | `./nexus-data` | SQLite 与 NexusDock 密钥目录；容器内使用 `/var/lib/nexus` |
| `NEXUS_AUTH_TOKEN` | 空 | 程序化 `/v1` API Bearer Token |
| `NEXUS_REQUIRE_AUTH` | `false` | 开启后，如果没有配置 `NEXUS_AUTH_TOKEN` 则拒绝启动 |
| `NEXUS_AUTH_ALLOW_INSECURE_HTTP` | `false` | 允许通过 HTTP 提交浏览器登录；仅建议本机调试使用 |
| `NEXUS_TRUSTED_PROXIES` | `127.0.0.1,::1` | 允许提供可信 `X-Forwarded-*` 的反向代理地址 |
| `NEXUS_LOG_LEVEL` | `info` | `debug`、`info`、`warn` 或 `error` |
| `RECALL_REPO_DIR` | `./recall` | Recall 仓库目录；容器内使用 `/recall` |

仓库中的基础示例见 [`.env.example`](./.env.example)。Embedding 与 Stage 3 也支持启动环境变量，但普通用户优先使用 Web 的 **AI 与向量** 页面配置。

## 安全部署

NexusDock 面向个人和可信环境。远程使用时建议：

- Docker 端口继续只绑定 `127.0.0.1`；
- 使用反向代理或 Tunnel 提供 HTTPS；
- 设置准确的 `NEXUS_PUBLIC_URL`；
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

新密码遵循与初始化相同的强度规则。恢复操作直接访问持久化数据库，不通过 Web 或远程 API；密码更新后，该管理员现有的活动 Web 会话会被全部撤销，需要重新登录。

## 常见问题

**本机打开页面后无法登录？**

确认本机 HTTP 试用时设置了 `NEXUS_AUTH_ALLOW_INSECURE_HTTP=true`。正式远程部署请改回 `false` 并使用 HTTPS。

**反向代理后登录返回 `HTTPS_REQUIRED` 或 `ORIGIN_REJECTED`？**

确认反向代理正确传递 `X-Forwarded-Proto` / `X-Forwarded-Host`，并确认反向代理实际来源地址已包含在 `NEXUS_TRUSTED_PROXIES` 中。不要通过扩大到无关私网的方式绕过检查。

**Linux 启动时报 `permission denied`？**

确认 `nexus-data` 和 `recall` 对 UID/GID `10001:10001` 可写。

**AgentDock 配对后没有上线？**

确认 NexusDock 的 HTTPS/WSS 地址可从目标设备访问，然后重启目标 AgentDock。设备不需要开放入站端口。

**Runtime 页面没有任务、Skill 或 MCP 数据？**

先确认顶部已选择目标 AgentDock，并确认该节点在线。Runtime 展示的是目标节点实时状态，不是 NexusDock 自己保存的副本。

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
