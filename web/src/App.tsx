import { formatTime, timeZoneLabel } from './lib/time';
import { useEffect, useRef, useState, type ReactNode } from 'react';
import {
  Activity, Cable, ChevronRight,
  CircleAlert, Database, FileJson, Home, ListChecks, Menu, RefreshCw,
  Settings, ShieldCheck, Sparkles, Wrench, X,
} from 'lucide-react';
import RecallWorkspace from './RecallWorkspace';
import { type WebSession } from './Auth';
import AccountSecurity from './AccountSecurity';
import { ApiError, api, setCSRFToken } from './api/client';
import WorkflowTemplatesPage from './components/workflows/WorkflowTemplatesPage';
import { SkillsPage, TaskCenterPage } from './components/runtime/RuntimePages';
import MCPPage from './components/runtime/MCPPage';
import './nexus.css';

type RuntimeSection = 'tasks' | 'skills' | 'templates' | 'mcp';
type Section = 'home' | 'recall' | RuntimeSection | 'settings';
type Tone = 'ok' | 'warn' | 'danger' | 'muted';


type BackupHistory = {
  state: string;
  message?: string;
  started_at?: string;
  completed_at?: string;
  archive?: string;
  archive_size?: number;
  sha256?: string;
  remote_path?: string;
};

type BackupStatus = {
  id: string;
  title: string;
  description?: string;
  provider: string;
  host: string;
  enabled: boolean;
  schedule: string;
  state: string;
  last_started_at?: string;
  last_completed_at?: string;
  next_run_at?: string;
  message?: string;
  archive?: string;
  archive_size?: number;
  sha256?: string;
  remote_path?: string;
  history?: BackupHistory[];
};

type SystemStatus = {
  ok: boolean;
  service: string;
  database: string;
  schema_version: number;
  nexus_data_dir?: string;
  recall_repo_dir?: string;
};

type Resource<T> = { data: T; live: boolean; loading: boolean; error?: string };

type SectionMeta = { id: Section; label: string; icon: typeof Home; scope: string };
type RuntimeSectionMeta = { id: RuntimeSection; label: string; icon: typeof Home };
type NavGroup = { label: string; items: SectionMeta[] };

const RUNTIME_SECTIONS: RuntimeSectionMeta[] = [
  { id: 'tasks', label: '任务', icon: ListChecks },
  { id: 'skills', label: 'Skill', icon: Wrench },
  { id: 'templates', label: '模板', icon: FileJson },
  { id: 'mcp', label: 'MCP', icon: Cable },
];

const NAV: SectionMeta[] = [
  { id: 'home', label: '总览', icon: Home, scope: 'workspace' },
  { id: 'recall', label: 'Recall', icon: Database, scope: 'workspace' },
  ...RUNTIME_SECTIONS.map((item) => ({ ...item, scope: 'runtime' })),
  { id: 'settings', label: '设置', icon: Settings, scope: 'system' },
];

const NAV_GROUPS: NavGroup[] = [
  { label: 'Workspace', items: NAV.filter((item) => item.scope === 'workspace') },
  { label: 'Runtime', items: NAV.filter((item) => item.scope === 'runtime') },
  { label: 'System', items: NAV.filter((item) => item.scope === 'system') },
];

const LEGACY_RUNTIME_SECTIONS: Record<string, RuntimeSection> = {
  tasks: 'tasks',
  cleanup: 'tasks',
  skills: 'skills',
  templates: 'templates',
  mcp: 'mcp',
};

