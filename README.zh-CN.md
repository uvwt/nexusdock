<div align="center">

[English](./README.md) | 简体中文

# NexusDock

**面向多台 AgentDock 的自托管中心端。**

把运行在 Mac、Windows、服务器与容器中的 AgentDock 汇聚到一个 Web 控制台和一个 MCP 入口：统一管理设备、共享 Recall、可复用 Workflow 与实时 Runtime 状态，而不需要把每台设备直接暴露到公网。

[AgentDock](https://github.com/uvwt/agentdock) · [Releases](https://github.com/uvwt/nexusdock/releases) · [Docker Hub](https://hub.docker.com/r/agentdockio/nexusdock) · [GHCR](https://github.com/uvwt/nexusdock/pkgs/container/nexusdock)

[![CI](https://github.com/uvwt/nexusdock/actions/workflows/ci.yml/badge.svg)](https://github.com/uvwt/nexusdock/actions/workflows/ci.yml)
[![GitHub Release](https://img.shields.io/github/v/release/uvwt/nexusdock?display_name=tag&logo=github)](https://github.com/uvwt/nexusdock/releases)
[![Docker Hub](https://img.shields.io/docker/pulls/agentdockio/nexusdock?logo=docker&label=Docker%20Hub)](https://hub.docker.com/r/agentdockio/nexusdock)
[![GHCR](https://img.shields.io/badge/GHCR-ghcr.io%2Fuvwt%2Fnexusdock-2496ED?logo=docker&logoColor=white)](https://github.com/uvwt/nexusdock/pkgs/container/nexusdock)

</div>

## NexusDock 是什么

NexusDock 是面向多设备 AgentDock 的自托管中心端。

AgentDock 仍然是每台设备上的执行层，负责文件、命令、Git、Skill、动态 MCP、浏览器自动化、任务等本机能力；NexusDock 在这些节点之上提供共享层：

- 一个统一的多设备 Web 控制台；
- 一个统一的 AI 客户端 MCP 入口；
- 集中的 Recall 与 Workflow 模板；
- 节点 Runtime 实时视图；
- 配对、路由、认证与共享设置。

NexusDock 不替代 AgentDock，也不会把每个节点的 Runtime 状态复制成另一套控制面。执行状态仍保留在各自 AgentDock 节点中，节点主动向 NexusDock 建立出站连接。

```text
              ChatGPT / Claude / Codex
                        │
                        │ MCP + OAuth / Access Token
                        ▼
                 ┌─────────────┐
                 │  NexusDock  │
                 │  Web + MCP  │
                 └──────┬──────┘
                        │
                    节点主动连接
          ┌─────────────┼─────────────┐
          ▼             ▼             ▼
   ┌───────────┐ ┌───────────┐ ┌───────────┐
   │ AgentDock │ │ AgentDock │ │ AgentDock │
   │    Mac    │ │  Windows  │ │  Server   │
   └───────────┘ └───────────┘ └───────────┘

             共享 Recall · Workflow · 设置
```

## 你可以用 NexusDock 做什么

- 查看多台 AgentDock 的在线状态、版本与能力
- 在一个地方完成设备配对、重命名、停用和移除
- 查看节点本机的任务、Skill 与动态 MCP，而不复制这些运行状态
- 集中保存和搜索 Recall、经验卡片与私密笔记
- 集中发布、退役、匹配和复用 Workflow 模板
- 用一个 MCP 地址同时访问 NexusDock 共享工具与路由后的 AgentDock 能力
- 使用 OAuth 或独立 MCP Access Token 认证 MCP 客户端
- 按需配置 Embedding 与外部模型，用于语义 Recall 和 Workflow 匹配
- 为在线 AgentDock 节点发布的文件生成临时下载地址

## 快速开始

最简单的方式是使用 Docker Compose。新建 `compose.yaml`：

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

创建管理员并启动 NexusDock：

```bash
docker compose run --rm nexusdock admin init admin
docker compose up -d
```

打开 `http://127.0.0.1:18777`，使用刚创建的账号登录。公网访问、HTTPS、API 认证和备份配置见[安全部署](#安全部署)与[数据、端口与备份](#数据端口与备份)。

也可以使用 `ghcr.io/uvwt/nexusdock:latest`；官方镜像支持 `linux/amd64` 和 `linux/arm64`。

## 配对 AgentDock 设备

登录 Web 控制台后，进入 **设置 → 系统与节点**，点击 **配对设备**。

NexusDock 会生成一个短时、单次使用的配对码，并给出类似命令：

```bash
agentdock nexus pair --endpoint https://nexus.example.com --code pair_xxx
```

在目标设备执行命令，并按提示重启 AgentDock。之后节点会主动向 NexusDock 建立出站连接，因此不需要把 AgentDock 节点本身开放到公网。

配对不会改变节点原有的本地 MCP 地址或认证方式，每台 AgentDock 仍然可以独立使用。

## 接入 AI 客户端

NexusDock 通过下面的地址提供 MCP Streamable HTTP：

```text
https://你的-nexus-域名/mcp
```

支持 OAuth 的 MCP 客户端可以直接连接，并在浏览器中完成授权。

对于不支持 OAuth、需要固定 Token 的客户端，进入 **设置 → MCP 接入**，使用独立的 MCP Access Token：

```text
Authorization: Bearer <MCP Access Token>
```

常见凭据彼此独立：

| 凭据 | 用途 |
| --- | --- |
| 管理员用户名和密码 | 登录 Web 控制台；受保护的管理 API 由登录会话访问 |
| MCP Access Token | 为不使用 OAuth 的客户端访问 `/mcp` |

重置 MCP Access Token 后，旧的固定 Token 会立即失效，不影响 OAuth 客户端。

## 核心能力

### 设备与 Runtime

- 集中查看设备连接状态、版本与能力
- 短时配对码与 AgentDock 主动出站连接
- 查看节点 Runtime 前显式选择目标 AgentDock
- 实时查看目标节点的任务、Skill 与动态 MCP
- 通过已有节点连接下载在线节点发布的临时 Artifact

### Recall 与 Workflow

- 共享 Markdown Recall 工作区
- 关键词搜索与可选语义召回
- 经验卡片与向量召回
- 私密笔记保险库与 age 加密备份副本
- 集中的 Workflow 模板注册表，支持版本化发布、退役、读取与匹配

NexusDock 不会自动配置或推送 Recall 的 Git remote，远程备份方式由部署者自行管理。

### MCP 网关

- 一个 MCP 入口同时提供 NexusDock 自有共享工具与已启用 AgentDock 节点能力
- 为兼容客户端提供 OAuth 授权
- 为不支持 OAuth 的客户端提供独立固定 MCP Access Token
- 基于节点的能力路由，不要求 AgentDock 节点开放入站公网端口
- 通过 [`uvwt/agentdock-protocol`](https://github.com/uvwt/agentdock-protocol) 与 AgentDock 共享协议契约

### AI 与向量设置

Embedding 与外部模型都是可选能力。需要语义 Recall、Workflow 向量匹配或相关 AI 辅助能力时，可在 **设置 → AI 与向量** 中配置。

即使不配置 Embedding 或模型，设备管理、MCP 路由、Recall 文件浏览、关键词搜索和基础 Workflow 仍然可以正常使用。

## 数据、端口与备份

| 路径 | 用途 |
| --- | --- |
| `/var/lib/nexus` | NexusDock 控制面状态、账号、设备、会话、设置、密钥与 Workflow 数据 |
| `/recall` | 共享 Recall 内容与私密笔记数据 |

默认 HTTP 端口：

```text
18777
```

升级前建议完整备份两份持久化数据。快速开始里的 Compose 使用 `nexus-data` 和 `recall` 两个 Docker Volume；如果改成宿主机目录挂载，则完整备份对应的两个目录。

```text
nexus-data/
recall/
```

升级：

```bash
docker compose pull
docker compose up -d
curl http://127.0.0.1:18777/health
```

不要运行两个 NexusDock 实例同时写同一份 Nexus 数据。

## 可选：仓库 Compose 模板

README 上面的“快速开始”是默认推荐方式，不要求使用仓库里的 Compose 文件。需要 clone 仓库并通过 `.env` 管理更多站点参数时，可以使用仓库 Compose 模板；它读取这些值：

| 变量 | 示例默认值 | 说明 |
| --- | --- | --- |
| `NEXUS_DATA_DIR` | `./nexus-data` | 挂载到容器 `/var/lib/nexus` 的宿主机目录 |
| `RECALL_REPO_DIR` | `./recall` | 挂载到容器 `/recall` 的宿主机目录 |
| `NEXUS_PUBLIC_URL` | 空 | 对外 HTTPS Origin，例如 `https://nexus.example.com` |
| `NEXUS_TRUSTED_PROXIES` | 自动 | 默认信任回环地址；Linux 容器仅在检测到唯一候选时自动追加当前容器的 RFC1918 bridge 网关。显式设置后完全覆盖自动值 |
| `NEXUS_HTTP_BIND` | `127.0.0.1` | 宿主机监听地址；容器内端口固定 `18777` |
| `NEXUS_HTTP_PORT` | `18777` | 宿主机端口；容器内端口固定 `18777` |
| `NEXUS_IMAGE` | 空 | 生产固定使用的镜像标签，例如 `ghcr.io/uvwt/nexusdock:sha-<短SHA>`；留空回退 `nexusdock:local` |

这里的 `NEXUS_DATA_DIR` 与 `RECALL_REPO_DIR` 是宿主机 bind mount 来源；官方镜像内部固定使用 `/var/lib/nexus` 和 `/recall`。

仓库 Compose 的示例值见 [`.env.example`](./.env.example)。已有自定义 Compose 可以继续使用，不需要迁移到仓库模板。

### 裸二进制与高级部署

直接运行 NexusDock 二进制时，还可以使用以下环境变量控制进程本身：

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `NEXUS_HOST` | `127.0.0.1` | HTTP 服务监听地址 |
| `NEXUS_PORT` | `18777` | HTTP 服务端口 |
| `NEXUS_LOG_LEVEL` | `info` | `debug`、`info`、`warn` 或 `error` |

上面的四个 Compose 变量在直接运行二进制时同样有效；此时 `NEXUS_DATA_DIR` 与 `RECALL_REPO_DIR` 表示应用实际数据路径，而不是 Docker 挂载来源。

## 安全部署

远程部署时管理员登录必须使用 HTTPS；只有直接访问 `localhost` / 回环地址时允许 HTTP。

宿主机 Nginx/Caddy 反代到官方 Docker 端口时，不需要手填 Docker 网段；NexusDock 仅在检测到唯一 RFC1918 默认网关时自动信任容器宿主机 bridge 网关，复杂网络拓扑必须显式配置 `NEXUS_TRUSTED_PROXIES`。反代仍需把外部协议传给 NexusDock，例如 Nginx：

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

只有复杂代理链才需要显式配置 `NEXUS_TRUSTED_PROXIES`；显式设置后不会再自动追加容器网关。若将 `NEXUS_HTTP_BIND` 改为 `0.0.0.0`，请显式配置精确可信代理并限制防火墙来源，不要继续依赖自动网关信任。

- 容器端口只绑定本机回环地址，通过 HTTPS 对外提供服务；
- 将 `NEXUS_PUBLIC_URL` 设置为真实 HTTPS Origin；
- 只信任真正位于 NexusDock 前面的反向代理；
- 使用高强度管理员密码，并通过登录会话访问受保护的管理 API；
- 限制宿主机对 `nexus-data` 与 `recall` 的访问权限；
- MCP Access Token 只用于 `/mcp`，不要用于管理 API。

官方镜像以 UID/GID `10001:10001` 运行，并适配只读根文件系统、丢弃 Linux capabilities 与 `no-new-privileges`。

忘记管理员密码时：

```bash
docker compose run --rm nexusdock admin recover
```

## 开发与贡献

克隆仓库并构建 Web UI 与 Go 服务：

```bash
git clone https://github.com/uvwt/nexusdock.git
cd nexusdock
make web-deps
make build
```

提交代码前运行常规检查：

```bash
make check
```

完整交付前运行与 CI 对应的完整验证：

```bash
make ci
```

涉及 AgentDock / NexusDock 共享契约的改动，可能还需要同步更新 [`uvwt/agentdock-protocol`](https://github.com/uvwt/agentdock-protocol) 与 [`uvwt/agentdock`](https://github.com/uvwt/agentdock)。

提交问题或功能建议请使用 [GitHub Issues](https://github.com/uvwt/nexusdock/issues)。

## 相关链接

- [AgentDock](https://github.com/uvwt/agentdock)
- [NexusDock Releases](https://github.com/uvwt/nexusdock/releases)
- [Docker Hub](https://hub.docker.com/r/agentdockio/nexusdock)
- [GitHub Container Registry](https://github.com/uvwt/nexusdock/pkgs/container/nexusdock)
- [agentdock-protocol](https://github.com/uvwt/agentdock-protocol)
