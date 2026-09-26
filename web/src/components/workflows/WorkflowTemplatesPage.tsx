import { useEffect, useMemo, useRef, useState, type ReactNode } from 'react';
import { useTranslation } from 'react-i18next';
import type { TFunction } from 'i18next';
import { ArrowLeft, Check, ChevronRight, Copy, FileJson, History, Search } from 'lucide-react';
import { api } from '../../api/client';
import { formatTime } from '../../lib/time';
import MobileDrilldownBar from '../MobileDrilldownBar';

type WorkflowStatus = 'active' | 'retired';
type Tone = 'ok' | 'warn' | 'danger' | 'info' | 'muted';
type DetailMode = 'current' | 'history' | 'history-detail';

type WorkflowTemplateSummary = {
  id: string;
  version: string;
  title: string;
  description?: string;
  status: WorkflowStatus;
  file_name: string;
  path: string;
  size_bytes: number;
  updated_at: string;
  step_count: number;
  keywords?: string[];
  version_count?: number;
  active_count?: number;
  retired_count?: number;
  has_conflict?: boolean;
};

type WorkflowTemplateDetail = WorkflowTemplateSummary & {
  content: string;
  json?: Record<string, unknown>;
};

type ListResponse = { ok: boolean; items: WorkflowTemplateSummary[]; count: number };
type DetailResponse = { ok: boolean; template: Record<string, unknown>; template_summary: WorkflowTemplateSummary };
type Notice = { tone: Tone; text: string };
type StepView = { id: string; title: string; phase: string; required: boolean; depends: string[]; substitution: string };
type StepGroup = { phase: string; steps: StepView[] };
type MatchView = { label: string; values: string[] };

function statusLabel(status: WorkflowStatus, t: TFunction): string {
  return status === 'active' ? t('Current version') : t('Historical version');
}

function statusTone(template?: Pick<WorkflowTemplateSummary, 'status'>): Tone {
  if (!template) return 'muted';
  return template.status === 'active' ? 'info' : 'muted';
}

function templateDisplayTitle(template?: Pick<WorkflowTemplateSummary, 'title' | 'id' | 'file_name'>, t?: TFunction): string {
  return template?.title?.trim() || template?.id || template?.file_name || (t ? t('Untitled template') : 'Untitled template');
}

function templateListMeta(template: WorkflowTemplateSummary, t: TFunction): string {
  return [`v${template.version || '—'}`, t('{{count}} steps', { count: template.step_count || 0 }), t('{{count}} versions', { count: template.version_count ?? 1 })].join(' · ');
}

function sortTemplateVersions(items: WorkflowTemplateSummary[]): WorkflowTemplateSummary[] {
  return [...items].sort((left, right) => {
    if (left.status !== right.status) return left.status === 'active' ? -1 : 1;
    return right.version.localeCompare(left.version, undefined, { numeric: true, sensitivity: 'base' });
  });
}

function parseTemplate(content: string, t: TFunction): { body: Record<string, unknown>; id: string; version: string; title: string; description: string; stepCount: number; error?: string } {
  try {
    const body = JSON.parse(content || '{}') as Record<string, unknown>;
    const id = text(body.id);
    const version = text(body.version);
    const title = text(body.title);
    const description = text(body.description);
    const steps = array(body.steps).length;
    if (!id || !version) return { body, id, version, title, description, stepCount: steps, error: t('JSON must include id and version.') };
    return { body, id, version, title, description, stepCount: steps };
  } catch (error) {
    return { body: {}, id: '', version: '', title: '', description: '', stepCount: 0, error: error instanceof Error ? error.message : t('Failed to parse JSON') };
  }
}

