// Code generated from scripts/generate-contracts.py by swagger-typescript-api. DO NOT EDIT.
// OpenAPI-SHA256: 24258ed9f60e5f8485c9b8bb4ea2cf4a247c8566ab7cb1bd7244248b8e1d6090

/** 与 Nexus 配对的一台 AgentDock 节点。 */
export interface AgentDockNode {
  /** 节点架构。 */
  arch?: string;
  /** 节点报告的工具能力。 */
  capabilities: string[];
  /**
   * RFC 3339 UTC 时间。
   * @format date-time
   */
  created_at: string;
  /** AgentDock 生成的稳定设备 ID。 */
  device_id: string;
  /** 节点是否允许连接和 Runtime 请求。 */
  enabled: boolean;
  /** Nexus 分配的稳定节点 ID。 */
  id: string;
  /**
   * RFC 3339 UTC 时间。
   * @format date-time
   */
  last_seen_at?: string;
  /**
   * 节点显示名称。
   * @minLength 1
   * @maxLength 100
   */
  name: string;
  /** 节点是否保持反向连接。 */
  online: boolean;
  /** 节点操作系统。 */
  os?: string;
  /** 节点连接协议版本。 */
  protocol_version?: string;
  /** 节点工具契约摘要。 */
  tool_contract_hash?: string;
  /**
   * RFC 3339 UTC 时间。
   * @format date-time
   */
  updated_at: string;
  /** 最近握手的 AgentDock 版本。 */
  version?: string;
}

/** AgentDock 节点列表。 */
export interface AgentDockNodeListResponse {
  /**
   * 节点数量。
   * @min 0
   */
  count: number;
  /** AgentDock 节点。 */
  nodes: AgentDockNode[];
  /** 请求是否成功。 */
  ok: boolean;
}

/** 单个 AgentDock 节点响应。 */
export interface AgentDockNodeResponse {
  /** 与 Nexus 配对的一台 AgentDock 节点。 */
  node: AgentDockNode;
  /** 请求是否成功。 */
  ok: boolean;
}

/** 更新 AgentDock 节点显示信息或启用状态。 */
export interface AgentDockNodeUpdateRequest {
  /** 节点是否启用。 */
  enabled?: boolean;
  /**
   * 节点显示名称。
   * @minLength 1
   * @maxLength 100
   */
  name?: string;
}

/** AgentDock 使用单次码换取固定设备身份。 */
export interface AgentDockPairRequest {
  /**
   * 单次配对码。
   * @minLength 1
   */
  code: string;
  /**
   * AgentDock 本地生成的稳定设备 ID。
   * @minLength 8
   * @maxLength 128
   */
  device_id: string;
  /**
   * 节点显示名称。
   * @minLength 1
   * @maxLength 100
   */
  name: string;
}

/** 短时单次 AgentDock 配对码。 */
export interface AgentDockPairingCode {
  /** 单次配对码。 */
  code: string;
  /**
   * RFC 3339 UTC 时间。
   * @format date-time
   */
  expires_at: string;
}

/** AgentDock 配对码响应。 */
export interface AgentDockPairingCodeResponse {
  /** 请求是否成功。 */
  ok: boolean;
  /** 短时单次 AgentDock 配对码。 */
  pairing: AgentDockPairingCode;
}

/** 管理员密码更新请求。 */
export interface AuthCredentialUpdateRequest {
  /**
   * 当前密码。
   * @minLength 1
   * @maxLength 1024
   */
  current: string;
  /**
   * 符合策略的新密码。
   * @minLength 12
   * @maxLength 1024
   */
  next: string;
}

/** 管理员密码更新结果。 */
export interface AuthCredentialUpdateResponse {
  /** 请求是否成功。 */
  ok: boolean;
  /** 是否必须重新登录。 */
  reauthenticate: boolean;
}

/** 管理员登录凭据。 */
export interface AuthLoginRequest {
  /**
   * 管理员密码。
   * @minLength 1
   * @maxLength 1024
   */
  password: string;
  /** 是否创建最长 30 天的记住登录会话。 */
  remember_me?: boolean;
  /**
   * 管理员用户名。
   * @minLength 1
   */
  username: string;
}

/** 管理员认证初始化状态。 */
export interface AuthStatusResponse {
  /** 管理员凭据是否已初始化。 */
  initialized: boolean;
  /** 请求是否成功。 */
  ok: boolean;
}

/** Recall 向量索引摘要。 */
export interface EmbeddingIndexSummary {
  /**
   * 索引文档数量。
   * @min 0
   */
  count: number;
  /**
   * 向量维度。
   * @min 0
   */
  dimension?: number;
  /** 索引使用的嵌入模型。 */
  model: string;
  /**
   * RFC 3339 UTC 时间。
   * @format date-time
   */
  updated_at: string;
}

