const translations = {
  en: {
    'nav.architecture': 'Architecture', 'nav.features': 'Features', 'nav.deploy': 'Deploy', 'nav.start': 'Get started',
    'hero.eyebrow': 'Self-hosted · Open source · Built for AgentDock',
    'hero.title': 'One control plane.<br><span>Every AgentDock.</span>',
    'hero.lead': 'Connect your Macs, Windows PCs, servers and containers to one private control center. Share Recall and Workflows, inspect live Runtime state, and expose one MCP endpoint to your AI clients.',
    'hero.start': 'Start self-hosting', 'hero.github': 'View on GitHub', 'hero.proof1': 'One MCP endpoint', 'hero.proof2': 'Outbound-only nodes',
    'hero.proof3': 'Your data, your infrastructure', 'hero.caption': 'AgentDock nodes connect out. No inbound port required.',
    'signals.fleet': 'Fleet control', 'signals.recall': 'Shared Recall', 'signals.workflow': 'Reusable Workflow', 'signals.runtime': 'Live Runtime', 'signals.mcp': 'Unified MCP',
    'architecture.title': 'Your devices stay local.<br><span>Your control plane stays yours.</span>',
    'architecture.lead': 'NexusDock adds a shared layer above AgentDock without replacing the execution layer on each machine.',
    'architecture.outbound': 'Outbound WebSocket connections', 'architecture.privateTitle': 'Private by design',
    'architecture.privateText': 'Nodes initiate the connection. You do not need to expose every AgentDock machine to the public internet.',
    'architecture.localTitle': 'Execution stays local',
    'architecture.localText': 'Files, commands, browser automation and other machine-local capabilities remain on their AgentDock node.',
    'features.title': 'One place to connect.<br><span>Nothing extra to operate.</span>',
    'features.fleetTitle': 'Fleet overview', 'features.fleetText': 'See every paired AgentDock node, its connection state, version and capabilities from a single console.',
    'features.recallTitle': 'Shared Recall', 'features.recallText': 'Centralize searchable Markdown memory, experience cards and private notes across your devices.',
    'features.workflowTitle': 'Reusable Workflows', 'features.workflowText': 'Publish, version, match and reuse proven workflows instead of rebuilding the same operational steps.',
    'features.runtimeTitle': 'Live Runtime', 'features.runtimeText': 'Inspect node-local Tasks, Skills, Plugins, MCP servers and diagnostics without duplicating runtime state.',
    'features.mcpTitle': 'One MCP endpoint', 'features.mcpText': 'Give AI clients one MCP address for NexusDock tools and capabilities routed to the right AgentDock node.',
    'deploy.title': 'From zero to your own Nexus.<br><span>Three steps.</span>', 'deploy.lead': 'NexusDock ships as a multi-architecture container for Linux AMD64 and ARM64.',
    'deploy.step1Title': 'Start NexusDock', 'deploy.step1Text': 'Run the official container with persistent volumes for Nexus state and Recall.',
    'deploy.step2Title': 'Pair your devices', 'deploy.step2Text': 'Generate a short-lived pairing code, then connect each AgentDock node.',
    'deploy.step3Title': 'Connect your AI client', 'deploy.step3Text': 'Point ChatGPT, Claude, Codex or another MCP client at your single NexusDock endpoint.',
    'deploy.copy': 'Copy', 'deploy.copied': 'Copied',
    'closing.title': 'Your agents already work everywhere.<br><span>Give them one home.</span>',
    'closing.text': 'Self-host NexusDock, keep control of your infrastructure, and connect every AgentDock through one shared layer.',
    'closing.github': 'Explore on GitHub', 'closing.docker': 'Docker Hub',
    'footer.tagline': 'The self-hosted control center for your AgentDock fleet.'
  },
  zh: {
    'nav.architecture': '架构', 'nav.features': '能力', 'nav.deploy': '部署', 'nav.start': '开始部署',
    'hero.eyebrow': '自托管 · 开源 · 为 AgentDock 而生',
    'hero.title': '一个控制中心。<br><span>连接所有 AgentDock。</span>',
    'hero.lead': '把 Mac、Windows、服务器和容器里的 AgentDock 汇聚到一个私有控制中心。共享 Recall 与 Workflow，查看实时 Runtime，并只向 AI 客户端提供一个 MCP 入口。',
    'hero.start': '开始自托管', 'hero.github': '查看 GitHub', 'hero.proof1': '一个 MCP 入口', 'hero.proof2': '节点只需主动出站',
    'hero.proof3': '你的数据，你的基础设施', 'hero.caption': 'AgentDock 主动连接，无需开放节点入站端口。',
    'signals.fleet': '多设备管理', 'signals.recall': '共享 Recall', 'signals.workflow': '复用 Workflow', 'signals.runtime': '实时 Runtime', 'signals.mcp': '统一 MCP',
    'architecture.title': '设备留在本地。<br><span>控制权也留在你手里。</span>',
    'architecture.lead': 'NexusDock 在 AgentDock 之上增加共享层，但不替代每台设备自己的执行层。',
    'architecture.outbound': 'AgentDock 主动建立 WebSocket 连接', 'architecture.privateTitle': '默认更私有',
    'architecture.privateText': '连接由节点主动发起，你不需要把每台 AgentDock 机器直接暴露到公网。',
    'architecture.localTitle': '执行仍在本机', 'architecture.localText': '文件、命令、浏览器自动化等本机能力仍由对应 AgentDock 节点负责。',
    'features.title': '一个地方统一连接。<br><span>不再多维护一套执行层。</span>',
    'features.fleetTitle': '设备总览', 'features.fleetText': '在一个控制台查看全部已配对 AgentDock 的连接状态、版本和能力。',
    'features.recallTitle': '共享 Recall', 'features.recallText': '跨设备集中保存和搜索 Markdown 记忆、经验卡片与私密笔记。',
    'features.workflowTitle': '复用 Workflow', 'features.workflowText': '集中发布、版本化、匹配和复用已经验证过的工作流，不必重复搭建步骤。',
    'features.runtimeTitle': '实时 Runtime', 'features.runtimeText': '查看节点本机的 Task、Skill、Plugin、MCP 与诊断状态，不复制第二套运行时数据。',
    'features.mcpTitle': '一个 MCP 入口', 'features.mcpText': '让 AI 客户端只连接一个 NexusDock MCP 地址，再把能力路由到正确的 AgentDock 节点。',
    'deploy.title': '从零到自己的 Nexus。<br><span>只需三步。</span>', 'deploy.lead': 'NexusDock 提供 Linux AMD64 与 ARM64 多架构容器镜像。',
    'deploy.step1Title': '启动 NexusDock', 'deploy.step1Text': '运行官方容器，并为 Nexus 状态与 Recall 挂载持久化存储。',
    'deploy.step2Title': '配对你的设备', 'deploy.step2Text': '生成短时配对码，然后让每台 AgentDock 主动连接到 NexusDock。',
    'deploy.step3Title': '连接 AI 客户端', 'deploy.step3Text': '把 ChatGPT、Claude、Codex 或其他 MCP 客户端指向唯一的 NexusDock 地址。',
    'deploy.copy': '复制', 'deploy.copied': '已复制',
    'closing.title': '你的 Agent 已经遍布设备。<br><span>现在给它们一个共同的家。</span>',
    'closing.text': '自托管 NexusDock，把基础设施掌握在自己手中，用一个共享层连接所有 AgentDock。',
    'closing.github': '前往 GitHub', 'closing.docker': 'Docker Hub',
    'footer.tagline': '面向多台 AgentDock 的自托管中心端。'
  }
};

