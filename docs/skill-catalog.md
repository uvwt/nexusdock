# 中央技能库

在 **Skill → 中央技能库** 上传 ZIP 或 tar.gz，保存技能包、历史版本、来源、摘要和使用说明。**节点已安装** 仍读取所选 AgentDock 的真实安装状态。

## 上传与分类

包内必须有 `SKILL.md`，可位于根目录或唯一的一层文件夹内；frontmatter 必须包含 `name`、`description`、语义版本 `version`。`references/`、脚本与许可证一起打包。不要上传密钥、环境文件、运行缓存或整个项目仓库。

分类规则：

- **待审查**：默认分类，可以存档、下载和节点校验，但不能安装。
- **通用文档流程**：仅用于不绑定客户端、主机、平台或依赖的纯文档方法。必须完整审阅正文及引用；服务端会拦截已识别的路径、客户端、系统命令和代码依赖。自动扫描是辅助检查，不能证明任意自然语言指令在所有模型上都能正确执行。
- **需指定环境的技能**：必须填写适用环境、安装与配置、调用示例、验证方法。例如 Codex 工具、macOS 命令、特定 SSH 主机或 ComfyUI 工作流都属于此类。

非通用用法示例：

| 字段 | 示例 |
| --- | --- |
| 适用环境 | Mac AgentDock；工作目录必须包含目标仓库 |
| 安装与配置 | 安装 Git；使用现有仓库权限，不在 Skill 中保存凭据 |
| 调用示例 | 在指定仓库执行只读 `git status --short`，解释未提交文件 |
| 验证方法 | 结果与仓库实际状态一致；不修改、提交或推送文件 |

上传后可在 **审查分类与用法** 补全说明。说明允许更新，原包内容及版本摘要不可变。同名同版本、相同包重复上传是幂等操作；不同包会返回冲突，需增加版本号。

## 分发

1. 选择在线节点，在中央库选择确切版本。
2. 阅读正文、引用和用法，点击 **在所选节点校验**。
3. 校验通过后点击 **安装已校验版本**。服务端重新校验，并固定前次校验摘要。
4. 返回“已安装并激活”后，按用法配置依赖和凭据，再执行真实验证。安装本身不会执行 Skill 脚本，也不会自动设置凭据。

AgentDock 远程安装使用 ZIP。Nexus 保留原始上传包，同时生成确定顺序、统一根路径的安装 ZIP，二者各有 SHA-256。分发经过现有反向 WebSocket 调用 `skill_package`，节点用五分钟内有效、操作结束即撤销的票据向 Nexus 下载指定安装包。需要配置节点可达的 `NEXUS_PUBLIC_URL`，不要求节点开放公网端口。

离线或平台不匹配的节点会被拒绝。安装回读失败时显示“暂未确认激活版本”，不能视为安装或业务验收通过。技能环境变量仍使用 AgentDock 原有的隔离环境管理。

## 存储与接口

数据位于 `NEXUS_DATA_DIR/skill-catalog/<name>/<version>/`，应随 Nexus 数据目录备份。上限为 32 MiB 压缩包、64 MiB 解压内容、2000 个成员。拒绝路径穿越、符号链接、硬链接、重复成员及明显的凭据文件。包内文本预览上限 1 MiB；文本按原文展示，不作为 HTML 执行。

所有管理接口沿用管理员 Web Session 和 CSRF 校验：

- `GET/POST /v1/skill-catalog`：列出版本 / multipart 上传，文件字段 `package`，说明字段 `metadata` 为 JSON。
- `GET/PUT /v1/skill-catalog/{name}/{version}`：读取版本 / 更新分类与用法。
- `GET /v1/skill-catalog/{name}/{version}/download`：下载原始包。
- `GET /v1/skill-catalog/{name}/{version}/files/{filePath}`：预览正文或引用。
- `POST /v1/skill-catalog/{name}/{version}/nodes/{nodeID}`：`action=validate` 或 `action=install`，安装必须传入校验返回的 `digest`。

本功能不自动同步 Codex、Claude、Hermes 的原生目录。节点安装证明的是 AgentDock 可以读取包，不能据此宣称所有客户端都支持其中的工具。

## 开发验证

```sh
make ci
NEXUS_TEST_AGENTDOCK_BINARY=/absolute/path/to/agentdock go test ./internal/httpx -run '^TestCatalogRealAgentDockNodes$' -v -count=1 -timeout 90s
```

真实节点测试创建两个独立 AgentDock 进程、临时状态与临时端口，验证下载、摘要拒绝、安装、激活和文档回读；不使用生产节点的凭据或技能。

手工 UI 验收：`NEXUS_CATALOG_BROWSER_TEST=1 go test ./internal/httpx -run '^TestCatalogBrowserFixture$' -v -timeout 20m`，打开测试输出的回环地址。该夹具仅供隔离本地验收，不用于部署。
