import { useEffect, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import type { TFunction } from 'i18next';
import { AppWindow, Cable, Copy, Eye, EyeOff, RotateCcw } from 'lucide-react';
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

function errorMessage(error: unknown, t: TFunction): string {
  if (error instanceof ApiError) return error.message;
  return error instanceof Error ? error.message : t('Request failed');
}

async function copyText(value: string, t: TFunction): Promise<void> {
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
  if (!copied) throw new Error(t('The browser did not allow copying.'));
}

export default function MCPAccessPanel({ refreshToken }: { refreshToken: number }) {
  const { t } = useTranslation();
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
      setNotice({ tone: 'error', text: errorMessage(error, t) });
    } finally {
      setLoading(false);
    }
  }

  useEffect(() => { void load(); }, [refreshToken]);

  async function copy(value: string, label: string) {
    try {
      await copyText(value, t);
      setNotice({ tone: 'success', text: t('{{label}} copied.', { label }) });
    } catch (error) {
      setNotice({ tone: 'error', text: errorMessage(error, t) });
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
      setNotice({ tone: 'success', text: t('MCP Token reset. The previous token is no longer valid.') });
    } catch (error) {
      setResetOpen(false);
      setNotice({ tone: 'error', text: errorMessage(error, t) });
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
      setNotice({ tone: 'success', text: result.mcp_apps_enabled ? t('Chat cards enabled.') : t('Chat cards disabled.') });
    } catch (error) {
      setMCPAppsEnabled(previous);
      setNotice({ tone: 'error', text: errorMessage(error, t) });
    } finally {
      setSavingApps(false);
    }
  }

  return <section className="mcp-access-panel">
    {notice && <div className={`nx-alert is-${notice.tone}`}>{notice.text}</div>}

    <section className="mcp-access-card">
      <header>
        <span className="nexus-panel-icon"><Cable size={17} /></span>
        <div><h3>{t('Connection information')}</h3><p>{t('The unified MCP endpoint for clients connecting to NexusDock.')}</p></div>
      </header>
      <div className="mcp-access-body">
        <label className="mcp-access-field">
          <span>{t('MCP endpoint')}</span>
          <div className="mcp-access-value"><input type="text" readOnly value={endpoint} aria-label={t('MCP endpoint')} /><button type="button" className="nx-button is-secondary is-small" onClick={() => void copy(endpoint, t('MCP endpoint'))}><Copy size={14} />{t('Copy')}</button></div>
        </label>
        <label className="mcp-access-field">
          <span>Access Token</span>
          <div className="mcp-access-value">
            <input type={revealed ? 'text' : 'password'} readOnly value={token} placeholder={loading ? t('Loading…') : ''} autoComplete="off" aria-label="MCP Access Token" />
            <button type="button" className="nx-button is-secondary is-small mcp-token-icon-button" onClick={() => setRevealed((value) => !value)} disabled={!token} aria-label={revealed ? t('Hide Token') : t('Show Token')} title={revealed ? t('Hide Token') : t('Show Token')}>{revealed ? <EyeOff size={14} /> : <Eye size={14} />}</button>
            <button type="button" className="nx-button is-secondary is-small" onClick={() => void copy(token, 'Token')} disabled={!token}><Copy size={14} />{t('Copy')}</button>
          </div>
        </label>
      </div>
      <footer className="mcp-access-footer">
        <div className="mcp-access-authorization"><strong>Authorization</strong><code>Bearer {'<Access Token>'}</code><span>{t('Resetting immediately revokes the previous Token. OAuth clients are unaffected.')}</span></div>
        <button type="button" className="nx-button is-danger" onClick={() => setResetOpen(true)} disabled={loading || resetting}><RotateCcw size={15} />{t('Reset Token')}</button>
      </footer>
    </section>

    <section className="mcp-access-card">
      <header>
        <span className="nexus-panel-icon"><AppWindow size={17} /></span>
        <div><h3>{t('Chat cards')}</h3><p>{t('Control whether NexusDock publishes interactive chat cards to MCP clients.')}</p></div>
      </header>
      <div className="mcp-access-body">
        <label className="mcp-apps-toggle">
          <input type="checkbox" checked={mcpAppsEnabled} onChange={(event) => void updateMCPAppsEnabled(event.target.checked)} disabled={loading || savingApps} />
          <span><strong>{t('Enable chat cards')}</strong><small>{t('Provide interactive chat cards to clients that support MCP Apps. Tool functionality is unaffected when disabled.')}</small></span>
        </label>
      </div>
    </section>

    {resetOpen && <Dialog title={t('Reset MCP Token')} description={t('The current Token will become invalid immediately. Clients using it must be reconfigured.')} onClose={() => !resetting && setResetOpen(false)}>
      <div className="mcp-token-reset-dialog">
        <p>{t('OAuth clients are unaffected. The new Token will be shown on this page after reset.')}</p>
        <footer><button type="button" className="nx-button is-secondary" onClick={() => setResetOpen(false)} disabled={resetting}>{t('Cancel')}</button><button type="button" className="nx-button is-danger" onClick={() => void resetToken()} disabled={resetting}><RotateCcw size={15} />{resetting ? t('Resetting…') : t('Confirm reset')}</button></footer>
      </div>
    </Dialog>}
  </section>;
}
