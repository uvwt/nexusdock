import { formatTime } from './lib/time';
import { useEffect, useRef, useState, type ReactNode } from 'react';
import { useTranslation } from 'react-i18next';
import i18n from './i18n';
import {
  Activity, BrainCircuit, Cable, ChevronRight,
  CircleAlert, CirclePlus, Database, FileJson, Home, ListChecks, Menu, RefreshCw,
  Package, ServerCog, Settings, UserRound, Wrench, X,
} from 'lucide-react';
import RecallWorkspace from './RecallWorkspace';
import { type WebSession } from './Auth';
import AccountSecurity from './AccountSecurity';
import AISettingsPanel from './components/settings/AISettingsPanel';
import LanguageSettingsPanel from './components/settings/LanguageSettingsPanel';
import MCPAccessPanel from './components/settings/MCPAccessPanel';
import { ApiError, api, setCSRFToken } from './api/client';
import WorkflowTemplatesPage from './components/workflows/WorkflowTemplatesPage';
import { SkillsPage, TaskCenterPage } from './components/runtime/RuntimePages';
import MCPPage from './components/runtime/MCPPage';
import PluginPage from './components/runtime/PluginPage';
import {
  AgentDockNodeRequired,
  AgentDockNodeSelector,
  AgentDockNodesPanel,
  useAgentDockNodes,
} from './components/runtime/AgentDockNodes';
import './nexus.css';

type RuntimeSection = 'tasks' | 'skills' | 'plugins' | 'mcp';
type Section = 'home' | 'recall' | 'templates' | RuntimeSection | 'settings';
type SettingsSection = 'account' | 'mcp' | 'ai' | 'system';
type Tone = 'ok' | 'warn' | 'danger' | 'info' | 'muted';


type SystemStatus = {
  ok: boolean;
  service: string;
  version?: string;
  revision?: string;
  database: string;
  schema_version: number;
  nexus_data_dir?: string;
  recall_repo_dir?: string;
};

type RuntimeOverview = {
  ok: boolean;
  tasks?: { active_recent_24h?: number };
  skills?: { count?: number };
  mcp?: { count?: number };
  plugins?: { count?: number; available?: boolean };
};

type RuntimeNodeMetrics = {
  skills: number;
  mcp: number;
  plugins: number | null;
  activeRecent24h: number;
};

type RuntimeMetricsState = {
  data: Record<string, RuntimeNodeMetrics>;
  loading: boolean;
  errors: Record<string, string>;
};

type Resource<T> = { data: T; live: boolean; loading: boolean; error?: string };

type SectionMeta = { id: Section; label: string; icon: typeof Home; scope: string };
type RuntimeSectionMeta = { id: RuntimeSection; label: string; icon: typeof Home };
type NavGroup = { label: string; items: SectionMeta[] };

const RUNTIME_SECTIONS: RuntimeSectionMeta[] = [
  { id: 'tasks', label: 'Tasks', icon: ListChecks },
  { id: 'plugins', label: 'Plugin', icon: Package },
  { id: 'skills', label: 'Skill', icon: Wrench },
  { id: 'mcp', label: 'MCP', icon: Cable },
];

const NAV: SectionMeta[] = [
  { id: 'home', label: 'Overview', icon: Home, scope: 'workspace' },
  { id: 'recall', label: 'Recall', icon: Database, scope: 'workspace' },
  { id: 'templates', label: 'Workflow', icon: FileJson, scope: 'workspace' },
  ...RUNTIME_SECTIONS.map((item) => ({ ...item, scope: 'runtime' })),
  { id: 'settings', label: 'Settings', icon: Settings, scope: 'system' },
];

const NAV_GROUPS: NavGroup[] = [
  { label: 'Workspace', items: NAV.filter((item) => item.scope === 'workspace') },
  { label: 'Runtime', items: NAV.filter((item) => item.scope === 'runtime') },
  { label: 'System', items: NAV.filter((item) => item.scope === 'system') },
];