/** 重建 Recall 向量索引。 */
export interface EmbeddingReindexRequest {
  /**
   * 最多索引条目数。
   * @min 1
   * @max 2000
   */
  max_entries?: number;
  /** 要索引的 Recall 路径前缀。 */
  prefix?: string;
}

/** Recall 向量索引重建结果。 */
export interface EmbeddingReindexResponse {
  /**
   * 索引文档数量。
   * @min 0
   */
  count: number;
  /**
   * 向量维度。
   * @min 0
   */
  dimension?: number;
  /** 嵌入服务是否启用。 */
  enabled: boolean;
  /** 嵌入端点。 */
  endpoint?: string;
  /** 索引文件路径。 */
  index_path: string;
  /** 索引使用的嵌入模型。 */
  model: string;
  /** 请求是否成功。 */
  ok: boolean;
  /** 已索引路径前缀。 */
  prefix: string;
  /**
   * RFC 3339 UTC 时间。
   * @format date-time
   */
  updated_at: string;
}

/** Recall 向量搜索命中。 */
export interface EmbeddingSearchHit {
  /** 命中文档 Frontmatter。 */
  frontmatter?: Record<string, string>;
  /** Recall 相对路径。 */
  path: string;
  /**
   * 余弦相似度。
   * @min -1
   * @max 1
   */
  score: number;
  /** 文档文本片段。 */
  snippet: string;
  /** Markdown 标题。 */
  title?: string;
}

/** 使用 Recall 向量索引执行语义搜索。 */
export interface EmbeddingSearchRequest {
  /**
   * 最大结果数。
   * @min 1
   * @max 50
   */
  max_results?: number;
  /** 可选的 Recall 路径前缀。 */
  prefix?: string;
  /**
   * 语义查询。
   * @minLength 1
   */
  query: string;
}

/** Recall 向量搜索结果。 */
export interface EmbeddingSearchResponse {
  /**
   * 返回命中数量。
   * @min 0
   */
  count: number;
  /** 嵌入服务是否启用。 */
  enabled: boolean;
  /** Recall 向量索引摘要。 */
  index: EmbeddingIndexSummary;
  /** 查询使用的嵌入模型。 */
  model: string;
  /** 请求是否成功。 */
  ok: boolean;
  /** 原始语义查询。 */
  query: string;
  /** 按相似度降序排列的命中。 */
  results: EmbeddingSearchHit[];
}

/** 向量检索与 Embedding 运行时配置。 */
export interface EmbeddingSettingsInput {
  /** 运行时 AI 密钥更新指令；服务永不回显密钥明文。 */
  api_key: RuntimeSecretUpdate;
  /** 是否启用向量检索。 */
  enabled: boolean;
  /** OpenAI 兼容 Embeddings HTTP(S) 地址。 */
  endpoint: string;
  /** Embedding 模型名称。 */
  model: string;
  /**
   * Embedding 请求超时秒数。
   * @min 1
   * @max 300
   */
  timeout_seconds: number;
}

/** 已脱敏的向量检索运行时配置。 */
export interface EmbeddingSettingsView {
  /** 是否已配置 API Key；不返回明文。 */
  api_key_configured: boolean;
  /** 是否启用向量检索。 */
  enabled: boolean;
  /** 当前 Embeddings 地址。 */
  endpoint: string;
  /** 当前 Embedding 模型。 */
  model: string;
  /**
   * 请求超时秒数。
   * @min 1
   * @max 300
   */
  timeout_seconds: number;
}

/** Recall 嵌入服务及索引状态。 */
export interface EmbeddingStatusResponse {
  /** 嵌入端点是否配置。 */
  configured: boolean;
  /** 嵌入服务是否启用。 */
  enabled: boolean;
  /** 当前嵌入端点。 */
  endpoint?: string;
  /** 最近一次探测错误。 */
  error?: string;
  /** Recall 向量索引摘要。 */
  index?: EmbeddingIndexSummary;
  /** 本地索引文件路径。 */
  index_path?: string;
  /** 当前嵌入模型。 */
  model?: string;
  /** 状态查询是否成功。 */
  ok: boolean;
  /** 嵌入端点是否可达。 */
  reachable?: boolean;
  /** 未启用原因。 */
  reason?: string;
}

/** 字段级错误详情。 */
export interface ErrorDetail {
  /** 字段路径。 */
  field?: string;
  /** 可读错误说明。 */
  message: string;
  /** 稳定原因标识。 */
  reason: string;
}

/** 统一错误响应。 */
export interface ErrorResponse {
  /** 稳定错误码。 */
  code: string;
  /** 可选字段级错误。 */
  details?: ErrorDetail[];
  /** 面向调用方的错误说明。 */
  message: string;
  /** 请求关联 ID。 */
  request_id: string;
}

/** 服务健康状态。 */
export interface HealthResponse {
  /** 服务是否健康。 */
  ok: boolean;
  /** 服务名称。 */
  service: string;
}