export default function WorkflowTemplatesPage({ refreshToken }: { refreshToken: number }) {
  const { t } = useTranslation();
  const [items, setItems] = useState<WorkflowTemplateSummary[]>([]);
  const [query, setQuery] = useState('');
  const [selectedCurrent, setSelectedCurrent] = useState<WorkflowTemplateDetail | null>(null);
  const [selectedHistory, setSelectedHistory] = useState<WorkflowTemplateDetail | null>(null);
  const [historyVersions, setHistoryVersions] = useState<WorkflowTemplateSummary[]>([]);
  const [detailMode, setDetailMode] = useState<DetailMode>('current');
  const [mobileDetailOpen, setMobileDetailOpen] = useState(false);
  const [loading, setLoading] = useState(true);
  const [historyLoading, setHistoryLoading] = useState(false);
  const [notice, setNotice] = useState<Notice | null>(null);

  const loadListRef = useRef(loadList);
  loadListRef.current = loadList;
  useEffect(() => { void loadListRef.current(); }, [refreshToken]);

  const filtered = useMemo(() => {
    const needle = query.trim().toLowerCase();
    if (!needle) return items;
    return items.filter((item) => [item.id, item.version, item.title, item.description, ...(item.keywords || [])].filter(Boolean).join(' ').toLowerCase().includes(needle));
  }, [items, query]);

  const visibleDetail = detailMode === 'history-detail' ? selectedHistory : selectedCurrent;
  const parsed = useMemo(() => parseTemplate(visibleDetail?.content || '', t), [visibleDetail, t]);

  async function loadList() {
    setLoading(true);
    setNotice(null);
    try {
      // 默认列表接口就是“当前视图”。历史版本只在用户进入某个模板的历史页时按需读取。
      const result = await api<ListResponse>('/v1/workflow-templates');
      const nextItems = (result.items || []).filter((item) => item.status === 'active');
      setItems(nextItems);
      const target = nextItems.find((item) => item.id === selectedCurrent?.id) || nextItems[0];
      if (target) await openCurrentTemplate(target);
      else {
        setSelectedCurrent(null);
        setSelectedHistory(null);
        setHistoryVersions([]);
        setDetailMode('current');
      }
    } catch (error) {
      setNotice({ tone: 'danger', text: error instanceof Error ? error.message : t('Failed to load workflow templates') });
    } finally {
      setLoading(false);
    }
  }

  async function readTemplate(template: WorkflowTemplateSummary, aggregate: WorkflowTemplateSummary = template): Promise<WorkflowTemplateDetail> {
    const result = await api<DetailResponse>(`/v1/workflow-templates/${encodeURIComponent(template.id)}/${encodeURIComponent(template.version)}`);
    return {
      ...result.template_summary,
      version_count: aggregate.version_count ?? result.template_summary.version_count,
      active_count: aggregate.active_count ?? result.template_summary.active_count,
      retired_count: aggregate.retired_count ?? result.template_summary.retired_count,
      has_conflict: aggregate.has_conflict ?? result.template_summary.has_conflict,
      content: JSON.stringify(result.template, null, 2),
      json: result.template,
    };
  }

  async function openCurrentTemplate(template: WorkflowTemplateSummary, revealOnMobile = false) {
    setNotice(null);
    try {
      const detail = await readTemplate(template);
      setSelectedCurrent(detail);
      setSelectedHistory(null);
      setHistoryVersions([]);
      setDetailMode('current');
      if (revealOnMobile) setMobileDetailOpen(true);
    } catch (error) {
      setNotice({ tone: 'danger', text: error instanceof Error ? error.message : t('Failed to load template details') });
    }
  }

  async function openHistory() {
    if (!selectedCurrent) return;
    setHistoryLoading(true);
    setNotice(null);
    try {
      const queryValue = encodeURIComponent(selectedCurrent.id);
      const result = await api<ListResponse>(`/v1/workflow-templates?include_history=true&q=${queryValue}`);
      const versions = sortTemplateVersions((result.items || []).filter((item) => item.id === selectedCurrent.id));
      setHistoryVersions(versions);
      setSelectedHistory(null);
      setDetailMode('history');
    } catch (error) {
      setNotice({ tone: 'danger', text: error instanceof Error ? error.message : t('Failed to load historical versions') });
    } finally {
      setHistoryLoading(false);
    }
  }

  async function openHistoryVersion(template: WorkflowTemplateSummary) {
    if (template.status === 'active') {
      setSelectedHistory(null);
      setDetailMode('current');
      return;
    }
    setNotice(null);
    try {
      setSelectedHistory(await readTemplate(template, selectedCurrent || template));
      setDetailMode('history-detail');
    } catch (error) {
      setNotice({ tone: 'danger', text: error instanceof Error ? error.message : t('Failed to load historical version details') });
    }
  }

  function copyPath() {
    if (!visibleDetail) return;
    void navigator.clipboard?.writeText(visibleDetail.path);
    setNotice({ tone: 'ok', text: t('Template path copied.') });
  }

  function showCurrentDetail() {
    setSelectedHistory(null);
    setDetailMode('current');
  }

  function mobileBar() {
    if (!selectedCurrent) return null;
    if (detailMode === 'history') {
      return <MobileDrilldownBar label={t('Historical versions')} title={templateDisplayTitle(selectedCurrent, t)} meta={t('{{count}} versions', { count: historyVersions.length })} backLabel={t('Back to current version')} onBack={showCurrentDetail} />;
    }
    if (detailMode === 'history-detail' && selectedHistory) {
      return <MobileDrilldownBar label={t('Historical versions')} title={templateDisplayTitle(selectedHistory, t)} meta={`v${selectedHistory.version}`} backLabel={t('Back to historical versions')} onBack={() => setDetailMode('history')} />;
    }
    return <MobileDrilldownBar label={t('Template details')} title={templateDisplayTitle(selectedCurrent, t)} meta={`v${selectedCurrent.version} · ${t('Current version')}`} backLabel={t('Back to template list')} onBack={() => setMobileDetailOpen(false)} />;
  }

  return <section className="workflow-page">
    {notice && <div className={`nx-alert is-${notice.tone === 'danger' ? 'error' : notice.tone === 'ok' ? 'success' : 'warning'}`}>{notice.text}</div>}

    <section className={`workflow-layout mobile-drilldown ${mobileDetailOpen ? 'is-detail-open' : 'is-list-open'}`}>
      <aside className="workflow-list-panel mobile-drilldown-list">
        <div className="workflow-toolbar">
          <label className="workflow-search"><Search size={15} /><input aria-label={t('Search workflow templates')} value={query} onChange={(event) => { setQuery(event.target.value); setMobileDetailOpen(false); }} placeholder={t('Search title or keywords')} /></label>
        </div>
        <div className="workflow-list-summary"><strong>{filtered.length}</strong><span>{t('workflow templates')}</span></div>
        <div className="workflow-list">
          {loading ? <p className="empty-mini">{t('Loading workflow templates…')}</p> : filtered.length === 0 ? <p className="empty-mini">{t('No matching templates.')}</p> : filtered.map((item) => <button type="button" key={item.id} className={selectedCurrent?.id === item.id ? 'is-active' : ''} aria-pressed={selectedCurrent?.id === item.id} onClick={() => void openCurrentTemplate(item, true)}>
            <span className="workflow-file-icon"><FileJson size={16} /></span>
            <span><strong>{templateDisplayTitle(item, t)}</strong><small>{templateListMeta(item, t)}</small></span>
            {item.has_conflict && <StatusPill tone="danger">{t('Current ×{{count}}', { count: item.active_count })}</StatusPill>}
          </button>)}
        </div>
      </aside>

      <section className="workflow-runtime-viewer mobile-drilldown-detail">
        {mobileBar()}
        {!selectedCurrent ? <div className="empty-state"><span><FileJson size={24} /></span><h3>{t('Select template')}</h3><p>{t('Select a template from the left to view execution steps.')}</p></div>
          : detailMode === 'history' ? <WorkflowHistoryViewer selected={selectedCurrent} versions={historyVersions} loading={historyLoading} onBack={showCurrentDetail} onOpenVersion={(version) => void openHistoryVersion(version)} />
            : visibleDetail && <>
              {detailMode === 'history-detail' && <div className="workflow-detail-context"><button type="button" className="nx-button is-secondary is-small" onClick={() => setDetailMode('history')}><ArrowLeft size={14} />{t('Back to historical versions')}</button><span>{t('Viewing v{{version}}', { version: visibleDetail.version })}</span></div>}
              <RuntimeTemplateViewer selected={visibleDetail} parsed={parsed} onCopy={copyPath} onOpenHistory={detailMode === 'current' ? () => void openHistory() : undefined} historyLoading={historyLoading} />
            </>}
      </section>
    </section>
  </section>;
}

