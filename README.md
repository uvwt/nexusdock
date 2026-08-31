# NexusDock

**NexusDock 是 AgentDock 的自托管中心服务。**

如果你在多台 Mac、Windows 或服务器上使用 AgentDock，可以用 NexusDock 提供一个统一的 Web 控制台和 MCP 入口，集中管理设备、Recall、Workflow 与常用运行时能力。

- AgentDock：<https://github.com/uvwt/agentdock>
- Docker Hub：<https://hub.docker.com/r/agentdockio/nexusdock>
- GitHub Container Registry：<https://github.com/uvwt/nexusdock/pkgs/container/nexusdock>
- Releases：<https://github.com/uvwt/nexusdock/releases>

## 能做什么

- **管理多台 AgentDock**：查看在线状态、版本和能力，完成设备配对、重命名、停用或移除。
- **查看设备运行时**：选择具体节点后查看它的任务、Skill 和动态 MCP；这些状态仍保留在 AgentDock 本机。
- **集中使用 Recall 与 Workflow**：管理长期记忆、经验卡片、版本历史和可复用工作流模板。
- **提供统一 MCP 入口**：支持 OAuth，也可以为不支持 OAuth 的客户端使用独立 MCP Access Token。
- **集中配置 AI 与向量能力**：在 Web 中配置 Embedding 和可选模型，并用于 Recall 与 Workflow 的语义能力。

NexusDock 不替代 AgentDock。命令执行、文件操作、浏览器、Skill、动态 MCP 等设备能力仍由对应的 AgentDock 节点执行；NexusDock 负责中心管理、共享数据和路由。

## 快速开始

普通部署直接使用官方 Docker 镜像即可，不需要安装 Go 或 Node.js。

官方镜像发布到：

```text
agentdockio/nexusdock
ghcr.io/uvwt/nexusdock
```

