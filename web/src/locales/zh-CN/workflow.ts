const workflow: Record<string, string> = {
  // Navigation & Drilldown
  'Template details': '模板详情',
  'Back to template list': '返回模板列表',
  'Historical versions': '历史版本',
  'Back to current version': '返回当前版本',
  'Back to historical versions': '返回历史版本',

  // Status & Meta
  'Current version': '当前版本',
  'Historical version': '历史版本',
  'Untitled template': '未命名模板',
  '{{count}} steps': '{{count}} 步',
  '{{count}} versions': '{{count}} 个版本',
  'Current ×{{count}}': '当前×{{count}}',
  'Version {{version}}': '版本 {{version}}',
  '{{count}} phases': '{{count}} 个阶段',
  'Historical versions {{count}}': '历史版本 {{count}}',

  // Errors & Notices
  'JSON must include id and version.': 'JSON 需要包含 id 和 version。',
  'Failed to parse JSON': 'JSON 解析失败',
  'Failed to load workflow templates': '工作流模板读取失败',
  'Failed to load template details': '模板详情读取失败',
  'Failed to load historical versions': '历史版本读取失败',
  'Failed to load historical version details': '历史版本详情读取失败',
  'Template path copied.': '模板路径已复制。',

  // List & Search
  'Search workflow templates': '搜索工作流模板',
  'Search title or keywords': '搜索标题或关键词',
  'workflow templates': '个工作流模板',
  'Loading workflow templates…': '正在读取工作流模板…',
  'No matching templates.': '没有匹配的模板。',

  // Detail & Empty State
  'Select template': '选择模板',
  'Select a template from the left to view execution steps.': '从左侧选择一个模板查看执行步骤。',
  'Viewing v{{version}}': '正在查看 v{{version}}',
  'Loading historical versions…': '正在读取历史版本…',
  'No historical versions yet': '还没有历史版本',
  'Older versions will appear here automatically when a new version is published.': '发布新版本后，旧版本会自动进入这里。',
  'No template description.': '暂无模板说明。',

  // Sections & Matching
  'Execution steps': '执行步骤',
  'View the main task flow by phase.': '按阶段查看任务主流程。',
  'No steps.': '没有步骤。',
  'Completion conditions': '完成条件',
  'Results that must be met before completing the task.': '任务结束前必须满足的结果。',
  'No completion conditions.': '没有完成条件。',
  'Matching and technical information': '匹配与技术信息',
  'Matching rules': '匹配规则',
  'Signals used by models to decide whether to use this template.': '模型用这些信号判断是否使用该模板。',
  'No matching rules.': '没有匹配规则。',
  'Template ID': '模板 ID',
  'File name': '文件名',
  'Version count': '版本数',
  'Parsable': '可解析',
  'Copy template path': '复制模板路径',
  'View raw runtime JSON': '查看 Runtime 原始 JSON',

  // Steps & Match Views
  'Unassigned phase': '未分阶段',
  'Required': '必需',
  'Depends on {{depends}}': '依赖 {{depends}}',
  'Keywords': '关键词',
  'Devices': '运行端',
  'Task types': '任务类型',
  'Projects': '项目',
  'Priority': '优先级',
};

export default workflow;
