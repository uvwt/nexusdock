import { useEffect, useState, type FormEvent } from 'react';
import { useTranslation } from 'react-i18next';
import type { TFunction } from 'i18next';
import { Activity, BrainCircuit, DatabaseZap, RefreshCw, Save, SearchCheck } from 'lucide-react';
import { ApiError, api } from '../../api/client';

type SecretForm = { value: string; clear: boolean };
type EmbeddingSettings = {
  enabled: boolean;
  endpoint: string;
  model: string;
  timeout_seconds: number;
  api_key_configured: boolean;
};
type Stage3Settings = {
  enabled: boolean;
  endpoint: string;
  model: string;
  timeout_seconds: number;
  interval_minutes: number;
  api_key_configured: boolean;
  configured: boolean;
};
type RuntimeAISettings = {
  embedding: EmbeddingSettings;
  stage3: Stage3Settings;
  persisted: boolean;
  updated_at?: string;
};
type SettingsResponse = { ok: boolean; settings: RuntimeAISettings };
type ConnectionTestResult = {
  ok: boolean;
  target: 'stage3' | 'embedding';
  model?: string;
  message: string;
  latency_ms: number;
};
type EmbeddingStatus = {
  ok: boolean;
  enabled: boolean;
  configured: boolean;
  reachable?: boolean;
  model?: string;
  error?: string;
  reason?: string;
  index?: { count?: number; dimension?: number; updated_at?: string };
};

type FormState = {
  embedding: EmbeddingSettings;
  stage3: Stage3Settings;
};

const emptySettings: RuntimeAISettings = {
  embedding: { enabled: false, endpoint: '', model: 'BAAI/bge-m3', timeout_seconds: 30, api_key_configured: false },
  stage3: { enabled: false, endpoint: '', model: '', timeout_seconds: 60, interval_minutes: 360, api_key_configured: false, configured: false },
  persisted: false,
};

function errorMessage(error: unknown, t: TFunction): string {
  if (error instanceof ApiError) return error.message;
  return error instanceof Error ? error.message : t('Request failed');
}

function secretAction(secret: SecretForm) {
  if (secret.clear) return { action: 'clear' };
  if (secret.value.trim()) return { action: 'replace', value: secret.value.trim() };
  return { action: 'keep' };
}