/** 通用结构化对象。 */
export type JsonObject = Record<string, any>;

/** Nexus 与浏览器接口使用的错误信封。 */
export interface LegacyErrorEnvelope {
  /** 错误详情。 */
  error: {
    /** 稳定错误码。 */
    code: string;
    /** 可读错误说明。 */
    message: string;
  };
  /** 固定为 false。 */
  ok: boolean;
  /** 请求关联 ID。 */
  request_id: string;
}

/** Nexus MCP 固定访问 Token。仅管理员接口返回明文。 */
export interface MCPAccessTokenResponse {
  /** 请求是否成功。 */
  ok: boolean;
  /**
   * 用于 Nexus /mcp 的 Bearer Token。
   * @minLength 64
   * @maxLength 64
   */
  token: string;
}

/** Nexus MCP 接入设置。管理员接口会同时返回固定访问 Token 与 Apps UI 开关状态。 */
export interface MCPSettingsResponse {
  /** 是否向 MCP 客户端发布 MCP Apps UI 元数据与资源。 */
  mcp_apps_enabled: boolean;
  /** 请求是否成功。 */
  ok: boolean;
  /** 是否已保存 SQLite 覆盖配置；false 表示当前来自环境变量或默认值。 */
  persisted: boolean;
  /**
   * 用于 Nexus /mcp 的 Bearer Token。
   * @minLength 64
   * @maxLength 64
   */
  token: string;
  /**
   * 最近一次持久化更新时间。
   * @format date-time
   */
  updated_at?: string;
}

/** 更新 Nexus MCP Apps UI 开关。 */
export interface MCPSettingsUpdateRequest {
  /** 是否启用 MCP Apps UI。 */
  mcp_apps_enabled: boolean;
}

/** 无附加数据的成功响应。 */
export interface OperationOK {
  /** 固定为 true。 */
  ok: boolean;
}

/** 同时删除私密笔记明文和 age 密文的请求。 */
export interface PrivateNoteDeleteRequest {
  /** 真实删除必须为 true。 */
  confirmed: boolean;
  /** notes/ 下的私密笔记相对路径。 */
  path: string;
}

/** 私密笔记明文与 age 密文删除结果。 */
export interface PrivateNoteDeleteResponse {
  /** 固定为 delete。 */
  action: string;
  /** 密文是否删除。 */
  deleted_encrypted: boolean;
  /** 明文是否删除。 */
  deleted_plaintext: boolean;
  /** age 密文相对路径。 */
  encrypted_path: string;
  /** 明文相对路径。 */
  path: string;
  /** Nexus 私密笔记根目录。 */
  root: string;
}

/** 私密笔记加密维护请求。 */
export interface PrivateNoteMaintenanceRequest {
  /** 维护动作。 */
  action: "init" | "init-encryption" | "sync-encrypted" | "encrypt-all";
}

/** 私密笔记加密初始化或全量重加密结果。 */
export interface PrivateNoteMaintenanceResponse {
  /** 执行的维护动作。 */
  action: string;
  /** 加密算法。 */
  algorithm: string;
  /**
   * 生成的密文数量。
   * @min 0
   */
  encrypted_count?: number;
  /** 是否新建 identity。 */
  identity_created?: boolean;
  /** age 公钥接收者。 */
  recipient?: string;
  /** Nexus 私密笔记根目录。 */
  root: string;
}

/** 显式读取私密笔记正文的请求。 */
export interface PrivateNoteReadRequest {
  /**
   * 最大返回字节数。
   * @min 1
   * @max 1048576
   */
  max_bytes?: number;
  /** notes/ 下的私密笔记相对路径。 */
  path: string;
}

/** 显式私密笔记明文读取结果。 */
export interface PrivateNoteReadResponse {
  /** 固定为 read。 */
  action: string;
  /** 正文是否被标记为含敏感信息。 */
  contains_secret: boolean;
  /** 私密笔记明文，仅显式读取接口返回。 */
  content: string;
  /** age 密文相对路径。 */
  encrypted_path: string;
  /** 明文相对路径。 */
  path: string;
  /** Nexus 私密笔记根目录。 */
  root: string;
  /** 正文是否被截断。 */
  truncated: boolean;
}

/** 私密笔记元数据检索请求。 */
export interface PrivateNoteSearchRequest {
  /**
   * 最大结果数。
   * @min 1
   * @max 100
   */
  max_results?: number;
  /** 仅匹配标题、简介、标签、分类和路径的查询。 */
  query: string;
}

/** 私密笔记元数据检索结果。 */
export interface PrivateNoteSearchResponse {
  /** 固定为 search。 */
  action: string;
  /**
   * 结果数。
   * @min 0
   */
  count: number;
  /** 固定为 true。 */
  metadata_only: boolean;
  /** 请求是否成功。 */
  ok: boolean;
  /** 检索安全策略说明。 */
  policy?: string;
  /** 原始查询。 */
  query: string;
  /** 仅含安全元数据的结果。 */
  results: PrivateNoteSummary[];
  /** Nexus 私密笔记根目录。 */
  root: string;
}