function WorkflowHistoryViewer({ selected, versions, loading, onBack, onOpenVersion }: { selected: WorkflowTemplateDetail; versions: WorkflowTemplateSummary[]; loading: boolean; onBack: () => void; onOpenVersion: (template: WorkflowTemplateSummary) => void }) {
  const { t } = useTranslation();
  return <article className="workflow-history-card">
    <header className="workflow-history-head">
      <div><button type="button" className="workflow-history-back" onClick={onBack}><ArrowLeft size={14} />{t('Back to current version')}</button><span className="nexus-eyebrow">{selected.id}</span><h3>{t('Historical versions')}</h3><p>{templateDisplayTitle(selected, t)}</p></div>
      <span className="workflow-history-count">{t('{{count}} versions', { count: versions.length })}</span>
    </header>
    <div className="workflow-history-list">
      {loading ? <EmptyMini>{t('Loading historical versions…')}</EmptyMini> : versions.length <= 1 ? <div className="workflow-history-empty"><History size={22} /><strong>{t('No historical versions yet')}</strong><span>{t('Older versions will appear here automatically when a new version is published.')}</span></div> : versions.map((version) => <button type="button" key={version.path} onClick={() => onOpenVersion(version)}>
        <span className="workflow-history-version"><strong>v{version.version}</strong><small>{t('Updated at {{time}}', { time: formatTime(version.updated_at) })}</small></span>
        <StatusPill tone={statusTone(version)}>{statusLabel(version.status, t)}</StatusPill>
        <ChevronRight size={15} />
      </button>)}
    </div>
  </article>;
}

