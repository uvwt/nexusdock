import { useEffect, useMemo, useState } from 'react';
import { AppWindow, Cable, Copy, Eye, EyeOff, RefreshCw, RotateCcw, ShieldCheck } from 'lucide-react';
import { ApiError, api } from '../../api/client';
import Dialog from '../Dialog';

type MCPTokenResponse = {
  ok: boolean;
  token: string;
};

type MCPSettingsResponse = MCPTokenResponse & {
  mcp_apps_enabled: boolean;
  persisted: boolean;
  updated_at?: string;
};

function errorMessage(error: unknown): string {
  if (error instanceof ApiError) return error.message;
  return error instanceof Error ? error.message : '请求失败';
}

async function copyText(value: string): Promise<void> {
  if (navigator.clipboard?.writeText) {
    await navigator.clipboard.writeText(value);
    return;
  }
  const textarea = document.createElement('textarea');
  textarea.value = value;
  textarea.setAttribute('readonly', '');
  textarea.style.position = 'fixed';
  textarea.style.opacity = '0';
  document.body.appendChild(textarea);
  textarea.select();
  const copied = document.execCommand('copy');
  textarea.remove();
  if (!copied) throw new Error('浏览器未允许复制');
}

export default function MCPAccessPanel({ refreshToken }: { refreshToken: number }) {
  const [token, setToken] = useState('');
  const [revealed, setRevealed] = useState(false);
  const [loading, setLoading] = useState(true);
  const [resetting, setResetting] = useState(false);
  const [mcpAppsEnabled, setMCPAppsEnabled] = useState(true);
  const [savingApps, setSavingApps] = useState(false);
  const [resetOpen, setResetOpen] = useState(false);
  const [notice, setNotice] = useState<{ tone: 'success' | 'error' | 'info'; text: string } | null>(null);
  const endpoint = useMemo(() => new URL('/mcp', window.location.origin).toString(), []);

  async function load() {
    setLoading(true);
    try {
      const result = await api<MCPSettingsResponse>('/v1/settings/mcp');
      setToken(result.token);
      setMCPAppsEnabled(result.mcp_apps_enabled);
      setRevealed(false);
      setNotice(null);
    } catch (error) {
      setNotice({ tone: 'error', text: errorMessage(error) });
    } finally {
      setLoading(false);
    }
  }

  useEffect(() => { void load(); }, [refreshToken]);

  async function copy(value: string, label: string) {
    try {
      await copyText(value);
      setNotice({ tone: 'success', text: `${label}已复制。` });
    } catch (error) {
      setNotice({ tone: 'error', text: errorMessage(error) });
    }
  }

  async function resetToken() {
    setResetting(true);
    setNotice(null);
    try {
      const result = await api<MCPTokenResponse>('/v1/settings/mcp-token/reset', { method: 'POST' });
      setToken(result.token);
      setRevealed(true);
      setResetOpen(false);
      setNotice({ tone: 'success', text: 'MCP Token 已重置，旧 Token 已立即失效。' });
    } catch (error) {
      setResetOpen(false);
      setNotice({ tone: 'error', text: errorMessage(error) });
    } finally {
      setResetting(false);
    }
  }

  async function updateMCPAppsEnabled(enabled: boolean) {
    const previous = mcpAppsEnabled;
    setMCPAppsEnabled(enabled);
    setSavingApps(true);
    setNotice(null);
    try {
      const result = await api<MCPSettingsResponse>('/v1/settings/mcp', {
        method: 'PUT',
        body: JSON.stringify({ mcp_apps_enabled: enabled }),
      });
      setMCPAppsEnabled(result.mcp_apps_enabled);
      setNotice({ tone: 'success', text: `MCP Apps UI 已${result.mcp_apps_enabled ? '启用' : '关闭'}。` });
    } catch (error) {
      setMCPAppsEnabled(previous);
      setNotice({ tone: 'error', text: errorMessage(error) });
    } finally {
      setSavingApps(false);
    }
  }

  return <section className="mcp-access-panel">
    <header className="settings-section-heading mcp-access-heading">
      <div><span className="nexus-eyebrow">MCP ACCESS</span><h2>MCP 接入</h2><p>为不使用 OAuth 的 MCP 客户端提供固定 Bearer Token。</p></div>
      <button type="button" className="nx-button is-secondary" onClick={() => void load()} disabled={loading || resetting}><RefreshCw size={15} />刷新</button>
    </header>

    {notice && <div className={`nx-alert is-${notice.tone}`}>{notice.text}</div>}

    <section className="mcp-access-card">
      <header>
        <span className="nexus-panel-icon"><Cable size={17} /></span>
        <div><h3>连接信息</h3><p>客户端连接 NexusDock 的统一 MCP 入口。</p></div>
      </header>
      <div className="mcp-access-body">
        <label className="mcp-access-field">
          <span>MCP 地址</span>
          <div className="mcp-access-value"><input type="text" readOnly value={endpoint} aria-label="MCP 地址" /><button type="button" className="nx-button is-secondary is-small" onClick={() => void copy(endpoint, 'MCP 地址')}><Copy size={14} />复制</button></div>
        </label>
        <label className="mcp-access-field">
          <span>Access Token</span>
          <div className="mcp-access-value">
            <input type={revealed ? 'text' : 'password'} readOnly value={token} placeholder={loading ? '读取中…' : ''} autoComplete="off" aria-label="MCP Access Token" />
            <button type="button" className="nx-button is-secondary is-small mcp-token-icon-button" onClick={() => setRevealed((value) => !value)} disabled={!token} aria-label={revealed ? '隐藏 Token' : '显示 Token'} title={revealed ? '隐藏 Token' : '显示 Token'}>{revealed ? <EyeOff size={14} /> : <Eye size={14} />}</button>
            <button type="button" className="nx-button is-secondary is-small" onClick={() => void copy(token, 'Token')} disabled={!token}><Copy size={14} />复制</button>
          </div>
        </label>
      </div>
      <footer className="mcp-access-footer">
        <div><ShieldCheck size={16} /><span><strong>仅用于 MCP</strong><small>这个 Token 不能访问 NexusDock 的 `/v1` 管理 API。</small></span></div>
        <button type="button" className="nx-button is-danger" onClick={() => setResetOpen(true)} disabled={loading || resetting}><RotateCcw size={15} />重置 Token</button>
      </footer>
    </section>

    <section className="mcp-access-card">
      <header>
        <span className="nexus-panel-icon"><AppWindow size={17} /></span>
        <div><h3>MCP Apps UI</h3><p>控制 NexusDock 对 MCP 客户端发布交互式 Apps UI。</p></div>
      </header>
      <div className="mcp-access-body">
        <label className="mcp-apps-toggle">
          <input type="checkbox" checked={mcpAppsEnabled} onChange={(event) => void updateMCPAppsEnabled(event.target.checked)} disabled={loading || savingApps} />
          <span><strong>启用 MCP Apps UI</strong><small>为支持 MCP Apps 的客户端提供交互式 UI 视图；关闭后工具功能不受影响。</small></span>
        </label>
      </div>
    </section>

    <div className="mcp-access-hint"><strong>Authorization</strong><code>Bearer {'<Access Token>'}</code><span>重置会立刻断开旧 Token 的访问权限，OAuth 客户端不受影响。</span></div>

    {resetOpen && <Dialog title="重置 MCP Token" description="当前 Token 会立即失效，已使用旧 Token 的客户端需要重新配置。" onClose={() => !resetting && setResetOpen(false)}>
      <div className="mcp-token-reset-dialog">
        <p>OAuth 客户端不会受影响。重置完成后，新 Token 会直接显示在当前页面。</p>
        <footer><button type="button" className="nx-button is-secondary" onClick={() => setResetOpen(false)} disabled={resetting}>取消</button><button type="button" className="nx-button is-danger" onClick={() => void resetToken()} disabled={resetting}><RotateCcw size={15} />{resetting ? '重置中…' : '确认重置'}</button></footer>
      </div>
    </Dialog>}
  </section>;
}