const SETTINGS_SECTIONS: Array<{ id: SettingsSection; label: string; description: string; icon: typeof Settings }> = [
  { id: 'account', label: 'Account & sessions', description: 'Sign-in, security, and active sessions', icon: UserRound },
  { id: 'mcp', label: 'MCP access', description: 'Client address and access token', icon: Cable },
  { id: 'ai', label: 'AI & vectors', description: 'Models, Embedding, and indexes', icon: BrainCircuit },
  { id: 'system', label: 'System & nodes', description: 'AgentDock, nodes, and system status', icon: ServerCog },
];

function sectionFromHash(): Section {
  const section = window.location.hash.replace(/^#\/?/, '').split('/')[0];
  if (NAV.some((item) => item.id === section)) return section as Section;

  const params = new URLSearchParams(window.location.search);
  if (params.has('tab') || params.has('path') || params.has('prefix') || params.has('q')) return 'recall';
  return 'home';
}

function settingsSectionFromHash(): SettingsSection {
  const [, subsection] = window.location.hash.replace(/^#\/?/, '').split('/');
  return SETTINGS_SECTIONS.some((item) => item.id === subsection) ? subsection as SettingsSection : 'account';
}

function unpackAPI<T>(body: unknown, fallback: T): T {
  const value = body as { data?: unknown; items?: unknown };
  if (value && typeof value === 'object' && 'data' in value) return (value.data ?? fallback) as T;
  if (value && typeof value === 'object' && 'items' in value) return (value.items ?? fallback) as T;
  return (body ?? fallback) as T;
}

function messageOf(error: unknown): string {
  if (error instanceof ApiError && error.status === 401) return i18n.t('Your login session has expired. Please sign in again.');
  if (error instanceof ApiError && error.status === 403) return i18n.t('This account does not have permission to access this resource.');
  return error instanceof Error ? error.message : i18n.t('Failed to load Nexus data');
}

function useResource<T>(path: string, fallback: T, refreshToken: number): Resource<T> {
  const fallbackRef = useRef(fallback);
  fallbackRef.current = fallback;
  const [state, setState] = useState<Resource<T>>({ data: fallback, live: false, loading: true });
  useEffect(() => {
    let cancelled = false;
    setState((current) => ({ ...current, loading: true }));
    api<unknown>(path).then((body) => {
      if (!cancelled) setState({ data: unpackAPI<T>(body, fallbackRef.current), live: true, loading: false });
    }).catch((error) => {
      if (!cancelled) setState({ data: fallbackRef.current, live: false, loading: false, error: messageOf(error) });
    });
    return () => { cancelled = true; };
  }, [path, refreshToken]);
  return state;
}



export default function App() {
  const { t } = useTranslation();
  const [section, setSection] = useState<Section>(sectionFromHash);
  const [menuOpen, setMenuOpen] = useState(false);
  const [refreshToken, setRefreshToken] = useState(0);
  const [sessionExpired, setSessionExpired] = useState(false);
  const [session, setSession] = useState<WebSession | null>(null);
  const runtimeNodes = useAgentDockNodes(refreshToken);

  useEffect(() => {
    let cancelled = false;
    api<{ ok: boolean; session: WebSession }>('/v1/auth/session').then((result) => {
      if (cancelled) return;
      setSession(result.session);
      if (result.session.csrf_token) setCSRFToken(result.session.csrf_token);
      if (result.session.must_change_password) {
        const returnTo = `${window.location.pathname}${window.location.search}${window.location.hash}`;
        window.location.replace(`/change-password?return_to=${encodeURIComponent(returnTo)}`);
      }
    }).catch((error) => {
      if (!cancelled && error instanceof ApiError && error.status === 401) setSessionExpired(true);
    });
    const expired = () => setSessionExpired(true);
    window.addEventListener('nexus:session-expired', expired);
    return () => {
      cancelled = true;
      window.removeEventListener('nexus:session-expired', expired);
    };
  }, []);

  useEffect(() => {
    const onHash = () => {
      setSection(sectionFromHash());
    };
    window.addEventListener('hashchange', onHash);
    return () => window.removeEventListener('hashchange', onHash);
  }, []);

  function navigate(next: Section) {
    window.location.hash = next;
    setSection(next);
    setMenuOpen(false);
  }

  const active = NAV.find((item) => item.id === section) ?? NAV[0];
  const sessionName = session?.display_name || session?.username || 'Admin';
  return (
    <div className="nexus-app">
      <aside id="nexus-primary-navigation" className={`nexus-sidebar ${menuOpen ? 'is-open' : ''}`}>
        <div className="nexus-brand">
          <span className="nexus-brand-mark" aria-hidden="true">N</span>
          <span><strong>Nexus</strong><small>AgentDock Console</small></span>
        </div>
        <nav aria-label={t('Primary navigation')}>
          {NAV_GROUPS.map((group) => <div className="nexus-nav-group" key={group.label}>
            <span className="nexus-nav-title">{group.label}</span>
            {group.items.map((item) => {
              const Icon = item.icon;
              return <button type="button" key={item.id} className={section === item.id ? 'active' : ''} aria-current={section === item.id ? 'page' : undefined} onClick={() => navigate(item.id)}><Icon size={18} /><span>{t(item.label)}</span></button>;
            })}
          </div>)}
        </nav>
      </aside>
      {menuOpen && <button type="button" className="nexus-scrim" aria-label={t('Close menu')} onClick={() => setMenuOpen(false)} />}
      <main className="nexus-main">
        <header className="nexus-topbar">
          <button type="button" className="nexus-mobile-menu" aria-label={t('Toggle menu')} aria-expanded={menuOpen} aria-controls="nexus-primary-navigation" onClick={() => setMenuOpen((value) => !value)}>{menuOpen ? <X /> : <Menu />}</button>
          <div><span className="nexus-eyebrow">Nexus / {active.scope}</span><h1>{t(active.label)}</h1></div>
          <div className="nexus-top-actions">
            <span className="nexus-environment"><i />{t('Running')}</span>
            <button type="button" className="icon-button" title={t('Refresh')} aria-label={t('Refresh current page')} onClick={() => setRefreshToken((value) => value + 1)}><RefreshCw size={17} /></button>
            <span className="nexus-session-user" title={session?.username || t('Administrator session')}><span className="nexus-avatar">{sessionName.charAt(0).toUpperCase()}</span><span>{sessionName}</span></span>
          </div>
        </header>
        <div className="nexus-content">
          {section === 'home' && <HomePage refreshToken={refreshToken} runtimeNodes={runtimeNodes} navigate={navigate} />}
          {section === 'recall' && <RecallWorkspace refreshToken={refreshToken} />}
          {section === 'templates' && <WorkflowTemplatesPage refreshToken={refreshToken} />}
          {isRuntimeSection(section) && <RuntimeContent active={section} refreshToken={refreshToken} runtimeNodes={runtimeNodes} />}
          {section === 'settings' && <SettingsPage refreshToken={refreshToken} runtimeNodes={runtimeNodes} />}
        </div>
      </main>
      {sessionExpired && <SessionExpiredDialog />}
    </div>
  );
}

function signInAgain() {
  const returnTo = `${window.location.pathname}${window.location.search}${window.location.hash}`;
  window.location.assign(`/login?return_to=${encodeURIComponent(returnTo)}`);
}

function SessionExpiredDialog() {
  const { t } = useTranslation();
  const dialogRef = useRef<HTMLDialogElement>(null);
  useEffect(() => {
    const dialog = dialogRef.current;
    if (dialog && !dialog.open) dialog.showModal();
  }, []);
  return <dialog ref={dialogRef} className="session-expired-overlay" aria-labelledby="session-expired-title"><section className="session-expired-dialog"><span><CircleAlert size={22} /></span><h2 id="session-expired-title">{t('Session expired')}</h2><p>{t('The current page is preserved. Failed write operations will not be retried automatically.')}</p><button type="button" onClick={signInAgain}>{t('Sign in again')}</button></section></dialog>;
}

type RuntimeNodesState = ReturnType<typeof useAgentDockNodes>;

function useRuntimeNodeMetrics(runtimeNodes: RuntimeNodesState, refreshToken: number): RuntimeMetricsState {
  const onlineNodeIDs = runtimeNodes.nodes.filter((node) => node.enabled && node.online).map((node) => node.id).sort();
  const onlineNodeKey = onlineNodeIDs.join('|');
  const [state, setState] = useState<RuntimeMetricsState>({ data: {}, loading: true, errors: {} });

  useEffect(() => {
    if (runtimeNodes.loading) {
      setState((current) => ({ ...current, loading: true, errors: {} }));
      return undefined;
    }
    if (onlineNodeIDs.length === 0) {
      setState({ data: {}, loading: false, errors: {} });
      return undefined;
    }

    let cancelled = false;
    setState((current) => ({ ...current, loading: true, errors: {} }));
    const nodeIDs = onlineNodeKey.split('|').filter(Boolean);
    Promise.all(nodeIDs.map(async (nodeID) => {
      try {
        const summary = await api<RuntimeOverview>(`/v1/runtime/nodes/${encodeURIComponent(nodeID)}/overview`);
        return { nodeID, summary };
      } catch (error) {
        return { nodeID, error: messageOf(error) };
      }
    })).then((results) => {
      if (cancelled) return;
      const data: Record<string, RuntimeNodeMetrics> = {};
      const errors: Record<string, string> = {};
      for (const result of results) {
        if ('summary' in result && result.summary) {
          data[result.nodeID] = {
            skills: result.summary.skills?.count || 0,
            mcp: result.summary.mcp?.count || 0,
            plugins: result.summary.plugins?.available === false ? null : (result.summary.plugins?.count ?? 0),
            activeRecent24h: result.summary.tasks?.active_recent_24h || 0,
          };
        } else if ('error' in result && result.error) {
          errors[result.nodeID] = result.error;
        }
      }
      setState({ data, loading: false, errors });
    });
    return () => { cancelled = true; };
  }, [onlineNodeKey, refreshToken, runtimeNodes.loading]);

  return state;
}

function HomePage({ refreshToken, runtimeNodes, navigate }: { refreshToken: number; runtimeNodes: RuntimeNodesState; navigate: (section: Section) => void }) {
  const { t } = useTranslation();
  const system = useResource<SystemStatus>('/v1/system/status', { ok: false, service: 'nexusdock', version: 'dev', revision: 'unknown', database: 'unknown', schema_version: 0, nexus_data_dir: '', recall_repo_dir: '' }, refreshToken);
  const runtimeMetrics = useRuntimeNodeMetrics(runtimeNodes, refreshToken);
  const enabledNodes = runtimeNodes.nodes.filter((node) => node.enabled);
  const onlineNodes = enabledNodes.filter((node) => node.online);
  const offlineNodes = enabledNodes.filter((node) => !node.online);
  const runtimeErrors = Object.entries(runtimeMetrics.errors).map(([nodeID, message]) => {
    const node = runtimeNodes.nodes.find((item) => item.id === nodeID);
    const displayMessage = message === t('Request failed') || message === 'Request failed'
      ? t('Request failed. Check whether AgentDock and NexusDock need to be updated.')
      : message;
    return `${node?.name || nodeID}: ${displayMessage}`;
  });
  const serviceErrors = [system.error, runtimeNodes.error].filter(Boolean) as string[];
  const errors = [...serviceErrors, ...runtimeErrors];
  const systemTone = system.data.ok ? 'ok' : 'danger';
  const nodesTone: Tone = runtimeNodes.loading ? 'muted' : offlineNodes.length > 0 ? 'danger' : enabledNodes.length > 0 ? 'ok' : 'muted';
  const nodeSummary = runtimeNodes.loading ? t('Loading') : enabledNodes.length > 0 ? t('{{online}}/{{total}} online', { online: onlineNodes.length, total: enabledNodes.length }) : t('No nodes');
  const databaseAbnormal = system.live && !system.data.ok;
  const needsAttention = databaseAbnormal || offlineNodes.length > 0 || errors.length > 0;
  // 节点离线或单节点概览读取失败不代表 Nexus 核心服务异常，顶部状态只反映中心服务本身。
  const coreNeedsAttention = databaseAbnormal || serviceErrors.length > 0;

  return <>
    <section className="nexus-overview-strip">
      <div><span className="nexus-kicker">{t('System overview')}</span><h2>{coreNeedsAttention ? t('Items need attention') : t('Running normally')}</h2></div>
      <div className="nexus-overview-status"><StatusBadge tone={systemTone}>Nexus</StatusBadge><StatusBadge tone={nodesTone}>{t('Nodes {{summary}}', { summary: nodeSummary })}</StatusBadge></div>
    </section>
    {serviceErrors.length > 0 && <InlineAlert tone="danger" title={t('Some data could not be loaded')} message={serviceErrors.join('; ')} />}

    <NodeOverview runtimeNodes={runtimeNodes} runtimeMetrics={runtimeMetrics} />

    {needsAttention && <Panel className="dashboard-attention-panel" icon={CircleAlert} title={t('Needs attention')} subtitle={t('Only issues that affect normal use are shown')}>
      {databaseAbnormal && <button type="button" className="attention-row" onClick={() => navigate('settings')}><StatusBadge tone="danger">{t('Abnormal')}</StatusBadge><span><strong>{t('Database abnormal')}</strong><small>{system.data.database || 'unknown'}</small></span><ChevronRight size={16} /></button>}
      {offlineNodes.map((node) => <button type="button" className="attention-row" key={node.id} onClick={() => { window.location.hash = 'settings/system'; }}><StatusBadge tone="danger">{t('Offline')}</StatusBadge><span><strong>{node.name}</strong></span><small className="attention-row-time">{formatTime(node.last_seen_at, { compact: true })}</small><ChevronRight size={16} /></button>)}
      {errors.map((message) => <div className="nx-alert is-error" key={message}>{message}</div>)}
    </Panel>}
  </>;
}

function NodeOverview({ runtimeNodes, runtimeMetrics }: { runtimeNodes: RuntimeNodesState; runtimeMetrics: RuntimeMetricsState }) {
  const { t } = useTranslation();
  const onlineCount = runtimeNodes.nodes.filter((node) => node.enabled && node.online).length;
  const orderedNodes = [
    ...runtimeNodes.nodes.filter((node) => node.enabled),
    ...runtimeNodes.nodes.filter((node) => !node.enabled),
  ];
  return <section className="dashboard-node-section">
    <header>
      <div className="dashboard-node-heading"><span className="nexus-panel-icon"><ServerCog size={17} /></span><div><h3>{t('AgentDock nodes')}</h3><p>{runtimeNodes.loading ? t('Loading node status…') : t('{{count}} nodes · {{online}} online', { count: runtimeNodes.nodes.length, online: onlineCount })}</p></div></div>
      <button type="button" className="nx-button is-secondary is-small" onClick={() => { window.location.hash = 'settings/system'; }}>{t('Manage nodes')}</button>
    </header>
    <div className="dashboard-node-list">
      {runtimeNodes.loading && runtimeNodes.nodes.length === 0 && <EmptyMini text={t('Loading AgentDock nodes…')} />}
      {!runtimeNodes.loading && runtimeNodes.nodes.length === 0 && <EmptyMini text={t('No AgentDock nodes have been paired.')} />}
      {orderedNodes.map((node) => {
        const statusTone: Tone = !node.enabled ? 'muted' : node.online ? 'ok' : 'danger';
        const statusLabel = !node.enabled ? t('Node disabled') : node.online ? t('Online') : t('Offline');
        const metrics = runtimeMetrics.data[node.id];
        const metricValue = (value?: number | null) => {
          if (!node.enabled || !node.online) return '—';
          if (runtimeMetrics.loading && !metrics) return t('Loading');
          if (!metrics || value == null) return '—';
          return String(value);
        };
        return <article className="dashboard-node-row" key={node.id}>
          <div className="dashboard-node-identity">
            <span className="dashboard-node-icon"><ServerCog size={17} /></span>
            <span><strong>{node.name}</strong><small>{nodePlatformLabel(node.os, node.arch)}</small></span>
          </div>
          <div className="dashboard-node-meta">
            <span><small>AgentDock</small><strong>{node.version ? `v${node.version}` : t('Unknown')}</strong></span>
            <span><small>Skill</small><strong>{metricValue(metrics?.skills)}</strong></span>
            <span><small>MCP</small><strong>{metricValue(metrics?.mcp)}</strong></span>
            <span><small>Plugin</small><strong>{metricValue(metrics?.plugins)}</strong></span>
            <span><small>{t('In progress (24h)')}</small><strong>{metricValue(metrics?.activeRecent24h)}</strong></span>
            <span><small>{t('Tools')}</small><strong>{t('{{count}} tools', { count: node.capabilities?.length || 0 })}</strong></span>
            <span><small>{t('Last online')}</small><strong>{formatTime(node.last_seen_at, { compact: true })}</strong></span>
          </div>
          <div className="dashboard-node-badges"><StatusBadge tone={statusTone}>{statusLabel}</StatusBadge></div>
        </article>;
      })}
    </div>
  </section>;
}

function nodePlatformLabel(os?: string, arch?: string): string {
  const osLabel = os === 'darwin' ? 'macOS' : os === 'windows' ? 'Windows' : os === 'linux' ? 'Linux' : os || '';
  return [osLabel, arch].filter(Boolean).join(' / ') || i18n.t('Waiting for first connection');
}

function isRuntimeSection(section: Section): section is RuntimeSection {
  return RUNTIME_SECTIONS.some((item) => item.id === section);
}

function RuntimeContent({ active, refreshToken, runtimeNodes }: {
  active: RuntimeSection;
  refreshToken: number;
  runtimeNodes: RuntimeNodesState;
}) {
  const { t } = useTranslation();
  const [mcpAddOpen, setMCPAddOpen] = useState(false);

  useEffect(() => {
    setMCPAddOpen(false);
  }, [active, runtimeNodes.selectedNodeID]);

  return <section className={`runtime-standalone-page runtime-${active}-page`}>
    <div className="runtime-node-bar">
      <AgentDockNodeSelector nodes={runtimeNodes.nodes} selectedNodeID={runtimeNodes.selectedNodeID} onSelect={runtimeNodes.selectNode} />
      {runtimeNodes.selectedNode && <span className={`runtime-node-status ${runtimeNodes.selectedNode.online ? 'is-online' : 'is-offline'}`}>{runtimeNodes.selectedNode.online ? t('Online') : t('Offline')}{runtimeNodes.selectedNode.os ? ` · ${runtimeNodes.selectedNode.os}/${runtimeNodes.selectedNode.arch}` : ''}</span>}
      {active === 'mcp' && runtimeNodes.selectedNode && <button type="button" className="nx-button runtime-node-action" onClick={() => setMCPAddOpen(true)}><CirclePlus size={16} />{t('Add MCP')}</button>}
    </div>
    {!runtimeNodes.selectedNode && <AgentDockNodeRequired><button type="button" className="nx-button" onClick={() => { window.location.hash = 'settings/system'; }}>{t('Manage nodes')}</button></AgentDockNodeRequired>}
    {active === 'tasks' && runtimeNodes.selectedNode && <TaskCenterPage key={runtimeNodes.selectedNode.id} nodeID={runtimeNodes.selectedNode.id} refreshToken={refreshToken} />}
    {active === 'skills' && runtimeNodes.selectedNode && <SkillsPage key={runtimeNodes.selectedNode.id} nodeID={runtimeNodes.selectedNode.id} refreshToken={refreshToken} />}
    {active === 'plugins' && runtimeNodes.selectedNode && <PluginPage key={runtimeNodes.selectedNode.id} nodeID={runtimeNodes.selectedNode.id} refreshToken={refreshToken} />}
    {active === 'mcp' && runtimeNodes.selectedNode && <MCPPage key={runtimeNodes.selectedNode.id} nodeID={runtimeNodes.selectedNode.id} refreshToken={refreshToken} addOpen={mcpAddOpen} onAddOpenChange={setMCPAddOpen} />}
  </section>;
}

function SettingsPage({ refreshToken, runtimeNodes }: { refreshToken: number; runtimeNodes: RuntimeNodesState }) {
  const { t } = useTranslation();
  const [active, setActive] = useState<SettingsSection>(settingsSectionFromHash);

  useEffect(() => {
    const onHash = () => setActive(settingsSectionFromHash());
    window.addEventListener('hashchange', onHash);
    return () => window.removeEventListener('hashchange', onHash);
  }, []);

  function navigate(next: SettingsSection) {
    window.location.hash = `settings/${next}`;
    setActive(next);
  }

  return <section className="settings-page">
    <nav className="settings-subnav" aria-label={t('Settings categories')}>
      {SETTINGS_SECTIONS.map((item) => {
        const Icon = item.icon;
        return <button key={item.id} type="button" className={active === item.id ? 'is-active' : ''} aria-current={active === item.id ? 'page' : undefined} onClick={() => navigate(item.id)}>
          <span className="settings-subnav-icon"><Icon size={17} /></span>
          <span><strong>{t(item.label)}</strong><small>{t(item.description)}</small></span>
        </button>;
      })}
    </nav>
    <div className="settings-content">
      {active === 'account' && <div className="account-settings-stack"><LanguageSettingsPanel /><AccountSecurity refreshToken={refreshToken} /></div>}
      {active === 'mcp' && <MCPAccessPanel refreshToken={refreshToken} />}
      {active === 'ai' && <AISettingsPanel refreshToken={refreshToken} />}
      {active === 'system' && <SystemSettingsPage refreshToken={refreshToken} runtimeNodes={runtimeNodes} />}
    </div>
  </section>;
}

function SystemSettingsPage({ refreshToken, runtimeNodes }: { refreshToken: number; runtimeNodes: RuntimeNodesState }) {
  const { t } = useTranslation();
  const system = useResource<SystemStatus>('/v1/system/status', { ok: false, service: 'nexusdock', version: 'dev', revision: 'unknown', database: 'unknown', schema_version: 0, nexus_data_dir: '', recall_repo_dir: '' }, refreshToken);

  return <section className="system-settings-page">
    <AgentDockNodesPanel
      nodes={runtimeNodes.nodes}
      selectedNodeID={runtimeNodes.selectedNodeID}
      loading={runtimeNodes.loading}
      error={runtimeNodes.error}
      onReload={runtimeNodes.reload}
      onSelect={runtimeNodes.selectNode}
    />
    <section className="settings-grid settings-system-grid">
      <Panel icon={Activity} title={t('System')} subtitle={t('Runtime status and data locations')}>
        <SettingValue label={t('Service')} value={system.data.service || 'nexusdock'} tone={system.data.ok ? 'ok' : 'danger'} />
        <SettingValue label={t('Database')} value={system.data.database || 'unknown'} tone={system.data.database === 'ok' ? 'ok' : 'danger'} />
        <section className="nexus-technical-details"><SettingValue label={t('Version')} value={system.data.version || 'dev'} mono /><SettingValue label={t('Revision')} value={system.data.revision || 'unknown'} mono /><SettingValue label="Schema" value={String(system.data.schema_version || 0)} /><SettingValue label={t('Nexus data')} value={system.data.nexus_data_dir || t('None')} mono /><SettingValue label={t('Recall repository')} value={system.data.recall_repo_dir || t('None')} mono /></section>
      </Panel>
    </section>
  </section>;
}

function Panel({ title, subtitle, icon: Icon, className = '', children }: { title: string; subtitle: string; icon?: typeof Home; className?: string; children: ReactNode }) { return <article className={`nexus-panel ${className}`.trim()}><header>{Icon && <span className="nexus-panel-icon"><Icon size={17} /></span>}<div><h3>{title}</h3><p>{subtitle}</p></div></header><div className="panel-body">{children}</div></article>; }
function StatusBadge({ tone, children }: { tone: Tone; children: ReactNode }) { return <span className={`status-badge tone-${tone}`}><span />{children}</span>; }
function InlineAlert({ tone, title, message }: { tone: Tone; title: string; message: string }) { return <div className={`nexus-inline-alert tone-${tone}`}><strong>{title}</strong><span>{message}</span></div>; }
function EmptyMini({ text }: { text: string }) { return <p className="empty-mini">{text}</p>; }
function SettingValue({ label, value, tone = 'muted', mono = false }: { label: string; value: string; tone?: Tone; mono?: boolean }) { return <div className="setting-value"><span>{label}</span><div>{tone !== 'muted' && <StatusBadge tone={tone}>{value}</StatusBadge>}{tone === 'muted' && <strong className={mono ? 'nx-mono' : ''}>{value}</strong>}</div></div>; }
