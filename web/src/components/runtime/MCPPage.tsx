import { useEffect, useMemo, useRef, useState, type FormEvent } from 'react';
import { useTranslation } from 'react-i18next';
import type { TFunction } from 'i18next';
import { Cable, KeyRound, Link, Power, RefreshCw, Server, Terminal, Trash2 } from 'lucide-react';
import { ApiError, api } from '../../api/client';
import Dialog from '../Dialog';
import MobileDrilldownBar from '../MobileDrilldownBar';

type MCPServer = {
  name: string;
  description: string;
  transport: string;
  enabled: boolean;
  status: string;
  tool_count: number;
  last_error?: string;
  refreshed_at?: string;
};

type MCPConfig = {
  name: string;
  description: string;
  transport: string;
  url?: string;
  command?: string;
  args?: string[];
  cwd?: string;
  header_env?: Record<string, string>;
  env_from_env?: Record<string, string>;
};

type MCPListResponse = { ok: boolean; servers: MCPServer[]; count: number };
type MCPDetailResponse = { ok: boolean; server: MCPServer; config: MCPConfig };
type MCPEnvResponse = { ok: boolean; items: Array<{ key: string; configured: boolean }>; count: number };
type Notice = { tone: 'success' | 'error'; text: string };

type AddForm = {
  name: string;
  description: string;
  transport: 'streamable_http' | 'stdio';
  url: string;
  command: string;
  args: string;
  cwd: string;
  timeout: string;
  enabled: boolean;
};

const emptyAddForm: AddForm = {
  name: '', description: '', transport: 'streamable_http', url: '', command: '', args: '', cwd: '', timeout: '30000', enabled: true,
};

function errorMessage(error: unknown, fallback: string): string {
  if (error instanceof ApiError) return `${error.code || error.status}: ${error.message}`;
  return error instanceof Error ? error.message : fallback;
}

function isServerAbnormal(server: Pick<MCPServer, 'status' | 'last_error'>): boolean {
  return server.status === 'error' || server.status === 'failed' || Boolean(server.last_error);
}

function serverStatusLabel(server: MCPServer, t: TFunction): string {
  if (!server.enabled) return t('Not enabled');
  return isServerAbnormal(server) ? t('Abnormal') : t('Normal');
}

function statusTone(server: MCPServer): string {
  if (!server.enabled) return 'muted';
  return isServerAbnormal(server) ? 'danger' : 'ok';
}

