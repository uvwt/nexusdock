# Workspace Control Plane

本文说明 workspace-control-plane 升级新增的 Workspace-first Runtime 控制。它们是增量能力：既有调用只传 node_id、没有传 workspace_id 时，继续保持原有行为。

## 范围与不变量

- AgentDock 仍是执行层；节点本地文件、命令、Skill、动态 MCP、浏览器状态与任务不会搬到 NexusDock。
- 节点选择仍然显式进行，本版本不会自动挑选 AgentDock 节点。
- Project Workspace 按调用 opt-in，入口是 workspace_id。
- 严格 Workspace 模式在无法证明边界安全时采用 fail-closed。
- Runtime Workspace 管理接口属于受保护管理 API，沿用已登录管理员 Session。
- 创建 Workspace 本身不会触发 push、merge 或部署。

## Workspace 模型

Runtime Workspace 包含：

| 字段 | 作用 |
| --- | --- |
| id | 稳定 ID，2-63 位小写字母、数字或连字符 |
| name | 项目可读名称 |
| node_id | Workspace 绑定的 AgentDock 节点 |
| project_root | 项目主文件根目录；写操作必须留在这里 |
| domain | 可选站点域名，用于 URL 类参数约束 |
| allowed_mcp | 允许使用的动态 MCP 服务白名单 |
| context_roots | 附加只读文件根目录 |
| design_authorities | 供客户端/Workflow 使用的设计与内容权威文件声明；本版本作为元数据保存 |
| route_authority | 可选 URL 路由权威文件；相对 project_root，或位于 Workspace 可读根目录中 |

示例：

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

Workspace CRUD：

| 方法 | 接口 |
| --- | --- |
| GET | /v1/runtime/workspaces |
| POST | /v1/runtime/workspaces |
| GET | /v1/runtime/workspaces/{workspaceID} |
| PATCH | /v1/runtime/workspaces/{workspaceID} |
| DELETE | /v1/runtime/workspaces/{workspaceID} |

配置的 node_id 必须已经存在于 NexusDock。

## 严格 Workspace 执行

所有路由到 AgentDock 的工具仍然必须显式提供 node_id；只有同时提供 workspace_id 时，才启用严格 Workspace 检查。

| 边界 | 严格 Workspace 行为 |
| --- | --- |
| Node | node_id 必须与 Workspace 绑定节点一致 |
| 动态 MCP | mcp_tool_call / mcp_tool_inspect 必须使用白名单内服务的 qualified tool；mcp_tool_search 必须明确指定允许的 server；禁止无边界列出全部 MCP |
| 文件读取 | 只能位于 project_root 或 context_roots；skill:// 读取仍允许 |
| 文件写入 | file_edit 的写入/移动必须留在 project_root |
| 域名 | URL/URI/domain/host/endpoint 类参数必须属于配置域名或其子域 |
| Route Authority | mcp_tool_call 中的路由类参数必须存在于配置的权威文件 |
| 命令执行 | 当前严格 Workspace 模式拒绝 exec_command |

exec_command 的限制是有意设计。当前 AgentDock 尚未暴露 NexusDock 可以验证的 OS sandbox capability，因此 NexusDock 无法证明任意 shell 命令一定不会越过 project_root。没有 workspace_id 的旧调用保持原有命令行为。

Route Authority 文件自身也必须解析到 project_root 或 context_roots 内；相对路径的父级穿越和可读根目录之外的绝对路径都会被拒绝。

严格调用真正转发前，NexusDock 会移除 node_id 与 workspace_id，避免 Nexus 专用路由元数据泄露给 AgentDock 工具实现。

## Route Authority

Route Authority 为可选功能。配置后，NexusDock 会在带路由参数的动态 MCP 调用前，从 Workspace 绑定节点实时读取权威文件。

候选路由字段包括 url、page_url、post_url、target_url、permalink、route、path、slug，以及以 _permalink、_route、_slug 结尾的字段。

边界说明：

- 当前只对 mcp_tool_call 应用 Route Authority。
- 只检查结构化路由候选字段，不扫描任意自由文本内容。
- 外部 URL 由 Workspace domain policy 处理。
- 如果绑定节点没有 Workspace-safe 的 filesystem.read 能力，或权威文件无法读取，调用 fail-closed。
- 权威文件必须位于 project_root 或 context_roots。

## 审计

所有路由到 AgentDock 的工具调用都会写入现有 append-only audit_events。

读取接口：

    GET /v1/runtime/audit

支持参数：

| 参数 | 值 |
| --- | --- |
| limit | 1-500，默认 100 |
| workspace_id | 精确 Workspace ID |
| node_id | 精确节点 ID |
| risk | low、medium、high |
| result | succeeded、failed |

审计记录包含 actor、节点/Workspace/工具、风险级别、结果、request ID、耗时和脱敏 metadata。该工具审计路径不会持久化完整工具参数、content、patch、password、secret、token 或 api_key。

当前风险分类：

| 风险 | 工具 |
| --- | --- |
| high | file_edit、exec_command、mcp_tool_call、mcp_manage |
| medium | file_publish、task_manage、workflow_template_manage |
| low | 其他路由 AgentDock 工具 |

## 并发与资源锁