镜像支持 `linux/amd64` 和 `linux/arm64`。下面示例使用 `latest`；如果希望固定版本，将它替换为 [Releases](https://github.com/uvwt/nexusdock/releases) 中对应的发布版本。

### 1. 创建目录

```bash
mkdir nexusdock
cd nexusdock
mkdir -p nexus-data recall
```

Linux 使用宿主机目录时，首次启动前建议让容器用户拥有写权限：

```bash
sudo chown -R 10001:10001 nexus-data recall
```

### 2. 创建配置

生成一个随机的程序化 API Token：

```bash
openssl rand -hex 32
```

新建 `.env`：

```dotenv
NEXUS_AUTH_TOKEN=<粘贴刚才生成的随机值>
NEXUS_PUBLIC_URL=
NEXUS_AUTH_ALLOW_INSECURE_HTTP=true
NEXUS_TRUSTED_PROXIES=127.0.0.1,::1
```

这里的 `NEXUS_AUTH_ALLOW_INSECURE_HTTP=true` 只用于本机 `http://127.0.0.1` 首次试用。通过域名访问时应使用 HTTPS，并改回 `false`。

### 3. 创建 Compose 文件

新建 `compose.yaml`：

```yaml
services:
  nexusdock:
    image: agentdockio/nexusdock:latest
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

如果使用 GHCR，将镜像改为：

```yaml
image: ghcr.io/uvwt/nexusdock:latest
```

### 4. 创建管理员

```bash
docker compose run --rm nexusdock admin init myadmin
```

`myadmin` 只是示例用户名，可以换成你自己的用户名。终端会要求输入并确认管理员密码。密码至少需要 12 个字符，不能与用户名相同，也不能使用常见弱密码。管理员账号不需要写入 `.env`。

### 5. 启动

```bash
docker compose up -d
curl http://127.0.0.1:18777/health
```

然后打开：

```text
http://127.0.0.1:18777
```

使用刚才创建的管理员账号登录。

## 远程访问

远程使用时，建议继续让 Docker 只监听 `127.0.0.1:18777`，再通过 Caddy、Nginx、Traefik、Cloudflare Tunnel 等提供 HTTPS。

例如：

```dotenv
NEXUS_PUBLIC_URL=https://nexus.example.com
NEXUS_AUTH_ALLOW_INSECURE_HTTP=false
NEXUS_TRUSTED_PROXIES=127.0.0.1,::1
```

需要注意：

- `NEXUS_PUBLIC_URL` 必须是完整的 HTTPS Origin，例如 `https://nexus.example.com`，不能带路径、查询参数或 Fragment。
- `NEXUS_TRUSTED_PROXIES` 只填写实际反向代理地址或必要网段。反代通过 Docker 网络访问 NexusDock 时，需要把对应代理来源加入这里。
- NexusDock 只信任受信任代理提供的 `X-Forwarded-*`。配置错误时，浏览器登录会提示需要 HTTPS 或拒绝请求来源（`HTTPS_REQUIRED` / `ORIGIN_REJECTED`）。

## 连接 AgentDock

登录 Web 控制台后进入 **设置 → 系统与节点**，点击 **配对设备**。

NexusDock 会生成一个短时、单次使用的配对码，并根据当前浏览器地址给出类似命令：

```bash
agentdock nexus pair --endpoint https://nexus.example.com --code pair_xxx
```

在目标设备执行命令并按提示重启 AgentDock。之后 AgentDock 会主动连接 NexusDock，不需要为节点开放入站公网端口。

接入 NexusDock 不会改变 AgentDock 原有的本地 MCP、认证和工具行为；每台设备仍然可以独立使用。

## Web 控制台

登录后主要有三类区域：

- **Workspace**：总览、Recall、Workflow。用于查看整体状态、管理长期记忆和工作流模板。
- **Runtime**：任务、Skill、MCP。先选择具体 AgentDock 节点，再查看该节点的实时运行状态。
- **Settings**：账号与会话、MCP 接入、AI 与向量、系统与节点。

Runtime 展示的是目标 AgentDock 的实时状态，不是 NexusDock 复制出来的另一套任务或 Skill 数据。

## 连接 MCP 客户端

统一 MCP 地址是：

```text
https://你的-nexus-域名/mcp
```

支持 OAuth 的 MCP 客户端可以直接连接并通过浏览器完成授权。

对于不支持 OAuth、需要固定 Token 的客户端，可以在 **设置 → MCP 接入** 中查看专用 MCP Access Token：

```text
Authorization: Bearer <Access Token>
```

NexusDock 中常见的三类凭据用途不同：

| 凭据 | 用途 |
| --- | --- |
| 管理员用户名和密码 | 登录 Web 控制台 |
| `NEXUS_AUTH_TOKEN` | 程序化访问受保护的 `/v1` 管理 API |
| MCP Access Token | 访问 `/mcp` |

重置 MCP Access Token 后旧 Token 会立即失效；OAuth 客户端不受影响。

## Recall、Workflow 与 AI

Recall 是 NexusDock 的长期记忆工作区，可以在 Web 中浏览、搜索和编辑内容，也可以查看本地 Git 版本历史。NexusDock 不会自动配置或操作 Recall 仓库的 Git remote，远端备份方式由你自己决定。

Workflow 用于集中保存和匹配可复用任务模板。即使没有配置 Embedding，也可以正常使用基本模板能力。

需要语义召回或语义匹配时，进入 **设置 → AI 与向量** 配置兼容的 Embedding 服务；如有需要，也可以配置可选的外部模型。Web 中可以测试连接和重建索引，保存后的配置会直接应用，无需重启容器。

不配置 AI 或 Embedding 时，NexusDock 的节点管理、MCP、Recall 文件浏览、关键词搜索、版本历史和基础 Workflow 仍然可以使用。

## 节点文件下载

AgentDock 工具产生可发布文件时，NexusDock 可以通过节点现有连接提供临时下载地址，不要求节点额外开放公网文件服务。

要生成可从外部访问的下载地址，需要正确设置 `NEXUS_PUBLIC_URL`；下载期间对应 AgentDock 节点需要在线。NexusDock 不会把这些节点文件长期保存为自己的文件副本。

## 升级与备份

升级前建议完整备份：

```text
nexus-data/
recall/
```

不要只备份 SQLite 数据库或单个密钥文件。`nexus-data` 中还包含账号、设备、会话和 NexusDock 自身需要的密钥；`recall` 中包含长期记忆和本地版本历史。

升级：

```bash
docker compose pull
docker compose up -d
curl http://127.0.0.1:18777/health
```

如果使用固定版本，先把 `image:` 调整到目标发布版本。需要回滚时，改回之前验证过的版本并重新执行 `pull` / `up -d`。

不要运行两个 NexusDock 实例同时写同一份 `nexus-data`。

## 常用配置

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `NEXUS_HOST` | `127.0.0.1` | HTTP 服务监听地址 |
| `NEXUS_PORT` | `18777` | HTTP 服务端口 |
| `NEXUS_PUBLIC_URL` | 空 | 对外 HTTPS Origin，例如 `https://nexus.example.com` |
| `NEXUS_DATA_DIR` | `./nexus-data` | NexusDock 状态与密钥目录；容器内使用 `/var/lib/nexus` |
| `NEXUS_AUTH_TOKEN` | 空 | 程序化 `/v1` API Bearer Token |
| `NEXUS_REQUIRE_AUTH` | `false` | 开启后，没有 `NEXUS_AUTH_TOKEN` 时拒绝启动 |
| `NEXUS_AUTH_ALLOW_INSECURE_HTTP` | `false` | 是否允许通过 HTTP 提交浏览器登录；仅建议本机调试 |
| `NEXUS_TRUSTED_PROXIES` | `127.0.0.1,::1` | 允许提供可信 `X-Forwarded-*` 的代理地址 |
| `NEXUS_LOG_LEVEL` | `info` | `debug`、`info`、`warn` 或 `error` |
| `RECALL_REPO_DIR` | `./recall` | Recall 仓库目录；容器内使用 `/recall` |

基础环境变量示例见 [`.env.example`](./.env.example)。AI 与向量能力更适合直接通过 Web 控制台配置。

## 安全部署

远程部署建议：

- Docker 端口只绑定 `127.0.0.1`；
- 使用 HTTPS 反向代理或 Tunnel；
- 正确设置 `NEXUS_PUBLIC_URL`；
- 保持 `NEXUS_AUTH_ALLOW_INSECURE_HTTP=false`；
- 只信任实际反向代理；
- 使用高强度管理员密码和随机 `NEXUS_AUTH_TOKEN`；
- 限制 `nexus-data` 和 `recall` 的宿主机访问权限。

官方镜像默认使用 UID/GID `10001:10001`，并采用只读根文件系统、丢弃 Linux capabilities 等容器安全设置。

## 管理员密码恢复

忘记管理员密码时，在部署主机执行：

```bash
docker compose run --rm nexusdock admin recover
```

默认会恢复已配置的管理员；如果需要，也可以在命令末尾显式指定用户名。新密码遵循与初始化相同的强度规则。恢复后该管理员现有的 Web 会话会失效，需要重新登录。

## 常见问题

**本机打开页面后无法登录？**

确认本机 HTTP 试用时设置了 `NEXUS_AUTH_ALLOW_INSECURE_HTTP=true`。正式远程部署应使用 HTTPS 并保持它为 `false`。

**反向代理后登录提示“仅允许 HTTPS”或拒绝请求来源（`HTTPS_REQUIRED` / `ORIGIN_REJECTED`）？**

确认反向代理正确传递 `X-Forwarded-Proto` / `X-Forwarded-Host`，并确认代理实际来源已包含在 `NEXUS_TRUSTED_PROXIES` 中。

**Linux 启动时报 `permission denied`？**

确认 `nexus-data` 和 `recall` 对 UID/GID `10001:10001` 可写。

**AgentDock 配对后没有上线？**

确认 NexusDock 的 HTTPS/WSS 地址可从目标设备访问，然后按配对提示重启目标 AgentDock。设备不需要开放入站端口。

**Runtime 页面没有任务、Skill 或 MCP 数据？**

先确认顶部已选择目标 AgentDock，并确认该节点在线。

**容器显示 unhealthy？**

```bash
docker compose logs --tail=200 nexusdock
curl http://127.0.0.1:18777/health
```

## 从源码开发

这一部分只面向希望修改 NexusDock 本身的开发者。普通部署不需要执行这些步骤。

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

仓库自带的 `docker-compose.yml` 默认从当前源码构建本地镜像，适合开发和测试。更多开发约束见 [`AGENTS.md`](./AGENTS.md)。