export default function AISettingsPanel({ refreshToken }: { refreshToken: number }) {
  const { t } = useTranslation();
  const [form, setForm] = useState<FormState>({ embedding: emptySettings.embedding, stage3: emptySettings.stage3 });
  const [embeddingSecret, setEmbeddingSecret] = useState<SecretForm>({ value: '', clear: false });
  const [stage3Secret, setStage3Secret] = useState<SecretForm>({ value: '', clear: false });
  const [embeddingStatus, setEmbeddingStatus] = useState<EmbeddingStatus | null>(null);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [reindexing, setReindexing] = useState(false);
  const [testingTarget, setTestingTarget] = useState<'stage3' | 'embedding' | null>(null);
  const [stage3Test, setStage3Test] = useState<ConnectionTestResult | null>(null);
  const [embeddingTest, setEmbeddingTest] = useState<ConnectionTestResult | null>(null);
  const [notice, setNotice] = useState<{ tone: 'success' | 'error' | 'info'; text: string } | null>(null);

  async function refreshEmbeddingStatus() {
    try {
      setEmbeddingStatus(await api<EmbeddingStatus>('/v1/embeddings/status', { timeoutMs: 35_000 }));
    } catch (error) {
      setEmbeddingStatus({ ok: false, enabled: form.embedding.enabled, configured: false, reachable: false, error: errorMessage(error, t) });
    }
  }

  async function load() {
    setLoading(true);
    try {
      const settingsResult = await api<SettingsResponse>('/v1/settings/ai');
      setForm({ embedding: settingsResult.settings.embedding, stage3: settingsResult.settings.stage3 });
      setEmbeddingSecret({ value: '', clear: false });
      setStage3Secret({ value: '', clear: false });
      setStage3Test(null);
      setEmbeddingTest(null);
      setNotice(null);
      void refreshEmbeddingStatus();
    } catch (error) {
      setNotice({ tone: 'error', text: errorMessage(error, t) });
    } finally {
      setLoading(false);
    }
  }

  useEffect(() => { void load(); }, [refreshToken]);

  async function submit(event: FormEvent) {
    event.preventDefault();
    setSaving(true);
    setNotice(null);
    try {
      const result = await api<SettingsResponse>('/v1/settings/ai', {
        method: 'PUT',
        body: JSON.stringify({
          embedding: {
            enabled: form.embedding.enabled,
            endpoint: form.embedding.endpoint.trim(),
            model: form.embedding.model.trim(),
            timeout_seconds: form.embedding.timeout_seconds,
            api_key: secretAction(embeddingSecret),
          },
          stage3: {
            enabled: form.stage3.enabled,
            endpoint: form.stage3.endpoint.trim(),
            model: form.stage3.model.trim(),
            timeout_seconds: form.stage3.timeout_seconds,
            interval_minutes: form.stage3.interval_minutes,
            api_key: secretAction(stage3Secret),
          },
        }),
      });
      setForm({ embedding: result.settings.embedding, stage3: result.settings.stage3 });
      setEmbeddingSecret({ value: '', clear: false });
      setStage3Secret({ value: '', clear: false });
      setStage3Test(null);
      setEmbeddingTest(null);
      setNotice({ tone: 'success', text: t('Configuration saved and applied. Nexus does not need to restart.') });
      void refreshEmbeddingStatus();
    } catch (error) {
      setNotice({ tone: 'error', text: errorMessage(error, t) });
    } finally {
      setSaving(false);
    }
  }

  async function reindex() {
    setReindexing(true);
    setNotice(null);
    try {
      await api('/v1/embeddings/reindex', { method: 'POST', body: '{}' , timeoutMs: 120_000 });
      await api('/v1/workflow-templates/reindex', { method: 'POST', timeoutMs: 120_000 });
      setNotice({ tone: 'success', text: t('Recall and Workflow vector indexes were rebuilt.') });
      void refreshEmbeddingStatus();
    } catch (error) {
      setNotice({ tone: 'error', text: errorMessage(error, t) });
    } finally {
      setReindexing(false);
    }
  }

  async function testConnection(target: 'stage3' | 'embedding') {
    setTestingTarget(target);
    const setResult = target === 'stage3' ? setStage3Test : setEmbeddingTest;
    setResult(null);
    try {
      const result = await api<ConnectionTestResult>(`/v1/settings/ai/test/${target}`, {
        method: 'POST',
        timeoutMs: 310_000,
      });
      setResult(result);
    } catch (error) {
      setResult({ ok: false, target, message: errorMessage(error, t), latency_ms: 0 });
    } finally {
      setTestingTarget(null);
    }
  }

  const reachableTone = embeddingStatus?.reachable === true ? 'is-ok' : embeddingStatus?.enabled ? 'is-warn' : 'is-muted';
  const stage3State = form.stage3.enabled ? (form.stage3.configured ? t('Enabled') : t('Waiting for complete configuration')) : t('Disabled');
  const embeddingState = embeddingStatus?.enabled ? (embeddingStatus.reachable ? t('Service available') : t('Service unreachable')) : t('Disabled');
  const embeddingMeta = embeddingStatus?.enabled
    ? (embeddingStatus.index ? t('Index {{count}} items · {{dimension}} dimensions', { count: embeddingStatus.index.count ?? 0, dimension: embeddingStatus.index.dimension ?? 0 }) : t('Waiting for index status'))
    : t('Recall and Workflow are not using vector search');

  return <section className="ai-settings-panel">
    <header className="ai-settings-heading">
      <div><span className="nexus-eyebrow">AI & VECTOR</span><h2>{t('Models & vector search')}</h2><p>{t('Manage the Stage 3 model and shared Embedding service. Changes are saved together and applied immediately.')}</p></div>
      <button type="button" className="nx-button is-secondary" onClick={() => void load()} disabled={loading || saving}><RefreshCw size={15} />{t('Refresh')}</button>
    </header>
    {notice && <div className={`nx-alert is-${notice.tone}`}>{notice.text}</div>}

    <form className="ai-settings-form-page" onSubmit={submit}>
      <section className="ai-config-section">
        <header className="ai-config-head">
          <div className="ai-config-title"><span className="nexus-panel-icon"><BrainCircuit size={17} /></span><div><h3>{t('Stage 3 model')}</h3><p>{t('Provides low-frequency semantic gap filling across Task and Evolution, submitting only candidates for AgentDock to decide.')}</p></div></div>
          <label className="ai-switch-row">
            <input type="checkbox" checked={form.stage3.enabled} onChange={(event) => setForm({ ...form, stage3: { ...form.stage3, enabled: event.target.checked } })} />
            <span><strong>{stage3State}</strong><small>{form.stage3.enabled ? t('External model calls are allowed') : t('External models will not be called')}</small></span>
          </label>
        </header>
        <div className="ai-config-body">
          <div className="ai-field-grid ai-stage3-fields">
            <label className="ai-field is-wide"><span>{t('Chat Completions endpoint')}</span><input type="url" required={form.stage3.enabled} value={form.stage3.endpoint} onChange={(event) => setForm({ ...form, stage3: { ...form.stage3, endpoint: event.target.value } })} placeholder="https://api.example.com/v1/chat/completions" /></label>
            <label className="ai-field"><span>{t('Model')}</span><input required={form.stage3.enabled} value={form.stage3.model} onChange={(event) => setForm({ ...form, stage3: { ...form.stage3, model: event.target.value } })} placeholder="gpt-5-mini" /></label>
            <label className="ai-field"><span>{t('Request timeout (seconds)')}</span><input type="number" min={1} max={300} value={form.stage3.timeout_seconds} onChange={(event) => setForm({ ...form, stage3: { ...form.stage3, timeout_seconds: Number(event.target.value) } })} /></label>
            <label className="ai-field"><span>{t('Run interval (minutes)')}</span><input type="number" min={60} max={10080} value={form.stage3.interval_minutes} onChange={(event) => setForm({ ...form, stage3: { ...form.stage3, interval_minutes: Number(event.target.value) } })} /></label>
            <label className="ai-field is-wide"><span>API Key {form.stage3.api_key_configured ? t('· Configured; leave blank to keep') : t('· Not configured')}</span><input type="password" autoComplete="new-password" disabled={stage3Secret.clear} value={stage3Secret.value} onChange={(event) => setStage3Secret({ value: event.target.value, clear: false })} placeholder={form.stage3.api_key_configured ? '••••••••' : t('Optional')} /></label>
            {form.stage3.api_key_configured && <label className="ai-clear-secret"><input type="checkbox" checked={stage3Secret.clear} onChange={(event) => setStage3Secret({ value: '', clear: event.target.checked })} /><span>{t('Clear saved API Key')}</span></label>}
          </div>
          <div className="ai-config-actions"><p>{t('Connection tests use the currently saved server configuration. Save form changes first.')}</p><button type="button" className="nx-button is-secondary" disabled={loading || saving || testingTarget !== null} onClick={() => void testConnection('stage3')}><Activity size={15} />{testingTarget === 'stage3' ? t('Testing…') : t('Test connection')}</button></div>
          {stage3Test && <div className={`nx-alert is-${stage3Test.ok ? 'success' : 'error'}`}>{stage3Test.message}{stage3Test.latency_ms > 0 ? ` · ${stage3Test.latency_ms} ms` : ''}</div>}
        </div>
      </section>

      <section className="ai-config-section">
        <header className="ai-config-head">
          <div className="ai-config-title"><span className="nexus-panel-icon"><DatabaseZap size={17} /></span><div><h3>{t('Vector search')}</h3><p>{t('Shared by Recall semantic search and Workflow template matching, compatible with the OpenAI Embeddings API.')}</p></div></div>
          <div className="ai-config-head-actions">
            <div className="ai-service-status"><span className={`ai-status-dot ${reachableTone}`} /><span><strong>{embeddingState}</strong><small>{embeddingMeta}</small></span></div>
            <label className="ai-switch-row">
              <input type="checkbox" checked={form.embedding.enabled} onChange={(event) => setForm({ ...form, embedding: { ...form.embedding, enabled: event.target.checked } })} />
              <span><strong>{form.embedding.enabled ? t('Enabled') : t('Disabled')}</strong><small>{t('Shared by Recall and Workflow')}</small></span>
            </label>
          </div>
        </header>
        <div className="ai-config-body">
          <div className="ai-field-grid ai-embedding-fields">
            <label className="ai-field is-wide"><span>{t('Embeddings endpoint')}</span><input type="url" required={form.embedding.enabled} value={form.embedding.endpoint} onChange={(event) => setForm({ ...form, embedding: { ...form.embedding, endpoint: event.target.value } })} placeholder="http://embedding-service:8000/v1/embeddings" /></label>
            <label className="ai-field"><span>{t('Embedding model')}</span><input required={form.embedding.enabled} value={form.embedding.model} onChange={(event) => setForm({ ...form, embedding: { ...form.embedding, model: event.target.value } })} placeholder="BAAI/bge-m3" /></label>
            <label className="ai-field"><span>{t('Request timeout (seconds)')}</span><input type="number" min={1} max={300} value={form.embedding.timeout_seconds} onChange={(event) => setForm({ ...form, embedding: { ...form.embedding, timeout_seconds: Number(event.target.value) } })} /></label>
            <label className="ai-field is-wide"><span>API Key {form.embedding.api_key_configured ? t('· Configured; leave blank to keep') : t('· Not configured')}</span><input type="password" autoComplete="new-password" disabled={embeddingSecret.clear} value={embeddingSecret.value} onChange={(event) => setEmbeddingSecret({ value: event.target.value, clear: false })} placeholder={form.embedding.api_key_configured ? '••••••••' : t('Local Embedding can leave this blank')} /></label>
            {form.embedding.api_key_configured && <label className="ai-clear-secret"><input type="checkbox" checked={embeddingSecret.clear} onChange={(event) => setEmbeddingSecret({ value: '', clear: event.target.checked })} /><span>{t('Clear saved API Key')}</span></label>}
          </div>
          <div className="ai-config-actions"><p>{t('Rebuilding indexes regenerates vector data for Recall and Workflow.')}</p><div><button type="button" className="nx-button is-secondary" disabled={loading || saving || testingTarget !== null} onClick={() => void testConnection('embedding')}><Activity size={15} />{testingTarget === 'embedding' ? t('Testing…') : t('Test connection')}</button><button type="button" className="nx-button is-secondary" disabled={!form.embedding.enabled || reindexing || saving || testingTarget !== null} onClick={() => void reindex()}><SearchCheck size={15} />{reindexing ? t('Rebuilding…') : t('Rebuild indexes')}</button></div></div>
          {embeddingTest && <div className={`nx-alert is-${embeddingTest.ok ? 'success' : 'error'}`}>{embeddingTest.message}{embeddingTest.latency_ms > 0 ? ` · ${embeddingTest.latency_ms} ms` : ''}</div>}
          {embeddingStatus?.error && <div className="nx-alert is-error">{embeddingStatus.error}</div>}
        </div>
      </section>

      <footer className="ai-save-bar"><span>{t('API Keys only return a configured state; plaintext is never read back from the server.')}</span><button type="submit" className="nx-button" disabled={loading || saving}><Save size={15} />{saving ? t('Saving…') : t('Save and apply')}</button></footer>
    </form>
  </section>;
}