function hashParts(): string[] {
  return window.location.hash.replace(/^#\/?/, '').split('/').filter(Boolean);
}

function sectionFromHash(): Section {
  const [first, second] = hashParts();
  if (first === 'runtime') return LEGACY_RUNTIME_SECTIONS[second] || 'tasks';
  if (LEGACY_RUNTIME_SECTIONS[first]) return LEGACY_RUNTIME_SECTIONS[first];
  if (NAV.some((item) => item.id === first)) return first as Section;
  const params = new URLSearchParams(window.location.search);
  if (params.has('tab') || params.has('path') || params.has('prefix') || params.has('q')) return 'recall';
  return 'home';
}

function unpackAPI<T>(body: unknown, fallback: T): T {
  const value = body as { data?: unknown; items?: unknown };
  if (value && typeof value === 'object' && 'data' in value) return (value.data ?? fallback) as T;
  if (value && typeof value === 'object' && 'items' in value) return (value.items ?? fallback) as T;
  return (body ?? fallback) as T;
}

function messageOf(error: unknown): string {
  if (error instanceof ApiError && error.status === 401) return '登录会话已失效，请重新登录。';
  if (error instanceof ApiError && error.status === 403) return '当前账号没有访问权限。';
  return error instanceof Error ? error.message : '读取 Nexus 数据失败';
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



function formatBytes(value?: number): string {
  if (value === undefined || value < 0) return '暂无';
  const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB'];
  let size = value;
  let unit = 0;
  while (size >= 1024 && unit < units.length - 1) {
    size /= 1024;
    unit += 1;
  }
  return `${size.toFixed(unit === 0 ? 0 : 2)} ${units[unit]}`;
}

function toneForStatus(status?: string): Tone {
  if (!status) return 'muted';
  if (['online', 'healthy', 'success', 'succeeded', 'completed', 'ready', 'mounted', 'listed'].includes(status)) return 'ok';
  if (['failed', 'offline', 'blocked', 'revoked', 'expired'].includes(status)) return 'danger';
  if (['degraded', 'pending', 'running', 'queued', 'uploading', 'downloading', 'listing'].includes(status)) return 'warn';
  return 'muted';
}

export default function App() {
  const [section, setSection] = useState<Section>(sectionFromHash);
  const [menuOpen, setMenuOpen] = useState(false);
  const [refreshToken, setRefreshToken] = useState(0);
  const [sessionExpired, setSessionExpired] = useState(false);
  const [session, setSession] = useState<WebSession | null>(null);

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
          <span className="nexus-brand-mark"><Sparkles size={19} /></span>
          <span><strong>Nexus</strong><small>AgentDock Console</small></span>
        </div>
        <nav aria-label="主导航">
          {NAV_GROUPS.map((group) => <div className="nexus-nav-group" key={group.label}>
            <span className="nexus-nav-title">{group.label}</span>
            {group.items.map((item) => {
              const Icon = item.icon;
              return <button type="button" key={item.id} className={section === item.id ? 'active' : ''} aria-current={section === item.id ? 'page' : undefined} onClick={() => navigate(item.id)}><Icon size={18} /><span>{item.label}</span></button>;
            })}
          </div>)}
        </nav>
        <div className="nexus-sidebar-foot"><ShieldCheck size={16} /><span><strong>Private workspace</strong><small>Local-first console</small></span></div>
      </aside>
      {menuOpen && <button type="button" className="nexus-scrim" aria-label="关闭菜单" onClick={() => setMenuOpen(false)} />}
      <main className="nexus-main">
        <header className="nexus-topbar">
          <button type="button" className="nexus-mobile-menu" aria-label="切换菜单" aria-expanded={menuOpen} aria-controls="nexus-primary-navigation" onClick={() => setMenuOpen((value) => !value)}>{menuOpen ? <X /> : <Menu />}</button>
          <div><span className="nexus-eyebrow">Nexus / {active.scope}</span><h1>{active.label}</h1></div>
          <div className="nexus-top-actions">
            <span className="nexus-environment"><i />运行中</span>
            <button type="button" className="icon-button" title="刷新" aria-label="刷新当前页面" onClick={() => setRefreshToken((value) => value + 1)}><RefreshCw size={17} /></button>
            <span className="nexus-session-user" title={session?.username || '管理员会话'}><span className="nexus-avatar">{sessionName.charAt(0).toUpperCase()}</span><span>{sessionName}</span></span>
          </div>
        </header>
        <div className={`nexus-content nexus-section-${section}`}>
          {section === 'home' && <HomePage refreshToken={refreshToken} navigate={navigate} />}
          {section === 'recall' && <RecallWorkspace refreshToken={refreshToken} />}
          {isRuntimeSection(section) && <RuntimeContent active={section} refreshToken={refreshToken} />}
          {section === 'settings' && <SettingsPage refreshToken={refreshToken} />}
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
  const dialogRef = useRef<HTMLDialogElement>(null);
  useEffect(() => {
    const dialog = dialogRef.current;
    if (dialog && !dialog.open) dialog.showModal();
  }, []);
  return <dialog ref={dialogRef} className="session-expired-overlay" aria-labelledby="session-expired-title"><section className="session-expired-dialog"><span><CircleAlert size={22} /></span><h2 id="session-expired-title">会话已过期</h2><p>当前页面保持不变，失败的写操作不会自动重试。</p><button type="button" onClick={signInAgain}>重新登录</button></section></dialog>;
}

function HomePage({ refreshToken, navigate }: { refreshToken: number; navigate: (section: Section) => void }) {
  const system = useResource<SystemStatus>('/v1/system/status', { ok: false, service: 'nexusdock', database: 'unknown', schema_version: 0, nexus_data_dir: '', recall_repo_dir: '' }, refreshToken);
  const backupResource = useResource<BackupStatus | undefined>('/v1/backup/status', undefined, refreshToken);
  const backup = backupResource.data;
  const errors = [system.error, backupResource.error].filter(Boolean) as string[];
  const systemTone = system.data.ok ? 'ok' : 'danger';
  const needsAttention = !system.data.ok || backup?.state === 'failed' || errors.length > 0;

  return <>
    <section className="nexus-overview-strip">
      <div><span className="nexus-kicker">个人控制台</span><h2>{needsAttention ? '有项目需要处理' : '核心服务正常'}</h2><p>数据库 {system.data.database || 'unknown'} · 备份 {backup?.state || '待确认'}</p></div>
      <div className="nexus-overview-status"><StatusBadge tone={systemTone}>Nexus</StatusBadge><StatusBadge tone={toneForStatus(backup?.state)}>备份</StatusBadge></div>
    </section>
    {errors.length > 0 && <InlineAlert tone="danger" title="部分数据读取失败" message={errors.join('；')} />}

    <section className="dashboard-grid-nexus dashboard-focus-grid">
      <Panel className="dashboard-attention-panel" icon={CircleAlert} title="需要处理" subtitle="只显示会影响使用的问题">
        {!needsAttention ? <EmptyMini text="当前没有需要立刻处理的对象。" /> : <>
          {!system.data.ok && <button type="button" className="attention-row" onClick={() => navigate('settings')}><StatusBadge tone="danger">error</StatusBadge><span><strong>系统状态异常</strong><small>{system.data.database || 'unknown'}</small></span><ChevronRight size={16} /></button>}
          {backup?.state === 'failed' && <button type="button" className="attention-row" onClick={() => navigate('settings')}><StatusBadge tone="danger">failed</StatusBadge><span><strong>备份失败</strong><small>{backup.message || formatTime(backup.last_completed_at || backup.last_started_at)}</small></span><ChevronRight size={16} /></button>}
          {errors.map((message) => <div className="nx-alert is-error" key={message}>{message}</div>)}
        </>}
      </Panel>

      <Panel className="dashboard-system-panel" icon={Activity} title="系统" subtitle="NexusDock 与数据库">
        <SettingValue label="服务" value={system.data.service || 'nexusdock'} tone={systemTone} />
        <SettingValue label="数据库" value={system.data.database || 'unknown'} tone={system.data.database === 'ok' ? 'ok' : 'danger'} />
        <details className="nexus-technical-details"><summary>技术信息</summary><SettingValue label="Schema" value={String(system.data.schema_version || 0)} /><SettingValue label="Nexus 数据" value={system.data.nexus_data_dir || '暂无'} mono /><SettingValue label="Recall 仓库" value={system.data.recall_repo_dir || '暂无'} mono /></details>
      </Panel>

      <BackupPanel backup={backup} className="dashboard-backup-panel" />
    </section>
  </>;
}

function isRuntimeSection(section: Section): section is RuntimeSection {
  return RUNTIME_SECTIONS.some((item) => item.id === section);
}

function RuntimeContent({ active, refreshToken }: { active: RuntimeSection; refreshToken: number }) {
  return <section className={`runtime-standalone-page runtime-${active}-page`}>
    {active === 'tasks' && <TaskCenterPage refreshToken={refreshToken} />}
    {active === 'skills' && <SkillsPage refreshToken={refreshToken} />}
    {active === 'templates' && <WorkflowTemplatesPage refreshToken={refreshToken} />}
    {active === 'mcp' && <MCPPage refreshToken={refreshToken} />}
  </section>;
}

function SettingsPage({ refreshToken }: { refreshToken: number }) {
  const system = useResource<SystemStatus>('/v1/system/status', { ok: false, service: 'nexusdock', database: 'unknown', schema_version: 0, nexus_data_dir: '', recall_repo_dir: '' }, refreshToken);
  const backup = useResource<BackupStatus | undefined>('/v1/backup/status', undefined, refreshToken);
  return <>
    <AccountSecurity />
    <section className="settings-grid compact-settings">
      <Panel className="settings-system-panel" icon={Activity} title="系统" subtitle="运行状态与数据位置">
        <SettingValue label="服务" value={system.data.service || 'nexusdock'} tone={system.data.ok ? 'ok' : 'danger'} />
        <SettingValue label="数据库" value={system.data.database || 'unknown'} tone={system.data.database === 'ok' ? 'ok' : 'danger'} />
        <details className="nexus-technical-details"><summary>数据与版本</summary><SettingValue label="Schema" value={String(system.data.schema_version || 0)} /><SettingValue label="Nexus 数据" value={system.data.nexus_data_dir || '暂无'} mono /><SettingValue label="Recall 仓库" value={system.data.recall_repo_dir || '暂无'} mono /></details>
      </Panel>
      <BackupPanel backup={backup.data} className="settings-backup-panel" />
    </section>
  </>;
}

function BackupPanel({ backup, className }: { backup?: BackupStatus; className?: string }) {
  return <Panel className={className} icon={Database} title="备份" subtitle="自动备份状态">
    {backup ? <>
      <SettingValue label="状态" value={backup.state || 'unknown'} tone={toneForStatus(backup.state)} />
      <SettingValue label="最近完成" value={formatTime(backup.last_completed_at)} />
      <SettingValue label="下次运行" value={formatTime(backup.next_run_at)} />
      {backup.message && <div className="nx-alert is-info">{backup.message}</div>}
      <details className="nexus-technical-details"><summary>备份详情</summary><SettingValue label="主机" value={backup.host || '暂无'} /><SettingValue label="计划" value={backup.schedule || '暂无'} /><SettingValue label="显示时区" value={timeZoneLabel()} /><SettingValue label="归档大小" value={formatBytes(backup.archive_size)} /><SettingValue label="远端路径" value={backup.remote_path || '暂无'} mono /><SettingValue label="SHA256" value={backup.sha256 || '暂无'} mono /></details>
      {backup.history?.length ? <details className="backup-history-details"><summary>最近备份（{backup.history.length}）</summary><div className="backup-history">{backup.history.slice(0, 5).map((item, index) => <div key={`${item.started_at || index}:${item.state}`}><StatusBadge tone={toneForStatus(item.state)}>{item.state}</StatusBadge><span><strong>{formatTime(item.completed_at || item.started_at)}</strong><small>{item.archive || item.remote_path || item.message || '暂无详情'}</small></span></div>)}</div></details> : null}
    </> : <EmptyMini text="暂无备份状态。" />}
  </Panel>;
}

function Panel({ title, subtitle, icon: Icon, className = '', children }: { title: string; subtitle: string; icon?: typeof Home; className?: string; children: ReactNode }) { return <article className={`nexus-panel ${className}`.trim()}><header>{Icon && <span className="nexus-panel-icon"><Icon size={17} /></span>}<div><h3>{title}</h3><p>{subtitle}</p></div></header><div className="panel-body">{children}</div></article>; }
function StatusBadge({ tone, children }: { tone: Tone; children: ReactNode }) { return <span className={`status-badge tone-${tone}`}><span />{children}</span>; }
function InlineAlert({ tone, title, message }: { tone: Tone; title: string; message: string }) { return <div className={`nexus-inline-alert tone-${tone}`}><strong>{title}</strong><span>{message}</span></div>; }
function EmptyMini({ text }: { text: string }) { return <p className="empty-mini">{text}</p>; }
function SettingValue({ label, value, tone = 'muted', mono = false }: { label: string; value: string; tone?: Tone; mono?: boolean }) { return <div className="setting-value"><span>{label}</span><div>{tone !== 'muted' && <StatusBadge tone={tone}>{value}</StatusBadge>}{tone === 'muted' && <strong className={mono ? 'nx-mono' : ''}>{value}</strong>}</div></div>; }