function RuntimeTemplateViewer({ selected, parsed, onCopy, onOpenHistory, historyLoading = false }: { selected: WorkflowTemplateDetail; parsed: ReturnType<typeof parseTemplate>; onCopy: () => void; onOpenHistory?: () => void; historyLoading?: boolean }) {
  const { t } = useTranslation();
  const match = record(parsed.body.match);
  const steps = stepViews(parsed.body.steps);
  const conditions = stringValues(parsed.body.completion_conditions);
  const keywords = [...stringValues(match.keywords), ...(selected.keywords || [])].filter((value, index, list) => value && list.indexOf(value) === index);
  const matchRows = matchViews(match, t);
  const stepGroups = groupSteps(steps, t('Unassigned phase'));
  const phases = stepGroups.flatMap((group) => group.phase ? [group.phase] : []);
  const raw = selected.json || parsed.body;

  return <article className="workflow-runtime-card">
    <header className="workflow-runtime-head">
      <div><span className="nexus-eyebrow">{selected.id}</span><h3>{parsed.title || selected.title || selected.file_name}</h3><p>{parsed.description || selected.description || t('No template description.')}</p></div>
      <div className="workflow-runtime-actions"><StatusPill tone={selected.has_conflict ? 'danger' : statusTone(selected)}>{selected.has_conflict ? t('Current ×{{count}}', { count: selected.active_count }) : statusLabel(selected.status, t)}</StatusPill><span className="workflow-step-count">{t('{{count}} steps', { count: steps.length || selected.step_count || 0 })}</span></div>
    </header>

    <div className="workflow-runtime-meta"><span>{t('Version {{version}}', { version: selected.version })}</span><span>{t('{{count}} phases', { count: phases.length || 1 })}</span><span>{t('Updated at {{time}}', { time: formatTime(selected.updated_at) })}</span>{onOpenHistory && (selected.retired_count ?? 0) > 0 && <button type="button" className="workflow-history-link" onClick={onOpenHistory} disabled={historyLoading}><History size={13} />{historyLoading ? t('Loading…') : t('Historical versions {{count}}', { count: selected.retired_count })}<ChevronRight size={13} /></button>}</div>

    <section className="workflow-runtime-section">
      <SectionTitle title={t('Execution steps')} subtitle={t('View the main task flow by phase.')} />
      {steps.length === 0 ? <EmptyMini>{t('No steps.')}</EmptyMini> : <div className="workflow-phase-list">{stepGroups.map((group) => <div className="workflow-phase-block" key={group.phase}><header><span>{group.phase}</span><strong>{t('{{count}} steps', { count: group.steps.length })}</strong></header><div className="workflow-step-list">{group.steps.map((step, index) => <StepCard key={`${group.phase}:${step.id}:${index}`} step={step} index={steps.indexOf(step) + 1} />)}</div></div>)}</div>}
    </section>

    <section className="workflow-runtime-section">
      <SectionTitle title={t('Completion conditions')} subtitle={t('Results that must be met before completing the task.')} />
      {conditions.length === 0 ? <EmptyMini>{t('No completion conditions.')}</EmptyMini> : <div className="workflow-condition-list">{conditions.map((condition, index) => <div key={`${condition}:${index}`}><span>{index + 1}</span><p>{condition}</p></div>)}</div>}
    </section>

    <details className="workflow-secondary-details">
      <summary>{t('Matching and technical information')}</summary>
      <div className="workflow-secondary-body">
        <section>
          <SectionTitle title={t('Matching rules')} subtitle={t('Signals used by models to decide whether to use this template.')} />
          {keywords.length > 0 && <ChipRow values={keywords} />}
          {matchRows.length === 0 ? <EmptyMini>{t('No matching rules.')}</EmptyMini> : <div className="workflow-match-grid">{matchRows.map((row) => <div key={row.label}><span>{row.label}</span><p>{row.values.join(' · ')}</p></div>)}</div>}
        </section>
        <section className="workflow-runtime-grid is-compact">
          <InfoTile label={t('Template ID')} value={parsed.id || selected.id} />
          <InfoTile label={t('File name')} value={selected.file_name} />
          <InfoTile label={t('Version count')} value={String(selected.version_count ?? 1)} />
          <InfoTile label={t('Historical versions')} value={String(selected.retired_count ?? 0)} />
          <InfoTile label="JSON" value={parsed.error || t('Parsable')} />
        </section>
        <div className="workflow-technical-actions"><button type="button" className="nx-button is-secondary" onClick={onCopy}><Copy size={15} />{t('Copy template path')}</button></div>
        <details className="workflow-runtime-json"><summary><Check size={13} />{t('View raw runtime JSON')}</summary><pre>{JSON.stringify(raw, null, 2)}</pre></details>
      </div>
    </details>
  </article>;
}

