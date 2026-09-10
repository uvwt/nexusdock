import { useEffect, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import type { TFunction } from 'i18next';
import { ArrowLeft, BrainCircuit, ChevronRight, CircleAlert, Search, Sparkles } from 'lucide-react';
import { ApiError, api } from '../../api/client';
import { formatTime } from '../../lib/time';

type LifecycleStatus = 'provisional' | 'active' | 'verified' | 'quarantine' | 'retired';
type LifecycleSummary = {
  evolution_id: string;
  title: string;
  statement: string;
  type: string;
  scope: string;
  project: string;
  device?: string;
  status: LifecycleStatus;
  revision: number;
  support_count: number;
  contradict_count: number;
  evidence_count?: number;
  tags?: string[];
  updated_at: string;
};
type LifecycleEvidence = {
  ref: string;
  relation: 'support' | 'contradict';
  task_id?: string;
  review_revision?: string;
  rationale?: string;
  recorded_at: string;
};
type LifecycleDetail = LifecycleSummary & {
  canonical_key?: string;
  policy_version: string;
  source?: string;
  evidence?: LifecycleEvidence[];
  superseded_by?: string;
  created_at: string;
};
type LifecycleListResponse = { records?: LifecycleSummary[]; count?: number };
type LifecycleDetailResponse = { record: LifecycleDetail };
type Stage3Settings = {
  enabled: boolean;
  configured: boolean;
  model: string;
  interval_minutes: number;
};
type AISettingsResponse = { settings?: { stage3?: Stage3Settings } };

type StatusFilter = 'all' | LifecycleStatus;

export default function RecallEvolutionPage({ refreshToken }: { refreshToken: number }) {
  const { t } = useTranslation();
  const [records, setRecords] = useState<LifecycleSummary[]>([]);
  const [stage3, setStage3] = useState<Stage3Settings | null>(null);
  const [query, setQuery] = useState('');
  const [status, setStatus] = useState<StatusFilter>('all');
  const [selectedID, setSelectedID] = useState('');
  const [detail, setDetail] = useState<LifecycleDetail | null>(null);
  const [detailOpen, setDetailOpen] = useState(false);
  const [loading, setLoading] = useState(true);
  const [detailLoading, setDetailLoading] = useState(false);
  const [listError, setListError] = useState('');
  const [settingsError, setSettingsError] = useState('');
  const [detailError, setDetailError] = useState('');

  const statusOptions = useMemo<Array<{ value: StatusFilter; label: string }>>(() => [
    { value: 'all', label: t('All statuses') },
    { value: 'verified', label: t('Verified') },
    { value: 'active', label: t('Active') },
    { value: 'provisional', label: t('Provisional') },
    { value: 'quarantine', label: t('Quarantine') },
    { value: 'retired', label: t('Retired') },
  ], [t]);

  useEffect(() => {
    let cancelled = false;
    setLoading(true);
    setListError('');
    setSettingsError('');

    Promise.allSettled([
      api<LifecycleListResponse>('/v1/evolution/lifecycle'),
      api<AISettingsResponse>('/v1/settings/ai'),
    ]).then(([lifecycleResult, settingsResult]) => {
      if (cancelled) return;
      if (lifecycleResult.status === 'fulfilled') {
        setRecords(lifecycleResult.value.records || []);
      } else {
        setRecords([]);
        setListError(errorMessage(lifecycleResult.reason, t));
      }
      if (settingsResult.status === 'fulfilled') {
        setStage3(settingsResult.value.settings?.stage3 || null);
      } else {
        setStage3(null);
        setSettingsError(errorMessage(settingsResult.reason, t));
      }
      setLoading(false);
    });

    return () => { cancelled = true; };
  }, [refreshToken, t]);

  const filtered = useMemo(() => {
    const needle = query.trim().toLowerCase();
    return [...records]
      .sort((left, right) => right.updated_at.localeCompare(left.updated_at))
      .filter((record) => status === 'all' || record.status === status)
      .filter((record) => {
        if (!needle) return true;
        return [
          record.title, record.statement, record.type, record.project, record.scope,
          record.device, record.evolution_id, ...(record.tags || []),
        ].filter(Boolean).join(' ').toLowerCase().includes(needle);
      });
  }, [query, records, status]);

  useEffect(() => {
    if (filtered.length === 0) {
      setSelectedID('');
      setDetail(null);
      setDetailOpen(false);
      return;
    }
    if (!filtered.some((record) => record.evolution_id === selectedID)) {
      setSelectedID(filtered[0].evolution_id);
    }
  }, [filtered, selectedID]);

  useEffect(() => {
    if (!selectedID) return;
    let cancelled = false;
    setDetail(null);
    setDetailLoading(true);
    setDetailError('');
    api<LifecycleDetailResponse>(`/v1/evolution/lifecycle/${encodeURIComponent(selectedID)}`)
      .then((result) => { if (!cancelled) setDetail(result.record); })
      .catch((error) => {
        if (!cancelled) {
          setDetail(null);
          setDetailError(errorMessage(error, t));
        }
      })
      .finally(() => { if (!cancelled) setDetailLoading(false); });
    return () => { cancelled = true; };
  }, [selectedID, refreshToken, t]);

  const counts = useMemo(() => ({
    total: records.length,
    active: records.filter((record) => record.status === 'active' || record.status === 'verified').length,
    provisional: records.filter((record) => record.status === 'provisional').length,
    quarantine: records.filter((record) => record.status === 'quarantine').length,
  }), [records]);

  const stage3State = !stage3
    ? { label: t('Status unknown'), tone: 'muted' as const }
    : stage3.enabled && stage3.configured
      ? { label: t('Enabled'), tone: 'ok' as const }
      : stage3.enabled
        ? { label: t('Pending configuration'), tone: 'warn' as const }
        : { label: t('Disabled'), tone: 'muted' as const };

  return <section className={`recall-evolution-page ${detailOpen ? 'is-detail-open' : 'is-list-open'}`}>
    <section className="evolution-overview">
      <div className="evolution-overview-copy">
        <span className="evolution-overview-icon"><Sparkles size={18} /></span>
        <div><h2>{t('Evolution')}</h2><p>{t('View lifecycle records, verification evidence, and active status established by AgentDock.')}</p></div>
      </div>
      <div className="evolution-stage3-state">
        <span><small>Stage 3</small><strong>{stage3?.model || t('No model configured')}</strong></span>
        <EvolutionState tone={stage3State.tone}>{stage3State.label}</EvolutionState>
        <button type="button" className="nx-button is-secondary is-small" onClick={() => { window.location.hash = 'settings/ai'; }}>{t('Configure model')}</button>
      </div>
    </section>

    {(listError || settingsError) && <div className="evolution-errors" role="alert">
      <CircleAlert size={16} />
      <span>{[listError, settingsError].filter(Boolean).join(' · ')}</span>
    </div>}

    <section className="evolution-stats" aria-label={t('Evolution status overview')}>
      <EvolutionStat label={t('Lifecycle records')} value={counts.total} />
      <EvolutionStat label={t('Active / Verified')} value={counts.active} tone="ok" />
      <EvolutionStat label={t('Provisional')} value={counts.provisional} tone="info" />
      <EvolutionStat label={t('Quarantine')} value={counts.quarantine} tone={counts.quarantine > 0 ? 'warn' : 'muted'} />
    </section>

    <section className="evolution-browser">
      <aside className="evolution-list-panel">
        <div className="evolution-panel-head"><div><h3>{t('Lifecycle')}</h3><p>{loading ? t('Loading…') : t('{{count}} of {{total}} records', { count: filtered.length, total: records.length })}</p></div></div>
        <div className="evolution-toolbar">
          <label className="evolution-search"><Search size={15} /><input aria-label={t('Search evolution records')} value={query} onChange={(event) => setQuery(event.target.value)} placeholder={t('Search records')} /></label>
          <select aria-label={t('Filter evolution status')} value={status} onChange={(event) => setStatus(event.target.value as StatusFilter)}>{statusOptions.map((option) => <option key={option.value} value={option.value}>{option.label}</option>)}</select>
        </div>
        <div className="evolution-record-list">
          {loading && records.length === 0 && <EvolutionEmpty text={t('Loading evolution records…')} />}
          {!loading && !listError && filtered.length === 0 && <EvolutionEmpty text={records.length === 0 ? t('No evolution records yet.') : t('No matching evolution records.')} />}
          {filtered.map((record) => <button type="button" key={record.evolution_id} className={selectedID === record.evolution_id ? 'is-active' : ''} aria-current={selectedID === record.evolution_id ? 'true' : undefined} onClick={() => { setSelectedID(record.evolution_id); setDetailOpen(true); }}>
            <span className="evolution-record-main"><strong>{recordTitle(record)}</strong><small>{record.project} · {typeLabel(record.type, t)}</small></span>
            <EvolutionStatus status={record.status} t={t} />
            <ChevronRight size={14} />
          </button>)}
        </div>
      </aside>

      <article className="evolution-detail-panel">
        <button type="button" className="evolution-mobile-back" onClick={() => setDetailOpen(false)}><ArrowLeft size={15} />{t('Back to lifecycle')}</button>
        {detailLoading && !detail && <EvolutionEmpty text={t('Loading record details…')} />}
        {detailError && <EvolutionEmpty text={detailError} danger />}
        {!detailLoading && !detailError && !detail && <EvolutionEmpty text={t('Select a record to view verification evidence.')} />}
        {detail && <EvolutionDetail record={detail} t={t} />}
      </article>
    </section>
  </section>;
}

function EvolutionDetail({ record, t }: { record: LifecycleDetail; t: TFunction }) {
  const supportEvidence = (record.evidence || []).filter((item) => item.relation === 'support');
  const contradictEvidence = (record.evidence || []).filter((item) => item.relation === 'contradict');

  return <>
    <header className="evolution-detail-head">
      <div><span className="nexus-eyebrow">{record.project} / {typeLabel(record.type, t)}</span><h3>{recordTitle(record)}</h3><p>{record.statement}</p></div>
      <EvolutionStatus status={record.status} t={t} />
    </header>
    <div className="evolution-detail-meta">
      <EvolutionMeta label={t('Scope')} value={scopeLabel(record.scope, t)} />
      <EvolutionMeta label={t('Device')} value={record.device || t('All devices')} />
      <EvolutionMeta label={t('Revision')} value={`r${record.revision}`} />
      <EvolutionMeta label={t('Updated at')} value={formatTime(record.updated_at, { compact: true })} />
    </div>
    <section className="evolution-evidence-summary">
      <div className="is-support"><span>{t('Supporting evidence')}</span><strong>{record.support_count}</strong></div>
      <div className={record.contradict_count > 0 ? 'is-contradict' : ''}><span>{t('Contradicting evidence')}</span><strong>{record.contradict_count}</strong></div>
      <div><span>{t('Evidence entries')}</span><strong>{record.evidence?.length || 0}</strong></div>
    </section>
    {(record.tags || []).length > 0 && <div className="evolution-tags">{record.tags!.map((tag) => <span key={tag}>{tag}</span>)}</div>}
    <section className="evolution-evidence-list">
      <div className="evolution-section-title"><h4>{t('Verification evidence')}</h4><span>{t('{{support}} support · {{contradict}} contradict', { support: supportEvidence.length, contradict: contradictEvidence.length })}</span></div>
      {(record.evidence || []).length === 0 && <p className="evolution-no-evidence">{t('No evidence entries yet.')}</p>}
      {(record.evidence || []).map((evidence, index) => <article key={`${evidence.ref}-${index}`} className={`evolution-evidence is-${evidence.relation}`}>
        <span className="evolution-evidence-mark" />
        <div><strong>{evidence.relation === 'support' ? t('Support') : t('Contradict')}</strong><p>{evidence.rationale || evidence.ref}</p><small>{[evidence.task_id, evidence.review_revision, formatTime(evidence.recorded_at, { compact: true })].filter(Boolean).join(' · ')}</small></div>
      </article>)}
    </section>
    <details className="evolution-technical">
      <summary>{t('Technical information')}</summary>
      <div>
        <EvolutionMeta label="Evolution ID" value={record.evolution_id} mono />
        <EvolutionMeta label="Policy" value={record.policy_version} />
        <EvolutionMeta label="Canonical Key" value={record.canonical_key || '—'} mono />
        <EvolutionMeta label={t('Source')} value={record.source || t('Not recorded')} />
        <EvolutionMeta label={t('Created at')} value={formatTime(record.created_at, { compact: true })} />
        <EvolutionMeta label={t('Superseded by')} value={record.superseded_by || '—'} mono />
      </div>
    </details>
  </>;
}

function EvolutionStat({ label, value, tone = 'muted' }: { label: string; value: number; tone?: 'ok' | 'warn' | 'info' | 'muted' }) {
  return <div className={`evolution-stat is-${tone}`}><span>{label}</span><strong>{value}</strong></div>;
}

function EvolutionState({ tone, children }: { tone: 'ok' | 'warn' | 'muted'; children: string }) {
  return <span className={`evolution-state is-${tone}`}><i />{children}</span>;
}

function EvolutionStatus({ status, t }: { status: LifecycleStatus; t: TFunction }) {
  return <span className={`evolution-status is-${status}`}><i />{statusLabel(status, t)}</span>;
}

function EvolutionMeta({ label, value, mono = false }: { label: string; value: string; mono?: boolean }) {
  return <div className="evolution-meta"><span>{label}</span><strong className={mono ? 'nx-mono' : ''}>{value}</strong></div>;
}

function EvolutionEmpty({ text, danger = false }: { text: string; danger?: boolean }) {
  return <div className={`evolution-empty ${danger ? 'is-danger' : ''}`}><BrainCircuit size={22} /><span>{text}</span></div>;
}

function recordTitle(record: Pick<LifecycleSummary, 'title' | 'statement'>): string {
  const title = record.title.trim();
  if (title) return title;
  return record.statement.length > 42 ? `${record.statement.slice(0, 42)}…` : record.statement;
}

function errorMessage(error: unknown, t: TFunction): string {
  if (error instanceof ApiError) return error.message;
  return error instanceof Error ? error.message : t('Failed to load evolution data');
}

function statusLabel(status: LifecycleStatus, t: TFunction): string {
  switch (status) {
    case 'verified': return t('Verified');
    case 'active': return t('Active');
    case 'provisional': return t('Provisional');
    case 'quarantine': return t('Quarantine');
    case 'retired': return t('Retired');
  }
}

function typeLabel(type: string, t: TFunction): string {
  const labels: Record<string, string> = {
    preference: t('Preference'),
    user_preference: t('User preference'),
    decision: t('Decision'),
    explicit_decision: t('Explicit decision'),
    constraint: t('Constraint'),
    runbook: t('Runbook'),
    bug_pattern: t('Bug pattern'),
    deploy_note: t('Deploy note'),
    project_trap: t('Project trap'),
    architecture: t('Architecture'),
    anti_pattern: t('Anti-pattern'),
    operational_lesson: t('Operational lesson'),
    technical_fact: t('Technical fact'),
    workflow_template: t('Workflow template'),
    skill: t('Skill'),
  };
  return labels[type] || type || t('Uncategorized');
}

function scopeLabel(scope: string, t: TFunction): string {
  const labels: Record<string, string> = {
    project: t('Project'),
    device: t('Device'),
    user: t('User'),
    shared: t('Shared'),
    global: t('Global'),
    local_only: t('Local only'),
  };
  return labels[scope] || scope || t('Not recorded');
}