/** 读取私密笔记状态或安全元数据列表。 */
export interface PrivateNoteStatusRequest {
  /** 状态动作。 */
  action: "check" | "list";
}

/** 私密笔记加密和 Git 忽略状态。 */
export interface PrivateNoteStatusResponse {
  /** 执行的状态动作。 */
  action: string;
  /**
   * 列表项数。
   * @min 0
   */
  count?: number;
  /** 明文与 age 密文是否一一对应且内容一致。 */
  encrypted_backup_ok: boolean;
  /** .keys/ 是否由仓库规则忽略。 */
  keys_git_ignored: boolean;
  /** 缺失的 age 密文路径。 */
  missing_encrypted?: string[];
  /** 私密笔记安全元数据。 */
  notes?: PrivateNoteSummary[];
  /**
   * 明文笔记数。
   * @min 0
   */
  notes_count?: number;
  /** 没有对应明文的孤儿 age 密文路径。 */
  orphaned_encrypted?: string[];
  /** notes/ 是否由仓库规则忽略。 */
  plaintext_git_ignored: boolean;
  /** Nexus 私密笔记根目录。 */
  root: string;
  /** 与当前明文不一致或无法校验的 age 密文路径。 */
  stale_encrypted?: string[];
}

/** 私密笔记安全元数据；不包含正文或正文片段。 */
export interface PrivateNoteSummary {
  /** 私密笔记分类。 */
  category?: string;
  /** 正文是否被标记为含敏感信息。 */
  contains_secret: boolean;
  /** 对应 age 密文相对路径。 */
  encrypted_path: string;
  /** notes/ 下的私密笔记相对路径。 */
  path: string;
  /**
   * 元数据检索匹配分数。
   * @min 0
   */
  score?: number;
  /** 人工维护的安全简介。 */
  summary?: string;
  /** 安全标签。 */
  tags?: string[];
  /** 私密笔记标题。 */
  title?: string;
  /**
   * RFC 3339 UTC 时间。
   * @format date-time
   */
  updated_at?: string;
}

/** 创建或覆盖私密笔记请求。 */
export interface PrivateNoteWriteRequest {
  /** 未传 path 时使用的分类。 */
  category?: string;
  /** 真实写入必须为 true。 */
  confirmed: boolean;
  /** 私密笔记正文。 */
  content: string;
  /** 是否覆盖已有笔记。 */
  overwrite?: boolean;
  /** 可选的 notes/ 相对路径。 */
  path?: string;
  /** 可安全检索的人工简介。 */
  summary?: string;
  /** 可安全检索的标签。 */
  tags?: string[];
  /** 标题，也可用于生成路径。 */
  title?: string;
}

/** 私密笔记明文与 age 密文原子写入结果。 */
export interface PrivateNoteWriteResponse {
  /** 固定为 write。 */
  action: string;
  /** 加密算法。 */
  algorithm: string;
  /** 密文是否写入。 */
  encrypted: boolean;
  /** age 密文相对路径。 */
  encrypted_path: string;
  /** 明文相对路径。 */
  path: string;
  /** Nexus 私密笔记根目录。 */
  root: string;
  /** 明文是否写入。 */
  written: boolean;
}

/** 规范化后的 Recall 卡片。 */
export interface RecallCard {
  /** 适用边界。 */
  boundary?: string;
  /** 卡片可信度。 */
  confidence: string;
  /** 卡片正文。 */
  content: string;
  /** 验证证据。 */
  evidence?: string;
  /** 最终 Recall 相对路径。 */
  path: string;
  /** 项目标识。 */
  project: string;
  /** 卡片作用域。 */
  scope: string;
  /** 信息来源。 */
  source: string;
  /** 卡片状态。 */
  status: string;
  /** 规范化标签。 */
  tags?: string[];
  /** 卡片标题。 */
  title: string;
  /** 卡片类型。 */
  type: string;
}

/** 卡片写入前的规范化、风险提示和去重计划。 */
export interface RecallCardCaptureResponse {
  /** 通用结构化对象。 */
  capture_plan: JsonObject;
  /** 规范化后的 Recall 卡片。 */
  card: RecallCard;
  /** 请求是否成功。 */
  ok: boolean;
  /**
   * 相似卡片数量。
   * @min 0
   */
  similar_count: number;
  /** 关键词相似的已有卡片。 */
  similar_results?: RecallSearchResult[];
  /** 需人工审阅的规范警告。 */
  warnings?: string[];
}

/** Recall 卡片文件列表和展示摘要。 */
export interface RecallCardListResponse {
  /** 只读卡片摘要。 */
  cards: RecallCardSummary[];
  /**
   * 卡片数量。
   * @min 0
   */
  count: number;
  /** 卡片目录下的文件和目录。 */
  entries: RecallFileEntry[];
  /** 请求是否成功。 */
  ok: boolean;
  /** 固定为 recall/managed/cards。 */
  prefix: string;
}

