# NexusDock 开发约束

## 产品边界

NexusDock 是个人多设备 AgentDock 汇总入口。一级产品区域保持为总览、Recall、Runtime 和设置；Runtime 与节点工具必须显式选择 AgentDock 节点，Recall 等 Nexus 自有工具只公开一次。节点通过 Device Token 主动建立出站 WebSocket，不要求 AgentDock 具备公网入口。不要恢复独立 Task、Run、Skill Registry、任意权限 scope、SSE/EventBus、旧单节点 Runtime 路由或 Nexus 主动回连 AgentDock 的拓扑。

## 代码原则

- 修改前先理解现有目录、数据模型、接口契约、错误处理和测试方式。
- 主流程保持连贯，优先具体类型和清楚的数据流；不要为形式上的分层提前增加 interface、helper 或通用抽象。
- 动态 JSON 只停留在 HTTP/Runtime 边界，进入核心流程后尽快转成明确结构。
- 错误必须显式返回并包含定位上下文，不吞错，不只记录日志后继续。
- 非显然业务约束、兼容原因和安全边界使用中文注释说明“为什么”。
- 测试描述真实业务行为，优先覆盖状态变化、错误路径、安全边界和曾经出现过的问题。

## 认证与秘密

- 浏览器管理员账号只存储在 Nexus SQLite 中，通过本地 `nexusdock admin` 命令初始化或重置。
- 受保护的管理 `/v1` API 使用管理员 Web Session；不要新增独立固定管理 Bearer Token，也不要恢复旧用户名、密码或 access-file 环境变量认证。
- NexusDock 固定 MCP Token 独立存放在 `NEXUS_DATA_DIR/secrets/mcp-access-token`，仅允许 `/mcp`，可由管理员设置页查看和重置；重置后旧 Token 必须立即失效。
- AgentDock 配对使用短时单次码；Device Token 只保存在 AgentDock，Nexus 仅保存哈希。不得要求用户向 Nexus 提供 AgentDock 的 `/mcp` Token 或公网地址。

## 验证与提交

- 常规验证执行 `make check`；完整交付执行 `make ci`。
- 修改公共 API 后必须更新生成器并执行 `make contracts`。
- 修改前端后必须提交 `internal/httpx/web_dist` 中对应的嵌入式产物。
- 提交标题使用 `type(scope): 中文说明`；默认从最新 `main` 创建任务分支，验证后通过 PR 归并 `main`。
