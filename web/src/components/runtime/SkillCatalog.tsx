import { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { api } from '../../api/client';
import './skill-catalog.css';

type Usage = { environment: string; setup: string; example: string; verification: string };
type Metadata = { source: string; platforms: string[] | null; dependencies: string; portability: string; usage: Usage; review_note: string };
const blankUsage: Usage = { environment: '', setup: '', example: '', verification: '' };
type Entry = {
  name: string; version: string; description: string; sha256: string; size_bytes: number;
  files: string[]; created_at: string;
  metadata: Metadata; constraints: string[];
};
type NodeResult = { ok: boolean; status: string; digest?: string };

export default function SkillCatalog({ nodeID, online, refreshToken }: { nodeID?: string; online?: boolean; refreshToken: number }) {
  const { t } = useTranslation();
  const [items, setItems] = useState<Entry[]>([]);
  const [query, setQuery] = useState('');
  const [selected, setSelected] = useState('');
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  const [loading, setLoading] = useState(true);
  const [revision, setRevision] = useState(0);
  const [file, setFile] = useState<File>();
  const [source, setSource] = useState('');
  const [dependencies, setDependencies] = useState('');
  const [platforms, setPlatforms] = useState<string[]>([]);
  const [portability, setPortability] = useState('unreviewed');
  const [usage, setUsage] = useState<Usage>(blankUsage);
  const [reviewNote, setReviewNote] = useState('');
  const [result, setResult] = useState<NodeResult>();
  const [notice, setNotice] = useState('');
  const key = (e: Entry) => `${e.name}/${e.version}`;
  const entry = items.find(e => key(e) === selected);
  useEffect(() => {
    const controller = new AbortController();
    setLoading(true);
    api<{ items: Entry[] }>('/v1/skill-catalog', { signal: controller.signal }).then(r => {
      setItems(r.items); setError('');
    }).catch(e => { if (!controller.signal.aborted) setError(String(e.message || e)); }).finally(() => { if (!controller.signal.aborted) setLoading(false); });
    return () => controller.abort();
  }, [refreshToken, revision]);
  useEffect(() => { setResult(undefined); }, [nodeID, selected]);
  async function upload(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault(); if (!file) return;
    setBusy(true); setError(''); setNotice('');
    try {
      const body = new FormData(); body.append('package', file);
      body.append('metadata', JSON.stringify({ source, dependencies, platforms, portability, usage, review_note: reviewNote }));
      const r = await api<{ entry: Entry }>('/v1/skill-catalog', { method: 'POST', body, timeoutMs: 120000 });
      setSelected(key(r.entry)); setRevision(v => v + 1); setNotice(t('Package stored. Select a node to validate it.'));
    } catch(e) { setError(e instanceof Error ? e.message : String(e)); } finally { setBusy(false); }
  }
  async function distribute(action: 'validate' | 'install') {
    if (!entry || !nodeID) return;
    setBusy(true); setError('');
    const operationKey = `${selected}:${nodeID}`;
    try {
      const r = await api<NodeResult>(`/v1/skill-catalog/${encodeURIComponent(entry.name)}/${encodeURIComponent(entry.version)}/nodes/${encodeURIComponent(nodeID)}`, {
        method: 'POST', body: JSON.stringify({ action, digest: result?.digest }), timeoutMs: 130000,
      });
      // 选择器操作期间禁用当前面板；结果始终说明它对应的节点。
      setResult(r); setNotice(`${operationKey} — ${statusLabel(r.status)}`);
    } catch(e) { setResult(undefined); setError(e instanceof Error ? e.message : String(e)); } finally { setBusy(false); }
  }
  function statusLabel(status: string) {
    switch(status) {
      case 'validated': return t('Validated; ready to install');
      case 'installed_dependencies_unverified': return t('Installed and active; dependencies and behavior still need verification');
      case 'installation_unverified': return t('Installation returned; active version could not be verified');
      default: return status;
    }
  }
  const filtered = items.filter(e => `${e.name} ${e.description} ${e.version}`.toLowerCase().includes(query.toLowerCase()));
  return <div className="skill-catalog">
    <header><h2>{t('Central Skill library')}</h2><p>{t('Store once, keep version history, install on compatible nodes when needed.')}</p></header>
    {error && <div role="alert" className="nx-alert is-error">{error}</div>}
    {notice && <p role="status">{notice}</p>}
    <details className="catalog-upload"><summary>{t('Upload Skill package')}</summary>
      <form onSubmit={upload}><fieldset disabled={busy}>
        <label>{t('ZIP or tar.gz, up to 32 MiB')}<input type="file" accept=".zip,.tar.gz,.tgz" required onChange={e => setFile(e.target.files?.[0])} /></label>
        <p>{t('Include SKILL.md with name, description and semantic version, plus referenced files. Exclude credentials and runtime data.')}</p>
        <label>{t('Package source')}<input value={source} maxLength={2048} onChange={e => setSource(e.target.value)} /></label>
        <label>{t('Dependencies and setup')}<textarea value={dependencies} maxLength={8192} onChange={e => setDependencies(e.target.value)} /></label>
        <div>{t('Supported platforms (empty means unspecified)')}{['darwin', 'linux', 'windows'].map(p => <label className="catalog-platform" key={p}><input type="checkbox" checked={platforms.includes(p)} onChange={e => setPlatforms(v => e.target.checked ? [...v, p] : v.filter(x => x !== p))} />{p === 'darwin' ? 'macOS' : p}</label>)}</div>
        <UsageFields portability={portability} setPortability={setPortability} usage={usage} setUsage={setUsage} reviewNote={reviewNote} setReviewNote={setReviewNote} />
        <button className="nx-button" disabled={!file || busy}>{busy ? t('Uploading…') : t('Upload Skill package')}</button>
      </fieldset></form>
    </details>
    <label>{t('Search Skill')}<input type="search" value={query} onChange={e => setQuery(e.target.value)} /></label>
    {loading && <p role="status">{t('Loading Skill library…')}</p>}
    <div className="catalog-layout"><nav aria-label={t('Skill versions')}>
      {!loading && !filtered.length && <p>{t('No matching skills.')}</p>}
      {filtered.map(e => <button disabled={busy} key={key(e)} aria-pressed={selected === key(e)} onClick={() => setSelected(key(e))}><strong>{e.name}</strong><span>{e.version} · {(e.size_bytes / 1024).toFixed(1)} KiB</span></button>)}
    </nav><section aria-label={t('Skill summary')}>
      {!entry ? <p>{t('Please select a Skill.')}</p> : <>
        <h3>{entry.name} <small>{entry.version}</small></h3><p>{entry.description}</p>
        <p><strong>{entry.metadata.portability === 'general' ? t('General document workflow') : entry.metadata.portability === 'environment_bound' ? t('Environment-specific Skill') : t('Not reviewed')}</strong></p>
        {entry.metadata.review_note && <p>{t('Portability review evidence')}: {entry.metadata.review_note}</p>}
        {entry.constraints.length > 0 && <p>{t('Environment or executable dependencies detected; inspect the usage before installation.')}</p>}
        {entry.metadata.portability === 'environment_bound' && <dl><dt>{t('Required environment')}</dt><dd>{entry.metadata.usage.environment}</dd><dt>{t('Setup instructions')}</dt><dd>{entry.metadata.usage.setup}</dd><dt>{t('Invocation example')}</dt><dd>{entry.metadata.usage.example}</dd><dt>{t('Verification steps')}</dt><dd>{entry.metadata.usage.verification}</dd></dl>}
        <UsageReview key={key(entry) + revision} entry={entry} onSaved={() => { setResult(undefined); setRevision(v => v + 1); }} />
        <dl><dt>SHA-256</dt><dd><code>{entry.sha256}</code></dd><dt>{t('Package source')}</dt><dd>{entry.metadata.source || '—'}</dd><dt>{t('Dependencies and setup')}</dt><dd>{entry.metadata.dependencies || t('Not specified; verify before use')}</dd><dt>{t('Platforms')}</dt><dd>{entry.metadata.platforms?.join(', ') || t('Not specified; verify before use')}</dd></dl>
        <a href={`/v1/skill-catalog/${encodeURIComponent(entry.name)}/${encodeURIComponent(entry.version)}/download`}>{t('Download package')}</a>
        <details><summary>{t('Package files')} ({entry.files.length})</summary><ul>{entry.files.map(f => <li key={f}><code>{f}</code></li>)}</ul></details>
        <CatalogFilePreview key={key(entry)} entry={entry} />
        <div className="catalog-actions"><button className="nx-button" disabled={busy || !nodeID || !online} onClick={() => distribute('validate')}>{t('Validate on selected node')}</button><button className="nx-button" disabled={busy || !nodeID || !online || result?.status !== 'validated' || entry.metadata.portability === 'unreviewed'} onClick={() => distribute('install')}>{t('Install validated version')}</button></div>
        {busy && <p role="status">{t('Processing package…')}</p>}
        {!online && <p>{t('Select an online node to install. Library browsing and downloads remain available.')}</p>}
        <p>{t('Uploading does not execute the Skill. Installation does not configure dependencies or credentials.')}</p>
      </>}
    </section></div>
  </div>;
}

function CatalogFilePreview({ entry }: { entry: Entry }) {
  const { t } = useTranslation();
  const [path, setPath] = useState('SKILL.md');
  const [content, setContent] = useState('');
  const [error, setError] = useState('');
  useEffect(() => {
    const controller = new AbortController(); setContent(''); setError('');
    api<{ content: string }>(`/v1/skill-catalog/${encodeURIComponent(entry.name)}/${encodeURIComponent(entry.version)}/files/${path.split('/').map(encodeURIComponent).join('/')}`, { signal: controller.signal })
      .then(r => setContent(r.content)).catch(e => { if (!controller.signal.aborted) setError(String(e.message || e)); });
    return () => controller.abort();
  }, [entry.name, entry.version, path]);
  return <details><summary>{t('Read Skill instructions')}</summary><label>{t('File')}<select value={path} onChange={e => setPath(e.target.value)}>{entry.files.map(f => <option key={f} value={f}>{f}</option>)}</select></label>{error && <p role="alert">{error}</p>}<pre className="catalog-file-preview">{content}</pre></details>;
}

function UsageFields({ portability, setPortability, usage, setUsage, reviewNote, setReviewNote }: { portability: string; setPortability: (v: string) => void; usage: Usage; setUsage: (v: Usage) => void; reviewNote: string; setReviewNote: (v: string) => void }) {
  const { t } = useTranslation();
  return <><label>{t('Portability classification')}<select value={portability} onChange={e => setPortability(e.target.value)}><option value="unreviewed">{t('Not reviewed')}</option><option value="general">{t('General document workflow')}</option><option value="environment_bound">{t('Environment-specific Skill')}</option></select></label>
    <p>{t('General means a document-only workflow without client, host, platform or dependency bindings. Code packages require environment-specific usage.')}</p>
    {portability === 'general' && <label>{t('Portability review evidence')}<textarea required maxLength={8192} value={reviewNote} onChange={e => setReviewNote(e.target.value)} /><span>{t('Record your review of all instructions and references. Automated checks alone cannot prove portability.')}</span></label>}
    {portability === 'environment_bound' && <>
      <label>{t('Required environment')}<textarea required maxLength={8192} value={usage.environment} onChange={e => setUsage({ ...usage, environment: e.target.value })} /></label>
      <label>{t('Setup instructions')}<textarea required maxLength={8192} value={usage.setup} onChange={e => setUsage({ ...usage, setup: e.target.value })} /></label>
      <label>{t('Invocation example')}<textarea required maxLength={8192} value={usage.example} onChange={e => setUsage({ ...usage, example: e.target.value })} /></label>
      <label>{t('Verification steps')}<textarea required maxLength={8192} value={usage.verification} onChange={e => setUsage({ ...usage, verification: e.target.value })} /></label>
    </>}
  </>;
}

function UsageReview({ entry, onSaved }: { entry: Entry; onSaved: () => void }) {
  const { t } = useTranslation();
  const [metadata, setMetadata] = useState<Metadata>(entry.metadata);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  async function save(event: React.FormEvent) {
    event.preventDefault(); setBusy(true); setError('');
    try { await api(`/v1/skill-catalog/${encodeURIComponent(entry.name)}/${encodeURIComponent(entry.version)}`, { method: 'PUT', body: JSON.stringify(metadata) }); onSaved(); }
    catch(e) { setError(e instanceof Error ? e.message : String(e)); } finally { setBusy(false); }
  }
  return <details><summary>{t('Review classification and usage')}</summary><form onSubmit={save}><fieldset disabled={busy}>
    <UsageFields portability={metadata.portability} setPortability={v => setMetadata(m => ({ ...m, portability: v }))} usage={metadata.usage} setUsage={v => setMetadata(m => ({ ...m, usage: v }))} reviewNote={metadata.review_note} setReviewNote={v => setMetadata(m => ({ ...m, review_note: v }))} />
    {error && <p role="alert">{error}</p>}<button className="nx-button">{t('Save usage review')}</button>
  </fieldset></form></details>;
}