function StepCard({ step, index }: { step: StepView; index: number }) {
  const { t } = useTranslation();
  return <div className="workflow-step-card"><div><span>{index}</span><strong>{step.title || step.id || t('Step {{step}}', { step: index })}</strong></div><footer><em>{step.phase || t('Unassigned phase')}</em>{step.required && <em>{t('Required')}</em>}{step.depends.length > 0 && <em>{t('Depends on {{depends}}', { depends: step.depends.join(', ') })}</em>}{step.substitution && <em>{step.substitution}</em>}</footer></div>;
}

function StatusPill({ tone, children }: { tone: Tone; children: ReactNode }) {
  return <span className={`status-badge tone-${tone}`}><span />{children}</span>;
}

function InfoTile({ label, value }: { label: string; value: string }) {
  const { t } = useTranslation();
  return <div className="workflow-info-tile"><span>{label}</span><strong>{value || t('None')}</strong></div>;
}

function SectionTitle({ title, subtitle }: { title: string; subtitle: string }) {
  return <header className="workflow-section-title"><div><h4>{title}</h4><p>{subtitle}</p></div></header>;
}

function ChipRow({ values }: { values: string[] }) {
  return <div className="workflow-chip-row">{values.slice(0, 18).map((value) => <span key={value}>{value}</span>)}</div>;
}

function EmptyMini({ children }: { children: ReactNode }) {
  return <p className="empty-mini">{children}</p>;
}

function record(value: unknown): Record<string, unknown> {
  return value && typeof value === 'object' && !Array.isArray(value) ? value as Record<string, unknown> : {};
}

function array(value: unknown): unknown[] {
  return Array.isArray(value) ? value : [];
}

function text(value: unknown): string {
  if (typeof value === 'string') return value;
  if (typeof value === 'number' || typeof value === 'boolean') return String(value);
  return '';
}

function stringValues(value: unknown): string[] {
  if (Array.isArray(value)) return value.flatMap((item) => { const current = text(item); return current ? [current] : []; });
  const single = text(value);
  return single ? [single] : [];
}

function groupSteps(steps: StepView[], fallbackPhase = 'Unassigned phase'): StepGroup[] {
  const groups = new Map<string, StepView[]>();
  for (const step of steps) {
    const phase = step.phase || fallbackPhase;
    groups.set(phase, [...(groups.get(phase) || []), step]);
  }
  return Array.from(groups.entries()).map(([phase, groupedSteps]) => ({ phase, steps: groupedSteps }));
}

function stepViews(value: unknown): StepView[] {
  return array(value).map((item) => {
    const body = record(item);
    return {
      id: text(body.id),
      title: text(body.title) || text(body.name),
      phase: text(body.phase),
      required: body.required === true,
      depends: stringValues(body.depends_on || body.depends),
      substitution: text(body.substitution),
    };
  });
}

function matchViews(match: Record<string, unknown>, t: TFunction): MatchView[] {
  const labels: Record<string, string> = {
    keywords: t('Keywords'),
    devices: t('Devices'),
    task_types: t('Task types'),
    projects: t('Projects'),
    tools: t('Tools'),
    skills: t('Skill'),
    priority: t('Priority'),
  };
  return Object.entries(match).reduce<MatchView[]>((rows, [key, value]) => {
    const values = stringValues(value);
    if (values.length > 0) rows.push({ label: labels[key] || key, values });
    return rows;
  }, []);
}