/** 捕获或写入一张可复用 Recall 卡片。 */
export interface RecallCardRequest {
  /** 是否在已审阅后接受规范警告。 */
  allow_warnings?: boolean;
  /** 卡片适用边界。 */
  boundary?: string;
  /** 卡片可信度。 */
  confidence?: "unknown" | "low" | "medium" | "high";
  /** 真实写入时必须为 true。 */
  confirmed?: boolean;
  /** 卡片正文。 */
  content?: string;
  /** 验证卡片内容的证据。 */
  evidence?: string;
  /**
   * 捕获阶段相似项最大数量。
   * @min 1
   * @max 50
   */
  max_results?: number;
  /** 是否覆盖同路径卡片。 */
  overwrite?: boolean;
  /** 可选的 recall/managed/cards/ 自定义路径。 */
  path?: string;
  /** 项目标识；为空时使用 global。 */
  project?: string;
  /** 卡片作用域。 */
  scope?: "global" | "project" | "device";
  /** 卡片信息来源。 */
  source?: string;
  /** 卡片状态。 */
  status?:
    | "inbox"
    | "active"
    | "verified"
    | "stale"
    | "archived"
    | "rejected"
    | "conflicted"
    | "unverified"
    | "deprecated";
  /** content 为空时使用的摘要正文。 */
  summary?: string;
  /** 卡片标签。 */
  tags?: string[];
  /**
   * 卡片标题。
   * @minLength 1
   */
  title: string;
  /** 卡片类型。 */
  type?:
    | "preference"
    | "runbook"
    | "bug_pattern"
    | "deploy_note"
    | "project_trap"
    | "architecture"
    | "decision"
    | "anti_pattern";
}

/** 在 Recall 卡片目录中执行关键词搜索。 */
export interface RecallCardSearchRequest {
  /**
   * 最大结果数。
   * @min 1
   * @max 200
   */
  max_results?: number;
  /**
   * 关键词查询。
   * @minLength 1
   */
  query: string;
}

/** Recall 卡片关键词搜索结果。 */
export interface RecallCardSearchResponse {
  /**
   * 结果数量。
   * @min 0
   */
  count: number;
  /** 请求是否成功。 */
  ok: boolean;
  /** 固定为 recall/managed/cards。 */
  prefix: string;
  /** 原始查询。 */
  query: string;
  /** 搜索命中。 */
  results: RecallSearchResult[];
}

/** 经验卡片列表使用的只读摘要。 */
export interface RecallCardSummary {
  /** 卡片类型。 */
  card_type: string;
  /** 卡片可信度。 */
  confidence?: string;
  /** 卡片文件最近修改时间。 */
  modified?: string;
  /** 卡片 Recall 相对路径。 */
  path: string;
  /** 项目标识。 */
  project: string;
  /** 卡片作用域。 */
  scope?: string;
  /**
   * 卡片文件大小。
   * @min 0
   */
  size_bytes?: number;
  /** 卡片生命周期状态。 */
  status: string;
  /** 卡片标签。 */
  tags?: string[];
  /** 从卡片正文标题解析出的展示标题。 */
  title: string;
}

/** 卡片及其 Recall 文件写入结果。 */
export interface RecallCardWriteResponse {
  /** 规范化后的 Recall 卡片。 */
  card: RecallCard;
  /** 卡片索引策略说明。 */
  index_policy: string;
  /** 请求是否成功。 */
  ok: boolean;
  /** 读取或写入后的完整 Recall 文本记录。 */
  recall: RecallRecord;
  /** 已接受的规范警告。 */
  warnings?: string[];
}

/** 按类别配额和总字节预算裁剪后的 Recall 启动索引。 */
export interface RecallContextIndex {
  /** 按 profile、project、verified_fact、runbook、card 顺序排列的候选。 */
  items: RecallContextIndexItem[];
  /**
   * items 允许的最大 JSON 字节预算。
   * @min 1
   * @max 32000
   */
  max_bytes: number;
  /**
   * 因预算或候选不可读而省略的条目数。
   * @min 0
   */
  omitted_count?: number;
  /** 规范化后的项目标识。 */
  project?: string;
  /**
   * items 实际 JSON 编码字节数。
   * @min 0
   */
  total_bytes: number;
  /** 是否因预算或候选不可读而省略了条目。 */
  truncated: boolean;
}

