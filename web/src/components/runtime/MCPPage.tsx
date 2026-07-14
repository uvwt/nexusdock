import { useEffect, useMemo, useRef, useState, type FormEvent } from 'react';
import { Cable, CirclePlus, KeyRound, Link, Power, RefreshCw, Server, Terminal, Trash2 } from 'lucide-react';
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

function errorMessage(error: unknown): string {
  if (error instanceof ApiError) return `${error.code || error.status}：${error.message}`;
  return error instanceof Error ? error.message : 'MCP 操作失败';
}

function statusTone(server: MCPServer): string {
  if (!server.enabled) return 'muted';
  if (server.status === 'ready' || server.status === 'connected') return 'ok';
  if (server.status === 'error' || server.last_error) return 'danger';
  return 'warn';
}

export default function MCPPage({ refreshToken }: { refreshToken: number }) {
  const [servers, setServers] = useState<MCPServer[]>([]);
  const [selectedName, setSelectedName] = useState('');
  const [detail, setDetail] = useState<MCPDetailResponse | null>(null);
  const [envItems, setEnvItems] = useState<MCPEnvResponse['items']>([]);
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState('');
  const [notice, setNotice] = useState<Notice | null>(null);
  const [addOpen, setAddOpen] = useState(false);
  const [removeTarget, setRemoveTarget] = useState<MCPServer | null>(null);
  const [mobileDetailOpen, setMobileDetailOpen] = useState(false);
  const [addForm, setAddForm] = useState<AddForm>(emptyAddForm);
  const [envKey, setEnvKey] = useState('');
  const [envValue, setEnvValue] = useState('');
  const detailRequestRef = useRef(0);

  const selected = useMemo(
    () => servers.find((server) => server.name === selectedName) || servers[0] || null,
    [servers, selectedName],
  );

  async function loadServers(preferredName = selectedName) {
    setLoading(true);
    try {
      const result = await api<MCPListResponse>('/v1/runtime/mcp');
      setServers(result.servers || []);
      const nextName = result.servers?.some((server) => server.name === preferredName) ? preferredName : result.servers?.[0]?.name || '';
      setSelectedName(nextName);
      if (!nextName) {
        setDetail(null);
        setEnvItems([]);
      }
    } catch (error) {
      setNotice({ tone: 'error', text: errorMessage(error) });
    } finally {
      setLoading(false);
    }
  }

  async function loadDetail(name: string) {
    if (!name) return;
    const requestID = ++detailRequestRef.current;
    try {
      const [detailResult, envResult] = await Promise.all([
        api<MCPDetailResponse>(`/v1/runtime/mcp/${encodeURIComponent(name)}`),
        api<MCPEnvResponse>(`/v1/runtime/mcp/${encodeURIComponent(name)}/environment`),
      ]);
      if (requestID === detailRequestRef.current) {
        setDetail(detailResult);
        setEnvItems(envResult.items || []);
      }
    } catch (error) {
      if (requestID === detailRequestRef.current) {
        setNotice({ tone: 'error', text: errorMessage(error) });
      }
    }
  }

  useEffect(() => { void loadServers(); }, [refreshToken]);
  useEffect(() => {
    detailRequestRef.current += 1;
    setDetail(null);
    if (selected?.name) void loadDetail(selected.name);
  }, [selected?.name]);

  async function manage(action: string, name: string, payload: Record<string, unknown> = {}): Promise<boolean> {
    setBusy(`${action}:${name}`);
    setNotice(null);
    try {
      await api('/v1/runtime/mcp', { method: 'POST', body: JSON.stringify({ action, name, ...payload }), timeoutMs: action === 'refresh' ? 60_000 : 15_000 });
      await loadServers(name);
      if (action !== 'remove') await loadDetail(name);
      setNotice({ tone: 'success', text: actionMessage(action, name) });
      return true;
    } catch (error) {
      setNotice({ tone: 'error', text: errorMessage(error) });
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
      await api('/v1/runtime/mcp', { method: 'POST', body: JSON.stringify(payload) });
      setAddOpen(false);
      setAddForm(emptyAddForm);
      await loadServers(name);
      setMobileDetailOpen(true);
      setNotice({ tone: 'success', text: `已添加 MCP「${name}」。` });
    } catch (error) {
      setNotice({ tone: 'error', text: errorMessage(error) });
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
    {notice && <div className={`nx-alert is-${notice.tone}`} role="status"><span>{notice.text}</span><button type="button" onClick={() => setNotice(null)}>关闭</button></div>}
    <header className="mcp-heading">
      <div><span className="nexus-eyebrow">AGENTDOCK RUNTIME</span><h2>MCP 服务</h2><p>Nexus 仅转发 AgentDock 的动态 MCP 管理接口；密钥值不会回显。</p></div>
      <button type="button" className="nx-button is-primary" onClick={() => setAddOpen(true)}><CirclePlus size={16} />添加 MCP</button>
    </header>

    <section className={`mcp-layout mobile-drilldown ${mobileDetailOpen ? 'is-detail-open' : 'is-list-open'}`}>
      <aside className="mcp-list-panel mobile-drilldown-list">
        <div className="mcp-list-summary"><strong>{servers.length}</strong><span>个已注册服务</span></div>
        <div className="mcp-server-list">
          {loading && servers.length === 0 ? <p className="empty-mini">正在读取 MCP 服务…</p> : null}
          {!loading && servers.length === 0 ? <div className="mcp-empty"><Cable size={24} /><strong>尚未注册 MCP</strong><span>添加 HTTP 或 stdio MCP 服务后会显示在这里。</span></div> : null}
          {servers.map((server) => <button key={server.name} type="button" className={selected?.name === server.name ? 'is-active' : ''} onClick={() => { setSelectedName(server.name); setMobileDetailOpen(true); }}>
            <span className="mcp-server-icon">{server.transport === 'stdio' ? <Terminal size={17} /> : <Link size={17} />}</span>
            <span><strong>{server.name}</strong><small>{server.description || server.transport}</small></span>
            <em className={`tone-${statusTone(server)}`}>{server.enabled ? server.status || 'enabled' : 'disabled'}</em>
          </button>)}
        </div>
      </aside>

      <main className="mcp-detail-panel mobile-drilldown-detail">
        {selected && <MobileDrilldownBar label="MCP 详情" title={selected.name} meta={selected.transport} onBack={() => setMobileDetailOpen(false)} />}
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
        /> : <div className="mcp-empty is-detail"><Server size={28} /><strong>选择 MCP 服务</strong><span>查看连接信息、工具数量和隔离环境变量。</span></div>}
      </main>
    </section>

    {addOpen && <Dialog title="添加 MCP 服务" description="配置会写入 AgentDock；敏感值请在添加后单独保存到隔离环境。" onClose={() => setAddOpen(false)} wide>
      <form className="mcp-form" onSubmit={addServer}>
        <label><span>名称</span><input required value={addForm.name} onChange={(event) => setAddForm({ ...addForm, name: event.target.value })} placeholder="例如 github" /></label>
        <label><span>说明</span><input value={addForm.description} onChange={(event) => setAddForm({ ...addForm, description: event.target.value })} placeholder="这个 MCP 提供什么能力" /></label>
        <label><span>传输方式</span><select value={addForm.transport} onChange={(event) => setAddForm({ ...addForm, transport: event.target.value as AddForm['transport'] })}><option value="streamable_http">Streamable HTTP</option><option value="stdio">stdio</option></select></label>
        <label><span>超时（毫秒）</span><input inputMode="numeric" value={addForm.timeout} onChange={(event) => setAddForm({ ...addForm, timeout: event.target.value })} /></label>
        {addForm.transport === 'streamable_http' ? <label className="is-wide"><span>服务 URL</span><input required type="url" value={addForm.url} onChange={(event) => setAddForm({ ...addForm, url: event.target.value })} placeholder="https://example.com/mcp" /></label> : <>
          <label className="is-wide"><span>命令</span><input required value={addForm.command} onChange={(event) => setAddForm({ ...addForm, command: event.target.value })} placeholder="npx" /></label>
          <label className="is-wide"><span>参数</span><input value={addForm.args} onChange={(event) => setAddForm({ ...addForm, args: event.target.value })} placeholder="-y @modelcontextprotocol/server" /></label>
          <label className="is-wide"><span>工作目录</span><input value={addForm.cwd} onChange={(event) => setAddForm({ ...addForm, cwd: event.target.value })} placeholder="可选" /></label>
        </>}
        <label className="mcp-check is-wide"><input type="checkbox" checked={addForm.enabled} onChange={(event) => setAddForm({ ...addForm, enabled: event.target.checked })} /><span>添加后立即启用</span></label>
        <footer><button type="button" className="nx-button is-secondary" onClick={() => setAddOpen(false)}>取消</button><button type="submit" className="nx-button is-primary" disabled={busy === 'add'}>{busy === 'add' ? '正在添加…' : '添加 MCP'}</button></footer>
      </form>
    </Dialog>}

    {removeTarget && <Dialog title="移除 MCP 服务" description={`将从 AgentDock 注册表移除「${removeTarget.name}」；隔离环境文件不会自动删除。`} onClose={() => setRemoveTarget(null)}>
      <div className="mcp-confirm"><p>此操作不会删除外部 MCP 服务，但 AgentDock 将不再加载它。</p><footer><button type="button" className="nx-button is-secondary" onClick={() => setRemoveTarget(null)}>取消</button><button type="button" className="nx-button is-danger" disabled={busy === `remove:${removeTarget.name}`} onClick={() => void removeServer()}>确认移除</button></footer></div>
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
  return <article className="mcp-detail-card">
    <header>
      <div><span className="nexus-eyebrow">{server.transport}</span><h3>{server.name}</h3><p>{server.description || '暂无说明。'}</p></div>
      <span className={`status-badge tone-${statusTone(server)}`}><span />{server.enabled ? server.status || 'enabled' : 'disabled'}</span>
    </header>
    <div className="mcp-actions">
      <button type="button" className="nx-button is-secondary" disabled={!!busy || !server.enabled} title={server.enabled ? '重新发现 MCP 工具' : '请先启用 MCP 服务'} onClick={onRefresh}><RefreshCw size={15} />刷新工具</button>
      <button type="button" className="nx-button is-secondary" disabled={!!busy} onClick={onToggle}><Power size={15} />{server.enabled ? '停用' : '启用'}</button>
      <button type="button" className="nx-button is-danger" disabled={!!busy} onClick={onRemove}><Trash2 size={15} />移除</button>
    </div>
    <section className="mcp-metrics"><div><span>工具</span><strong>{server.tool_count || 0}</strong></div><div><span>传输</span><strong>{server.transport}</strong></div><div><span>状态</span><strong>{server.enabled ? server.status || 'enabled' : 'disabled'}</strong></div></section>
    {server.last_error && <div className="nx-alert is-error">{server.last_error}</div>}
    <section className="mcp-section"><h4>连接配置</h4><dl><div><dt>{config.transport === 'stdio' ? '命令' : 'URL'}</dt><dd>{config.transport === 'stdio' ? [config.command, ...(config.args || [])].filter(Boolean).join(' ') : config.url || '暂无'}</dd></div>{config.cwd && <div><dt>工作目录</dt><dd>{config.cwd}</dd></div>}</dl></section>
    <section className="mcp-section"><div className="mcp-section-head"><div><h4>隔离环境</h4><p>只显示变量名和是否已配置，永不读取原值。</p></div><KeyRound size={18} /></div>
      <div className="mcp-env-list">{envItems.length === 0 ? <p className="empty-mini">尚未配置环境变量。</p> : envItems.map((item) => <div key={item.key}><span><strong>{item.key}</strong><small>{item.configured ? '已配置' : '空值'}</small></span><button type="button" disabled={!!busy} onClick={() => onRemoveEnvironment(item.key)}>删除</button></div>)}</div>
      <form className="mcp-env-form" onSubmit={onSaveEnvironment}><label><span>变量名</span><input required value={envKey} onChange={(event) => onEnvKey(event.target.value)} placeholder="API_TOKEN" autoComplete="off" /></label><label><span>变量值</span><input required type="password" value={envValue} onChange={(event) => onEnvValue(event.target.value)} placeholder="保存后不会再显示" autoComplete="new-password" /></label><button type="submit" className="nx-button is-primary" disabled={!!busy}><KeyRound size={15} />保存变量</button></form>
    </section>
  </article>;
}

function splitArgs(value: string): string[] {
  return value.match(/(?:[^\s"]+|"[^"]*")+/g)?.map((item) => item.replace(/^"|"$/g, '')) || [];
}

function actionMessage(action: string, name: string): string {
  const labels: Record<string, string> = { refresh: '已刷新工具', enable: '已启用', disable: '已停用', remove: '已移除', env_set: '已保存环境变量', env_unset: '已删除环境变量' };
  return `${labels[action] || '操作已完成'}「${name}」。`;
}