function mcpSelectionFromHash(): string {
  const [section, encodedName] = window.location.hash.replace(/^#\/?/, '').split('/');
  if (section !== 'mcp' || !encodedName) return '';
  try {
    return decodeURIComponent(encodedName);
  } catch {
    return '';
  }
}

function actionMessage(action: string, name: string, t: (key: string, options?: Record<string, unknown>) => string): string {
  switch (action) {
    case 'refresh': return t('Refreshed tools for “{{name}}”.', { name });
    case 'enable': return t('Enabled “{{name}}”.', { name });
    case 'disable': return t('Disabled “{{name}}”.', { name });
    case 'remove': return t('Removed “{{name}}”.', { name });
    case 'env_set': return t('Saved environment variable for “{{name}}”.', { name });
    case 'env_unset': return t('Deleted environment variable for “{{name}}”.', { name });
    default: return t('Operation completed for “{{name}}”.', { name });
  }
}

export default function MCPPage({ nodeID, refreshToken, addOpen, onAddOpenChange }: {
  nodeID: string;
  refreshToken: number;
  addOpen: boolean;
  onAddOpenChange: (open: boolean) => void;
}) {
  const { t } = useTranslation();
  const [servers, setServers] = useState<MCPServer[]>([]);
  const [selectedName, setSelectedName] = useState(mcpSelectionFromHash);
  const [detail, setDetail] = useState<MCPDetailResponse | null>(null);
  const [envItems, setEnvItems] = useState<MCPEnvResponse['items']>([]);
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState('');
  const [notice, setNotice] = useState<Notice | null>(null);
  const [removeTarget, setRemoveTarget] = useState<MCPServer | null>(null);
  const [mobileDetailOpen, setMobileDetailOpen] = useState(() => Boolean(mcpSelectionFromHash()));
  const [addForm, setAddForm] = useState<AddForm>(emptyAddForm);
  const [envKey, setEnvKey] = useState('');
  const [envValue, setEnvValue] = useState('');
  const detailRequestRef = useRef(0);
  const runtimeBase = `/v1/runtime/nodes/${encodeURIComponent(nodeID)}`;

  const selected = useMemo(
    () => servers.find((server) => server.name === selectedName) || servers[0] || null,
    [servers, selectedName],
  );

  async function loadServers(preferredName = selectedName) {
    setLoading(true);
    try {
      const result = await api<MCPListResponse>(`${runtimeBase}/mcp`);
      setServers(result.servers || []);
      const nextName = result.servers?.some((server) => server.name === preferredName) ? preferredName : result.servers?.[0]?.name || '';
      setSelectedName(nextName);
      if (!nextName) {
        setDetail(null);
        setEnvItems([]);
      }
    } catch (error) {
      setNotice({ tone: 'error', text: errorMessage(error, t('MCP operation failed')) });
    } finally {
      setLoading(false);
    }
  }

  async function loadDetail(name: string) {
    if (!name) return;
    const requestID = ++detailRequestRef.current;
    try {
      const [detailResult, envResult] = await Promise.all([
        api<MCPDetailResponse>(`${runtimeBase}/mcp/${encodeURIComponent(name)}`),
        api<MCPEnvResponse>(`${runtimeBase}/mcp/${encodeURIComponent(name)}/environment`),
      ]);
      if (requestID === detailRequestRef.current) {
        setDetail(detailResult);
        setEnvItems(envResult.items || []);
      }
    } catch (error) {
      if (requestID === detailRequestRef.current) {
        setNotice({ tone: 'error', text: errorMessage(error, t('MCP operation failed')) });
      }
    }
  }

  useEffect(() => { void loadServers(); }, [refreshToken, nodeID]);
  useEffect(() => {
    detailRequestRef.current += 1;
    setDetail(null);
    if (selected?.name) void loadDetail(selected.name);
  }, [selected?.name, nodeID]);

  async function manage(action: string, name: string, payload: Record<string, unknown> = {}): Promise<boolean> {
    setBusy(`${action}:${name}`);
    setNotice(null);
    try {
      await api(`${runtimeBase}/mcp`, { method: 'POST', body: JSON.stringify({ action, name, ...payload }), timeoutMs: action === 'refresh' ? 60_000 : 15_000 });
      await loadServers(name);
      if (action !== 'remove') await loadDetail(name);
      setNotice({ tone: 'success', text: actionMessage(action, name, t) });
      return true;
    } catch (error) {
      setNotice({ tone: 'error', text: errorMessage(error, t('MCP operation failed')) });
      return false;
    } finally {
      setBusy('');
    }
  }

  async function addServer(event: FormEvent) {
    event.preventDefault();
    const name = addForm.name.trim();
    if (!name) return;
    setBusy('add');
    setNotice(null);
    try {
      const payload = {
        action: 'add',
        name,
        description: addForm.description.trim(),
        transport: addForm.transport,
        url: addForm.transport === 'streamable_http' ? addForm.url.trim() : undefined,
        command: addForm.transport === 'stdio' ? addForm.command.trim() : undefined,
        args: addForm.transport === 'stdio' ? splitArgs(addForm.args) : undefined,
        cwd: addForm.transport === 'stdio' ? addForm.cwd.trim() : undefined,
        timeout_ms: Number(addForm.timeout) || 30000,
        enabled: addForm.enabled,
      };
      await api(`${runtimeBase}/mcp`, { method: 'POST', body: JSON.stringify(payload) });
      onAddOpenChange(false);
      setAddForm(emptyAddForm);
      await loadServers(name);
      setMobileDetailOpen(true);
      setNotice({ tone: 'success', text: t('Added MCP “{{name}}”.', { name }) });
    } catch (error) {
      setNotice({ tone: 'error', text: errorMessage(error, t('MCP operation failed')) });
    } finally {
      setBusy('');
    }
  }

  async function saveEnvironment(event: FormEvent) {
    event.preventDefault();
    if (!selected || !envKey.trim()) return;
    if (await manage('env_set', selected.name, { key: envKey.trim(), value: envValue })) {
      setEnvKey('');
      setEnvValue('');
    }
  }

  async function removeEnvironment(key: string) {
    if (!selected) return;
    await manage('env_unset', selected.name, { key });
  }

  async function removeServer() {
    const target = removeTarget;
    if (!target) return;
    if (await manage('remove', target.name)) {
      setRemoveTarget(null);
      setMobileDetailOpen(false);
    }
  }

  return <section className="mcp-page">
    {notice && <div className={`nx-alert is-${notice.tone}`} role="status"><span>{notice.text}</span><button type="button" onClick={() => setNotice(null)}>{t('Close')}</button></div>}
    <section className={`mcp-layout mobile-drilldown ${mobileDetailOpen ? 'is-detail-open' : 'is-list-open'}`}>
      <aside className="mcp-list-panel mobile-drilldown-list">
        <div className="mcp-list-summary"><strong>{servers.length}</strong><span>{t('registered services')}</span></div>
        <div className="mcp-server-list">
          {loading && servers.length === 0 ? <p className="empty-mini">{t('Loading MCP services…')}</p> : null}
          {!loading && servers.length === 0 ? <div className="mcp-empty"><Cable size={24} /><strong>{t('No MCP registered yet')}</strong><span>{t('HTTP or stdio MCP services will appear here once added.')}</span></div> : null}
          {servers.map((server) => <button key={server.name} type="button" className={selected?.name === server.name ? 'is-active' : ''} onClick={() => { setSelectedName(server.name); setMobileDetailOpen(true); }}>
            <span className="mcp-server-icon">{server.transport === 'stdio' ? <Terminal size={17} /> : <Link size={17} />}</span>
            <span><strong>{server.name}</strong><small>{server.description || server.transport}</small></span>
            <em className={`tone-${statusTone(server)}`}>{serverStatusLabel(server, t)}</em>
          </button>)}
        </div>
      </aside>

      <section className="mobile-drilldown-detail">
        {selected && <MobileDrilldownBar label={t('MCP details')} title={selected.name} meta={selected.transport} onBack={() => setMobileDetailOpen(false)} />}
        {selected && detail ? <MCPDetail
          server={detail.server}
          config={detail.config}
          envItems={envItems}
          envKey={envKey}
          envValue={envValue}
          busy={busy}
          onEnvKey={setEnvKey}
          onEnvValue={setEnvValue}
          onSaveEnvironment={saveEnvironment}
          onRemoveEnvironment={removeEnvironment}
          onRefresh={() => void manage('refresh', selected.name)}
          onToggle={() => void manage(selected.enabled ? 'disable' : 'enable', selected.name)}
          onRemove={() => setRemoveTarget(selected)}
        /> : <div className="mcp-empty is-detail"><Server size={28} /><strong>{t('Select an MCP service')}</strong><span>{t('View connection details, tool counts, and isolated environment variables.')}</span></div>}
      </section>
    </section>

    {addOpen && <Dialog title={t('Add MCP service')} description={t('Configuration is written to AgentDock; save sensitive values to the isolated environment after adding.')} onClose={() => onAddOpenChange(false)} wide>
      <form className="mcp-form" onSubmit={addServer}>
        <label><span>{t('Name')}</span><input required value={addForm.name} onChange={(event) => setAddForm({ ...addForm, name: event.target.value })} placeholder={t('e.g. github')} /></label>
        <label><span>{t('Description')}</span><input value={addForm.description} onChange={(event) => setAddForm({ ...addForm, description: event.target.value })} placeholder={t('What capabilities this MCP provides')} /></label>
        <label><span>{t('Transport')}</span><select value={addForm.transport} onChange={(event) => setAddForm({ ...addForm, transport: event.target.value as AddForm['transport'] })}><option value="streamable_http">Streamable HTTP</option><option value="stdio">stdio</option></select></label>
        <label><span>{t('Timeout (ms)')}</span><input inputMode="numeric" value={addForm.timeout} onChange={(event) => setAddForm({ ...addForm, timeout: event.target.value })} /></label>
        {addForm.transport === 'streamable_http' ? <label className="is-wide"><span>{t('Service URL')}</span><input required type="url" value={addForm.url} onChange={(event) => setAddForm({ ...addForm, url: event.target.value })} placeholder="https://example.com/mcp" /></label> : <>
          <label className="is-wide"><span>{t('Command')}</span><input required value={addForm.command} onChange={(event) => setAddForm({ ...addForm, command: event.target.value })} placeholder="npx" /></label>
          <label className="is-wide"><span>{t('Arguments')}</span><input value={addForm.args} onChange={(event) => setAddForm({ ...addForm, args: event.target.value })} placeholder="-y @modelcontextprotocol/server" /></label>
          <label className="is-wide"><span>{t('Working directory')}</span><input value={addForm.cwd} onChange={(event) => setAddForm({ ...addForm, cwd: event.target.value })} placeholder={t('Optional')} /></label>
        </>}
        <label className="mcp-check is-wide"><input type="checkbox" checked={addForm.enabled} onChange={(event) => setAddForm({ ...addForm, enabled: event.target.checked })} /><span>{t('Enable immediately after adding')}</span></label>
        <footer><button type="button" className="nx-button is-secondary" onClick={() => onAddOpenChange(false)}>{t('Cancel')}</button><button type="submit" className="nx-button" disabled={busy === 'add'}>{busy === 'add' ? t('Adding…') : t('Add MCP')}</button></footer>
      </form>
    </Dialog>}

    {removeTarget && <Dialog title={t('Remove MCP service')} description={t('Will remove “{{name}}” from the AgentDock registry; isolated environment files will not be deleted automatically.', { name: removeTarget.name })} onClose={() => setRemoveTarget(null)}>
      <div className="mcp-confirm"><p>{t('This action will not delete the external MCP service, but AgentDock will no longer load it.')}</p><footer><button type="button" className="nx-button is-secondary" onClick={() => setRemoveTarget(null)}>{t('Cancel')}</button><button type="button" className="nx-button is-danger" disabled={busy === `remove:${removeTarget.name}`} onClick={() => void removeServer()}>{t('Confirm removal')}</button></footer></div>
    </Dialog>}
  </section>;
}

function MCPDetail({ server, config, envItems, envKey, envValue, busy, onEnvKey, onEnvValue, onSaveEnvironment, onRemoveEnvironment, onRefresh, onToggle, onRemove }: {
  server: MCPServer;
  config: MCPConfig;
  envItems: MCPEnvResponse['items'];
  envKey: string;
  envValue: string;
  busy: string;
  onEnvKey: (value: string) => void;
  onEnvValue: (value: string) => void;
  onSaveEnvironment: (event: FormEvent) => void;
  onRemoveEnvironment: (key: string) => void;
  onRefresh: () => void;
  onToggle: () => void;
  onRemove: () => void;
}) {
  const { t } = useTranslation();
  return <article className="mcp-detail-card">
    <header>
      <div><span className="nexus-eyebrow">{server.transport}</span><h3>{server.name}</h3><p>{server.description || t('No description.')}</p></div>
      <span className={`status-badge tone-${statusTone(server)}`}><span />{serverStatusLabel(server, t)}</span>
    </header>
    <div className="mcp-actions">
      <button type="button" className="nx-button is-secondary" disabled={!!busy || !server.enabled} title={server.enabled ? t('Rediscover MCP tools') : t('Please enable the MCP service first')} onClick={onRefresh}><RefreshCw size={15} />{t('Refresh tools')}</button>
      <button type="button" className="nx-button is-secondary" disabled={!!busy} onClick={onToggle}><Power size={15} />{server.enabled ? t('Disable') : t('Enable')}</button>
      <button type="button" className="nx-button is-danger" disabled={!!busy} onClick={onRemove}><Trash2 size={15} />{t('Remove')}</button>
    </div>
    <section className="mcp-metrics"><div><span>{t('Tools')}</span><strong>{server.tool_count || 0}</strong></div><div><span>{t('Transport')}</span><strong>{server.transport}</strong></div><div><span>{t('Status')}</span><strong>{serverStatusLabel(server, t)}</strong></div></section>
    {server.last_error && <div className="nx-alert is-error">{server.last_error}</div>}
    <section className="mcp-section"><h4>{t('Connection configuration')}</h4><dl><div><dt>{config.transport === 'stdio' ? t('Command') : 'URL'}</dt><dd>{config.transport === 'stdio' ? [config.command, ...(config.args || [])].filter(Boolean).join(' ') : config.url || t('None')}</dd></div>{config.cwd && <div><dt>{t('Working directory')}</dt><dd>{config.cwd}</dd></div>}</dl></section>
    <section className="mcp-section"><div className="mcp-section-head"><div><h4>{t('Isolated Environment')}</h4><p>{t('Only shows variable names and configuration status; original values are never read.')}</p></div><KeyRound size={18} /></div>
      <div className="mcp-env-list">{envItems.length === 0 ? <p className="empty-mini">{t('No environment variables configured.')}</p> : envItems.map((item) => <div key={item.key}><span><strong>{item.key}</strong><small>{item.configured ? t('Configured') : t('Empty')}</small></span><button type="button" disabled={!!busy} onClick={() => onRemoveEnvironment(item.key)}>{t('Delete')}</button></div>)}</div>
      <form className="mcp-env-form" onSubmit={onSaveEnvironment}><label><span>{t('Variable name')}</span><input required value={envKey} onChange={(event) => onEnvKey(event.target.value)} placeholder="API_TOKEN" autoComplete="off" /></label><label><span>{t('Variable value')}</span><input required type="password" value={envValue} onChange={(event) => onEnvValue(event.target.value)} placeholder={t('Will not be shown again after saving')} autoComplete="new-password" /></label><button type="submit" className="nx-button" disabled={!!busy}><KeyRound size={15} />{t('Save variable')}</button></form>
    </section>
  </article>;
}

function splitArgs(value: string): string[] {
  return value.match(/(?:[^\s"]+|"[^"]*")+/g)?.map((item) => item.replace(/^"|"$/g, '')) || [];
}