/** 紧凑 Recall 启动索引中的单条候选。 */
export interface RecallContextIndexItem {
  /** 用于路由到完整文档的别名。 */
  aliases?: string[];
  /** 经验卡片类型。 */
  card_type?: string;
  /** 候选可信度。 */
  confidence?: "low" | "medium" | "high";
  /** 用于路由到完整文档的关键词。 */
  keywords?: string[];
  /** 候选类别。 */
  kind: "profile" | "project" | "verified_fact" | "runbook" | "card";
  /** 可直接交给 recall_read 的 Recall 相对路径。 */
  path: string;
  /** 进入启动索引的 Recall 生命周期状态。 */
  status?: "active" | "verified";
  /** 仅对可安全独立理解的类别返回的短摘要。 */
  summary?: string;
  /** 卡片标签。 */
  tags?: string[];
  /** 用于判断相关性的短标题。 */
  title?: string;
  /**
   * RFC 3339 UTC 时间。
   * @format date-time
   */
  verified_at?: string;
}

/** 为 agentdock_context 构造无查询的紧凑 Recall 启动索引。 */
export interface RecallContextIndexRequest {
  /**
   * 索引 items 的最大 JSON 字节预算；无效值使用服务默认值。
   * @min 2
   * @max 32000
   */
  max_bytes?: number;
  /** 项目标识；为空时只返回全局可用条目。 */
  project?: string;
}

/** 紧凑 Recall 启动索引响应。 */
export interface RecallContextIndexResponse {
  /** 按类别配额和总字节预算裁剪后的 Recall 启动索引。 */
  context_index: RecallContextIndex;
  /** 请求是否成功。 */
  ok: boolean;
}

/** Markdown 召回条目。 */
export interface RecallEntry {
  /** Markdown 或文本内容。 */
  content?: string;
  /**
   * RFC 3339 UTC 时间。
   * @format date-time
   */
  modified_at?: string;
  /** 召回相对路径。 */
  path: string;
  /**
   * 内容字节数。
   * @min 0
   */
  size_bytes?: number;
}

/** Recall 仓库中的文件或目录条目。 */
export interface RecallFileEntry {
  /**
   * RFC 3339 UTC 时间。
   * @format date-time
   */
  modified?: string;
  /** 文件或目录名。 */
  name: string;
  /** Recall 相对路径。 */
  path: string;
  /**
   * 条目字节数。
   * @min 0
   */
  size_bytes?: number;
  /** 条目类型。 */
  type: "file" | "directory";
}

/** 读取或写入后的完整 Recall 文本记录。 */
export interface RecallRecord {
  /** 移除 Frontmatter 后的正文。 */
  body: string;
  /** 包含 Frontmatter 的完整文本。 */
  content: string;
  /** 解析后的 Frontmatter 字符串字段。 */
  frontmatter: Record<string, string>;
  /** Recall 相对路径。 */
  path: string;
  /**
   * 文本字节数。
   * @min 0
   */
  size_bytes: number;
}

/** Recall 读取或写入结果。 */
export interface RecallRecordResponse {
  /** 请求是否成功。 */
  ok: boolean;
  /** 读取或写入后的完整 Recall 文本记录。 */
  recall: RecallRecord;
}

/** Recall 关键词搜索命中。 */
export interface RecallSearchResult {
  /** 命中文档 Frontmatter。 */
  frontmatter: Record<string, string>;
  /** 命中的文档字段。 */
  matched_fields?: string[];
  /** 命中的查询词。 */
  matched_terms?: string[];
  /** Recall 相对路径。 */
  path: string;
  /** 命中位置附近的文本片段。 */
  snippet: string;
  /** Markdown 标题。 */
  title?: string;
}

/** Recall 写入预检结果；只执行真实写入会使用的路径、内容和覆盖校验，不产生持久化副作用。 */
export interface RecallWritePreviewResponse {
  /** 请求携带的确认标记；预检本身不要求确认。 */
  confirmed: boolean;
  /** 固定为 true，表示未执行持久化写入。 */
  dry_run: boolean;
  /** 请求是否成功。 */
  ok: boolean;
  /** 预检使用的覆盖语义。 */
  overwrite: boolean;
  /** 规范化后的 Recall 相对路径。 */
  path: string;
  /** 真实写入时会持久化的规范化内容。 */
  proposed_content: string;
}

/** 创建或覆盖 Recall 文本记录。 */
export interface RecallWriteRequest {
  /** Agent 标识。 */
  agent?: string;
  /** 可信度。 */
  confidence?: "unknown" | "low" | "medium" | "high";
  /** 写入受保护目录时的确认标记。 */
  confirmed?: boolean;
  /** Markdown 或文本内容。 */
  content: string;
  /** 设备标识。 */
  device?: string;
  /** 是否覆盖已有文件。 */
  overwrite?: boolean;
  /**
   * Recall 相对路径；PATCH 时由路径参数覆盖。
   * @minLength 1
   */
  path?: string;
  /** 项目标识。 */
  project?: string;
  /** Recall 作用域。 */
  scope?:
    | "profile"
    | "global"
    | "project"
    | "device"
    | "agent"
    | "ops"
    | "inbox";
  /** Skill 标识。 */
  skill?: string;
  /** 信息来源。 */
  source?: string;
  /** 验证来源 Agent。 */
  source_agent?: string;
  /** 验证来源设备。 */
  source_device?: string;
  /** Recall 状态。 */
  status?:
    | "inbox"
    | "active"
    | "verified"
    | "stale"
    | "archived"
    | "rejected"
    | "conflicted"
    | "unverified"
    | "deprecated";
  /** Recall 标签。 */
  tags?: string[];
  /** 可选的 Recall 类型。 */
  type?: string;
  /** 验证运行 ID。 */
  verification_run_id?: string;
  /**
   * RFC 3339 UTC 时间。
   * @format date-time
   */
  verified_at?: string;
}