const root = document.documentElement;
const languageButton = document.querySelector('[data-language]');
const menuButton = document.querySelector('[data-menu]');
const mobileNav = document.querySelector('[data-mobile-nav]');
const copyButton = document.querySelector('[data-copy]');
const composeBlock = document.querySelector('[data-compose]');
const header = document.querySelector('[data-header]');
const preferredLanguage = localStorage.getItem('nexusdock-site-language') || (navigator.language.toLowerCase().startsWith('zh') ? 'zh' : 'en');
let language = translations[preferredLanguage] ? preferredLanguage : 'en';

function applyLanguage(nextLanguage) {
  language = nextLanguage;
  root.lang = language === 'zh' ? 'zh-CN' : 'en';
  document.querySelectorAll('[data-i18n]').forEach((element) => {
    const value = translations[language][element.dataset.i18n];
    if (value) element.textContent = value;
  });
  document.querySelectorAll('[data-i18n-html]').forEach((element) => {
    const value = translations[language][element.dataset.i18nHtml];
    if (value) element.innerHTML = value;
  });
  languageButton.textContent = language === 'en' ? '中文' : 'EN';
  languageButton.setAttribute('aria-label', language === 'en' ? '切换到中文' : 'Switch to English');
  document.title = language === 'zh' ? 'NexusDock — 一个控制中心，连接所有 AgentDock' : 'NexusDock — One control plane for every AgentDock';
  localStorage.setItem('nexusdock-site-language', language);
}

function setMenu(open) {
  mobileNav.classList.toggle('is-open', open);
  menuButton.classList.toggle('is-open', open);
  menuButton.setAttribute('aria-expanded', String(open));
  menuButton.setAttribute('aria-label', open ? 'Close menu' : 'Open menu');
}

languageButton.addEventListener('click', () => applyLanguage(language === 'en' ? 'zh' : 'en'));
menuButton.addEventListener('click', () => setMenu(!mobileNav.classList.contains('is-open')));
mobileNav.querySelectorAll('a').forEach((link) => link.addEventListener('click', () => setMenu(false)));

copyButton.addEventListener('click', async () => {
  try {
    await navigator.clipboard.writeText(composeBlock.textContent.trim());
    copyButton.textContent = translations[language]['deploy.copied'];
    window.setTimeout(() => { copyButton.textContent = translations[language]['deploy.copy']; }, 1600);
  } catch {
    const selection = window.getSelection();
    const range = document.createRange();
    range.selectNodeContents(composeBlock);
    selection.removeAllRanges();
    selection.addRange(range);
  }
});

const onScroll = () => header.classList.toggle('is-scrolled', window.scrollY > 12);
window.addEventListener('scroll', onScroll, { passive: true });
onScroll();
applyLanguage(language);
