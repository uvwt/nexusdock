import { useEffect, useState } from 'react';
import { Clock3, KeyRound, Laptop, LogOut, ShieldCheck, Smartphone, Trash2 } from 'lucide-react';
import { api, clearCSRFToken, setCSRFToken } from './api/client';
import { formatTime } from './lib/time';
import { type WebSession } from './Auth';
import { errorMessage } from './authShared';
import { useTranslation } from 'react-i18next';

type SessionResponse = { ok: boolean; session: WebSession };
type SessionsResponse = { ok: boolean; items: WebSession[] };

export default function AccountSecurity({ refreshToken }: { refreshToken: number }) {
  const { t } = useTranslation();
  const [session, setSession] = useState<WebSession | null>(null);
  const [sessions, setSessions] = useState<WebSession[]>([]);
  const [loading, setLoading] = useState(true);
  const [actionBusy, setActionBusy] = useState('');
  const [error, setError] = useState('');

  async function load() {
    setLoading(true);
    setError('');
    try {
      const [current, list] = await Promise.all([
        api<SessionResponse>('/v1/auth/session'),
        api<SessionsResponse>('/v1/auth/sessions'),
      ]);
      setSession(current.session);
      setSessions(list.items || []);
      if (current.session.csrf_token) setCSRFToken(current.session.csrf_token);
    } catch (loadError) {
      setError(errorMessage(loadError));
    } finally {
      setLoading(false);
    }
  }

  useEffect(() => { void load(); }, [refreshToken]);

  async function revoke(id: string) {
    setActionBusy(`revoke:${id}`);
    setError('');
    try {
      await api(`/v1/auth/sessions/${encodeURIComponent(id)}`, { method: 'DELETE' });
      await load();
    } catch (revokeError) {
      setError(errorMessage(revokeError));
    } finally {
      setActionBusy('');
    }
  }

  async function logoutOthers() {
    setActionBusy('logout-others');
    setError('');
    try {
      await api('/v1/auth/sessions/logout-others', { method: 'POST', body: '{}' });
      await load();
    } catch (logoutError) {
      setError(errorMessage(logoutError));
    } finally {
      setActionBusy('');
    }
  }

  async function logout() {
    setActionBusy('logout');
    setError('');
    try {
      await api('/v1/auth/logout', { method: 'POST', body: '{}' });
      clearCSRFToken();
      window.location.assign('/login');
    } catch (logoutError) {
      setError(errorMessage(logoutError));
      setActionBusy('');
    }
  }

  return (
    <section className="security-panel">
      <header className="security-head"><div><span className="security-icon"><ShieldCheck size={19} /></span><div><h2>{t('Account & sessions')}</h2><p>{t('Manage the current administrator session and signed-in clients')}</p></div></div></header>
      {error && <div className="auth-error">{error}</div>}
      <div className="security-profile"><div><strong>{session?.display_name || session?.username || 'Administrator'}</strong><span>{session?.username || '—'}</span></div><div className="security-actions"><button type="button" onClick={() => window.location.assign('/change-password?return_to=%2Fui%2F%23settings%2Faccount')} disabled={Boolean(actionBusy)}><KeyRound size={15} />{t('Change password')}</button><button type="button" onClick={() => void logout()} disabled={Boolean(actionBusy)}><LogOut size={15} />{actionBusy === 'logout' ? t('Signing out…') : t('Sign out')}</button></div></div>
      <div className="session-toolbar"><div><strong>{t('Active sessions')}</strong><span>{t('{{count}} sessions', { count: sessions.length })}</span></div><button type="button" onClick={() => void logoutOthers()} disabled={Boolean(actionBusy) || sessions.filter((item) => !item.current).length === 0}>{actionBusy === 'logout-others' ? t('Signing out…') : t('Sign out other sessions')}</button></div>
      <div className="session-list">
        {sessions.map((item) => (
          <article className={`session-row ${item.current ? 'is-current' : ''}`} key={item.id}>
            <span className="session-client">{/iOS|Android/.test(item.user_agent_summary) ? <Smartphone size={18} /> : <Laptop size={18} />}</span>
            <div className="session-copy"><div><strong>{item.user_agent_summary || t('Unknown client')}</strong>{item.current && <em>{t('Current')}</em>}</div><span>{item.ip_prefix || t('Unknown network')} · {item.remember_me ? t('Remembered') : t('Browser session')}</span><small><Clock3 size={12} />{t('Last active {{time}}', { time: formatTime(item.last_seen_at, { fallback: t('Unknown') }) })}</small></div>
            {!item.current && <button type="button" className="session-revoke" title={t('Revoke session')} aria-label={t('Revoke {{client}} session', { client: item.user_agent_summary || t('Unknown client') })} onClick={() => void revoke(item.id)} disabled={Boolean(actionBusy)}><Trash2 size={16} /></button>}
          </article>
        ))}
        {!loading && sessions.length === 0 && <p className="session-empty">{t('No active sessions to display.')}</p>}
      </div>
    </section>
  );
}