/** Stage 3 或向量服务的脱敏连接测试结果。 */
export interface RuntimeAIConnectionTestResponse {
  /**
   * 测试耗时毫秒。
   * @min 0
   */
  latency_ms: number;
  /** 脱敏后的测试结果说明。 */
  message: string;
  /** 测试使用的模型名称。 */
  model?: string;
  /** 连接测试是否成功。 */
  ok: boolean;
  /** 测试目标。 */
  target: "stage3" | "embedding";
}

/** 运行时 AI 设置响应。 */
export interface RuntimeAISettingsResponse {
  /** 请求是否成功。 */
  ok: boolean;
  /** Nexus 当前已脱敏的 AI 与向量检索配置。 */
  settings: RuntimeAISettingsView;
}

/** 保存 Nexus Stage 3 与向量检索运行时配置。 */
export interface RuntimeAISettingsUpdateRequest {
  /** Nexus Stage 3 外部模型运行时配置。 */
  stage3: Stage3SettingsInput;
  /** 向量检索与 Embedding 运行时配置。 */
  embedding: EmbeddingSettingsInput;
}

/** Nexus 当前已脱敏的 AI 与向量检索配置。 */
export interface RuntimeAISettingsView {
  /** 已脱敏的 Stage 3 外部模型运行时配置。 */
  stage3: Stage3SettingsView;
  /** 已脱敏的向量检索运行时配置。 */
  embedding: EmbeddingSettingsView;
  /** 是否已保存 SQLite 覆盖配置；false 表示当前来自环境变量或默认值。 */
  persisted: boolean;
  /**
   * 最近一次持久化更新时间。
   * @format date-time
   */
  updated_at?: string;
}

/** AgentDock 内存中最近一次运行调用的零 Payload 诊断视图。 */
export interface RuntimeDiagnosticCall {
  /**
   * 调用总耗时毫秒。
   * @min 0
   */
  duration_ms: number;
  /** 稳定错误分类。 */
  error_category?: string;
  /** 稳定错误码。 */
  error_code?: string;
  /** 节点进程内单调递增的调用 ID。 */
  id: string;
  /** 调用来源。 */
  source: "internal" | "mcp" | "nexus";
  /** 内部稳定阶段耗时。 */
  stages?: RuntimeDiagnosticStage[];
  /**
   * RFC 3339 UTC 时间。
   * @format date-time
   */
  started_at: string;
  /** 调用是否成功。 */
  success: boolean;
  /** 工具名称。 */
  tool: string;
  /** 存在上游 W3C Trace Context 时的 Trace ID。 */
  trace_id?: string;
}

/** 单次运行调用中的稳定阶段耗时。 */
export interface RuntimeDiagnosticStage {
  /**
   * 阶段耗时毫秒。
   * @min 0
   */
  duration_ms: number;
  /**
   * 稳定阶段名称；未知新阶段保持原值以支持前向兼容。
   * @minLength 1
   */
  name: string;
  /**
   * 相对调用开始时间的毫秒偏移。
   * @min 0
   */
  started_offset_ms: number;
  /** 阶段是否成功。 */
  success: boolean;
}

/** 指定在线 AgentDock 节点的最近运行调用；数据仅按需从节点内存读取。 */
export interface RuntimeDiagnosticsResponse {
  /**
   * 返回调用数量。
   * @min 0
   */
  count: number;
  /** 最近调用，按最新优先。 */
  items: RuntimeDiagnosticCall[];
  /** AgentDock 节点 ID。 */
  node_id: string;
  /** 请求是否成功。 */
  ok: boolean;
  /** 固定为 agentdock-runtime-api。 */
  source: string;
}

/** AgentDock Runtime 不可用或拒绝请求时的错误信封。 */
export interface RuntimeErrorEnvelope {
  /** 固定为 false，表示目标 Runtime 当前不可用。 */
  available: boolean;
  /** 稳定 Nexus 错误及可选的上游错误码。 */
  error: {
    /** AgentDock Runtime 返回的错误分类。 */
    category?: string;
    /** Nexus 稳定错误码。 */
    code: string;
    /** AgentDock Runtime 返回的结构化错误详情。 */
    details?: Record<string, any>;
    /** 可读错误说明。 */
    message: string;
    /** AgentDock Runtime 是否标记该错误可重试。 */
    retryable?: boolean;
    /** AgentDock Runtime 返回的原始错误码。 */
    upstream_code?: string;
  };
  /** 固定为 false。 */
  ok: boolean;
  /** 请求关联 ID。 */
  request_id: string;
  /** 错误来源，固定为 agentdock-runtime-api。 */
  source: string;
}