路由到 AgentDock 的工具在真正执行前会经过并发闸门和确定性资源锁。

环境变量：

| 变量 | 默认值 | 有效范围 |
| --- | ---: | ---: |
| NEXUS_TOOL_CONCURRENCY_GLOBAL | 16 | 1-256 |
| NEXUS_TOOL_CONCURRENCY_PER_NODE | 4 | 1-64 |
| NEXUS_TOOL_CONCURRENCY_PER_WORKSPACE | 3 | 1-64 |
| NEXUS_TOOL_QUEUE_TIMEOUT_SECONDS | 30 | 1-300 |

资源锁会把同一个派生文件、路由、远端对象、task/workflow 或 MCP 配置资源上的冲突操作串行化；无关资源仍可并行。锁获取顺序固定，用于避免 lock-order deadlock。

## 节点健康与能力发现

节点状态接口：

    GET /v1/runtime/nodes/{nodeID}/status

返回：

- ready / offline / disabled / incompatible 健康状态；
- online/enabled/compatibility 与 last-seen age；
- 协议兼容信息；
- 逻辑能力解析结果；
- Workspace-safe 能力解析结果；
- 当前并发预算；
- 路由语义。

本版本明确返回：

    explicit_node_required: true
    automatic_node_selection: false

能力解析依赖协议兼容性与节点实际声明的 capabilities，而不是硬编码 AgentDock runtime version。这样未来 provider alias 可以变化，上层无需依赖 AgentDock 内部实现名。

## 兼容矩阵

| 场景 | 行为 |
| --- | --- |
| 既有客户端只传 node_id | 兼容；保留旧行为 |
| 客户端增加 workspace_id | 该调用启用严格 Workspace policy |
| AgentDock 协议等于共享 ConnectionProtocolVersion | 兼容 |
| AgentDock runtime version 改变，但协议/能力仍兼容 | 由兼容抽象支持 |
| AgentDock 协议缺失或不同 | 节点标记 incompatible，不提供 Workspace-safe 路由 |
| 节点 disabled | health=disabled，不作为候选 |
| 节点 offline | health=offline，不作为候选 |
| 严格 Workspace + exec_command | 在有可验证 sandbox capability 前拒绝 |
| 已配置的 Route Authority 不可读或解析到 Workspace 可读根目录之外 | 带路由的动态 MCP 调用 fail-closed |
| schema v3 既有数据 | 向前迁移到 schema v4，新增 runtime_workspaces |
| 尚未采用 Workspace 的既有调用 | 不强制迁移 |

本次开发环境已用当前 AgentDock 0.8.3 节点完成回归验证；兼容性定义仍以共享协议契约与节点声明能力为准，不以 0.8.3 版本字符串作为硬条件。

## 推荐 rollout

### Stage 0 - 先部署但保持旧路由

使用默认并发值部署，不给既有客户端增加 workspace_id。先验证 health/readiness、现有 AgentDock 连接和旧工具调用正常。

### Stage 1 - 注册 Workspace

按项目创建 Workspace 并绑定正确节点。优先确保 project_root、allowed_mcp、domain 准确；只有确实需要读取项目外文件时才增加 context_roots。

### Stage 2 - 先迁移读操作

先给 read/list/search/image 等低风险调用加入 workspace_id。检查审计中 workspace_id 是否正确，并确认没有误拦截必须的文件或 MCP 访问。

### Stage 3 - 打开 Route Authority 与动态 MCP 写操作

需要 URL Ownership 的项目再配置 route_authority。白名单、根目录、域名和权威文件验证通过后，再迁移动态 MCP 与文件写操作。

### Stage 4 - 调整并发

通过 /v1/runtime/audit 与 /v1/runtime/nodes/{nodeID}/status 观察失败、排队压力和有效限额。保守调整四个 NEXUS_TOOL_* 变量，不建议同时激进提高 global 与 per-node 上限。

### Stage 5 - shell 继续走 legacy，等待 sandbox

不要通过削弱 Workspace 文件检查来绕过 exec_command 的严格拒绝。确实需要命令执行时继续使用不带 workspace_id 的旧 node-only 路径，或等待 AgentDock 提供 NexusDock 可协商、可验证的 sandbox capability。

## 回滚

Workspace 是按调用启用，因此操作回滚很直接：

1. 让受影响客户端/Workflow 停止发送 workspace_id。
2. Workspace 记录可保留以后复用，也可以通过受保护管理 API 删除。
3. 如果调高并发导致排队压力，将四个并发环境变量恢复默认值。
4. 不要在没有数据库回滚方案的情况下直接降级数据文件；schema v4 新增 runtime_workspaces。

移除 workspace_id 会恢复旧路由行为，不会删除项目数据，也不会修改 AgentDock 配置。

## Release Gate

合并或部署该控制面升级前，至少验证：

- go test ./...
- go vet ./...
- go build ./cmd/nexusdock
- contracts 生成/检查
- repository boundary 检查
- migration/e2e/security 测试
- git diff --check
- 如果仓库历史文件包含 CRLF，在行尾中性的 checkout 中验证 gofmt 与 go mod tidy -diff
- README、环境变量模板、Compose 透传、本兼容矩阵与回滚说明保持一致