/** 运行时 AI 密钥更新指令；服务永不回显密钥明文。 */
export interface RuntimeSecretUpdate {
  /** 密钥更新动作：保留、替换或清空。 */
  action: "keep" | "replace" | "clear";
  /**
   * 仅 action=replace 时提交的新密钥。
   * @maxLength 65536
   */
  value?: string;
}

/** Nexus Stage 3 外部模型运行时配置。 */
export interface Stage3SettingsInput {
  /** 运行时 AI 密钥更新指令；服务永不回显密钥明文。 */
  api_key: RuntimeSecretUpdate;
  /** 是否启用 Stage 3 辅助进化。 */
  enabled: boolean;
  /** OpenAI 兼容 Chat Completions HTTP(S) 地址。 */
  endpoint: string;
  /**
   * Stage 3 执行间隔分钟数。
   * @min 60
   * @max 10080
   */
  interval_minutes: number;
  /** Stage 3 模型名称。 */
  model: string;
  /**
   * 模型请求超时秒数。
   * @min 1
   * @max 300
   */
  timeout_seconds: number;
}

/** 已脱敏的 Stage 3 外部模型运行时配置。 */
export interface Stage3SettingsView {
  /** 是否已配置 API Key；不返回明文。 */
  api_key_configured: boolean;
  /** Stage 3 是否具备运行所需的启用、地址和模型配置。 */
  configured: boolean;
  /** 是否启用 Stage 3。 */
  enabled: boolean;
  /** 当前模型地址。 */
  endpoint: string;
  /**
   * 执行间隔分钟数。
   * @min 60
   * @max 10080
   */
  interval_minutes: number;
  /** 当前模型名称。 */
  model: string;
  /**
   * 请求超时秒数。
   * @min 1
   * @max 300
   */
  timeout_seconds: number;
}

/** Nexus 系统与数据存储状态。 */
export interface SystemStatus {
  /** SQLite 健康状态。 */
  database: string;
  /** Nexus 系统状态目录。 */
  nexus_data_dir: string;
  /** 系统是否健康。 */
  ok: boolean;
  /** Recall Markdown 数据目录。 */
  recall_repo_dir: string;
  /** NexusDock 构建修订（git 提交号），未注入的本地构建为 unknown。 */
  revision: string;
  /**
   * 数据库 Schema 版本。
   * @min 0
   */
  schema_version: number;
  /** 服务名称，固定为 nexusdock。 */
  service: string;
  /** NexusDock 构建版本（git describe），未注入的本地构建为 dev。 */
  version: string;
}

/** 已脱敏的管理员浏览器会话。 */
export interface WebSession {
  /**
   * RFC 3339 UTC 时间。
   * @format date-time
   */
  absolute_expires_at: string;
  /**
   * RFC 3339 UTC 时间。
   * @format date-time
   */
  created_at: string;
  /** 仅当前会话返回的 CSRF Token。 */
  csrf_token?: string;
  /** 是否为当前浏览器会话。 */
  current?: boolean;
  /** 管理员显示名称。 */
  display_name: string;
  /** 会话 ID。 */
  id: string;
  /**
   * RFC 3339 UTC 时间。
   * @format date-time
   */
  idle_expires_at: string;
  /** 脱敏后的客户端网络前缀。 */
  ip_prefix: string;
  /**
   * RFC 3339 UTC 时间。
   * @format date-time
   */
  last_seen_at: string;
  /** 是否必须先更新管理员密码。 */
  must_change_password: boolean;
  /** 是否为记住登录会话。 */
  remember_me: boolean;
  /** 脱敏后的客户端摘要。 */
  user_agent_summary: string;
  /** 管理员用户 ID。 */
  user_id: string;
  /** 管理员用户名。 */
  username: string;
}

/** 管理员活动浏览器会话列表。 */
export interface WebSessionListResponse {
  /** 活动浏览器会话。 */
  items: WebSession[];
  /** 请求是否成功。 */
  ok: boolean;
}

/** 当前或新创建的管理员浏览器会话。 */
export interface WebSessionResponse {
  /** 请求是否成功。 */
  ok: boolean;
  /** 已脱敏的管理员浏览器会话。 */
  session: WebSession;
}

/** 撤销其他浏览器会话的结果。 */
export interface WebSessionRevokeOthersResponse {
  /** 请求是否成功。 */
  ok: boolean;
  /**
   * 已撤销的会话数量。
   * @min 0
   */
  revoked: number;
}
