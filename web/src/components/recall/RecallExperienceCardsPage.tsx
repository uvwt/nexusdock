import { useEffect, useMemo, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import type { TFunction } from 'i18next';
import { ArrowLeft, ChevronRight, FileText, Search, Tags } from 'lucide-react';
import { api } from '../../api/client';
import { formatTime } from '../../lib/time';
import type { Recall, RecallCardSummary } from './types';
import { formatBytes, nameOf, normalizePath } from './utils';

type StatusFilter = 'all' | 'active' | 'inbox' | 'history';
type CardPathMeta = { project: string; status: string; cardType: string };
type Props = { entries: RecallCardSummary[]; loading: boolean };

export default function RecallExperienceCardsPage({ entries, loading }: Props) {
  const { t } = useTranslation();
  const [query, setQuery] = useState('');
  const [statusFilter, setStatusFilter] = useState<StatusFilter>('all');
  const [selectedPath, setSelectedPath] = useState('');
  const [selected, setSelected] = useState<Recall | null>(null);
  const [detailLoading, setDetailLoading] = useState(false);
  const [error, setError] = useState('');
  const [mobileDetailOpen, setMobileDetailOpen] = useState(false);
  const detailRequestRef = useRef(0);

  const statusFilters = useMemo<Array<{ value: StatusFilter; label: string }>>(() => [
    { value: 'all', label: t('All') },
    { value: 'active', label: t('Active') },
    { value: 'inbox', label: t('Inbox') },
    { value: 'history', label: t('History') },
  ], [t]);

  const cards = useMemo(
    () => [...entries].sort((left, right) => left.path.localeCompare(right.path)),
    [entries],
  );
  const filtered = useMemo(() => {
    const needle = query.trim().toLowerCase();
    return cards.filter((entry) => {
      if (statusFilter !== 'all' && statusGroup(entry.status) !== statusFilter) return false;
      if (!needle) return true;
      return [entry.title, entry.project, entry.status, entry.card_type, cardTypeLabel(entry.card_type, t), ...(entry.tags || []), entry.path]
        .filter(Boolean)
        .join(' ')
        .toLowerCase()
        .includes(needle);
    });
  }, [cards, query, statusFilter, t]);

  useEffect(() => {
    if (filtered.length === 0) {
      detailRequestRef.current += 1;
      setSelectedPath('');
      setSelected(null);
      setDetailLoading(false);
      return;
    }
    if (!filtered.some((entry) => entry.path === selectedPath)) void openCard(filtered[0].path, false);
  }, [filtered, selectedPath]);

  async function openCard(path: string, revealOnMobile: boolean) {
    const requestID = ++detailRequestRef.current;
    setSelectedPath(path);
    setDetailLoading(true);
    setError('');
    try {
      const response = await api<{ recall: Recall }>(`/v1/recall/${encodeURIComponent(path)}`);
      if (requestID !== detailRequestRef.current) return;
      setSelected(response.recall);
      if (revealOnMobile) setMobileDetailOpen(true);
    } catch (reason) {
      if (requestID !== detailRequestRef.current) return;
      setSelected(null);
      setError(reason instanceof Error ? reason.message : t('Failed to load experience cards'));
    } finally {
      if (requestID === detailRequestRef.current) setDetailLoading(false);
    }
  }

  return <section className={`recall-card-browser ${mobileDetailOpen ? 'is-detail-open' : 'is-list-open'}`}>
    <aside className="recall-card-list-panel">
      <div className="recall-panel-head recall-card-list-head">
        <div><h2>{t('Experience Cards')}</h2><p>{t('Browse accumulated reusable experiences')}</p></div>
        <span className="recall-card-total">{cards.length}</span>
      </div>
      <div className="recall-card-toolbar">
        <label className="recall-card-search"><Search size={15} /><input aria-label={t('Search experience cards')} value={query} onChange={(event) => { setQuery(event.target.value); setMobileDetailOpen(false); }} placeholder={t('Search cards')} /></label>
        <select aria-label={t('Filter experience card status')} value={statusFilter} onChange={(event) => { setStatusFilter(event.target.value as StatusFilter); setMobileDetailOpen(false); }}>{statusFilters.map((item) => <option key={item.value} value={item.value}>{item.label}</option>)}</select>
      </div>
      <div className="recall-card-list-summary"><strong>{filtered.length}</strong><span>{t('cards')}</span>{query || statusFilter !== 'all' ? <em>{t('Filtered results')}</em> : <em>{t('All')}</em>}</div>
      <div className="recall-card-list">
        {loading && cards.length === 0 ? <p className="recall-empty">{t('Loading experience cards…')}</p>
          : filtered.length === 0 ? <div className="recall-card-empty"><FileText size={22} /><strong>{t('No matching experience cards')}</strong><span>{t('Adjust search query or status filter.')}</span></div>
            : filtered.map((entry) => {
              return <button type="button" key={entry.path} className={selectedPath === entry.path ? 'is-active' : ''} aria-pressed={selectedPath === entry.path} onClick={() => void openCard(entry.path, true)}>
                <span className="recall-card-file-icon"><FileText size={15} /></span>
                <span className="recall-card-list-copy"><strong>{entry.title}</strong><small>{entry.project} · {cardTypeLabel(entry.card_type, t)}</small></span>
                <CardStatus status={entry.status} t={t} />
                <ChevronRight size={14} />
              </button>;
            })}
      </div>
    </aside>

    <article className="recall-card-detail-panel">
      <button type="button" className="recall-card-mobile-back" onClick={() => setMobileDetailOpen(false)}><ArrowLeft size={15} />{t('Back to experience cards')}</button>
      {error ? <div className="recall-card-detail-empty"><strong>{t('Failed to load card')}</strong><span>{error}</span></div>
        : detailLoading && !selected ? <p className="recall-empty">{t('Loading card content…')}</p>
          : !selected ? <div className="recall-card-detail-empty"><FileText size={24} /><strong>{t('Select an experience card')}</strong><span>{t('View content and source from the left list.')}</span></div>
            : <CardDetail recall={selected} t={t} />}
    </article>
  </section>;
}

function CardDetail({ recall, t }: { recall: Recall; t: TFunction }) {
  const frontmatter = recall.frontmatter || {};
  const pathMeta = cardPathMeta(recall.path);
  const status = frontmatter.status || pathMeta.status;
  const cardType = frontmatter.card_type || pathMeta.cardType;
  const title = cardTitle(recall);
  const body = cardBody(recall.body || recall.content, title);
  const tags = splitTags(frontmatter.tags);

  return <>
    <header className="recall-card-detail-head">
      <div><span className="nexus-eyebrow">{frontmatter.project || pathMeta.project} / {cardTypeLabel(cardType, t)}</span><h2>{title}</h2><p>{frontmatter.summary || t('Structured experience card')}</p></div>
      <CardStatus status={status} t={t} />
    </header>
    <div className="recall-card-meta-grid">
      <Info label={t('Project')} value={frontmatter.project || pathMeta.project} fallback={t('Not recorded')} />
      <Info label={t('Type')} value={cardTypeLabel(cardType, t)} fallback={t('Not recorded')} />
      <Info label={t('Scope')} value={scopeLabel(frontmatter.scope, t)} fallback={t('Not recorded')} />
      <Info label={t('Confidence')} value={confidenceLabel(frontmatter.confidence, t)} fallback={t('Not recorded')} />
    </div>
    {tags.length > 0 && <div className="recall-card-tags"><Tags size={14} />{tags.map((tag) => <span key={tag}>{tag}</span>)}</div>}
    <section className="recall-card-body"><p>{body || t('This card has no content.')}</p></section>
    <details className="recall-card-technical">
      <summary>{t('Source and technical information')}</summary>
      <div>
        <Info label={t('Source')} value={frontmatter.source} fallback={t('Not recorded')} />
        {frontmatter.evidence && <Info label={t('Evidence')} value={frontmatter.evidence} />}
        <Info label={t('Updated at')} value={formatTime(frontmatter.updated_at || frontmatter.created_at || '', { fallback: t('Not recorded') })} />
        <Info label={t('Size')} value={formatBytes(recall.size_bytes)} />
        <Info label={t('Path')} value={recall.path} />
      </div>
    </details>
  </>;
}

function CardStatus({ status, t }: { status: string; t: TFunction }) {
  const group = statusGroup(status);
  return <span className={`recall-card-status is-${group}`}><i />{statusLabel(status, t)}</span>;
}

function Info({ label, value, fallback }: { label: string; value?: string; fallback?: string }) {
  return <div className="recall-card-info"><span>{label}</span><strong>{value || fallback || '—'}</strong></div>;
}

function cardPathMeta(path: string): CardPathMeta {
  const parts = normalizePath(path).split('/');
  const cardsIndex = parts.indexOf('cards');
  return {
    project: parts[cardsIndex + 1] || 'global',
    status: parts[cardsIndex + 2] || 'unknown',
    cardType: parts[cardsIndex + 3] || 'unknown',
  };
}

function cardListTitle(entry: Pick<RecallCardSummary, 'path' | 'title'>): string {
  if (entry.title.trim()) return entry.title.trim();
  return nameOf(entry.path)
    .replace(/\.(md|txt)$/i, '')
    .replace(/--+/g, ' / ')
    .replace(/-/g, ' ');
}

function cardTitle(recall: Recall): string {
  const heading = (recall.body || recall.content).match(/^#\s+(.+)$/m)?.[1]?.trim();
  return heading || cardListTitle({ path: recall.path, title: '' });
}

function cardBody(body: string, title: string): string {
  const trimmed = body.trim();
  const heading = `# ${title}`;
  return trimmed.startsWith(heading) ? trimmed.slice(heading.length).trim() : trimmed;
}

function splitTags(value?: string): string[] {
  if (!value) return [];
  return value.split(',').map((tag) => tag.trim()).filter(Boolean);
}

function statusGroup(status: string): Exclude<StatusFilter, 'all'> {
  const normalized = status.toLowerCase();
  if (normalized === 'active' || normalized === 'verified') return 'active';
  if (normalized === 'inbox' || normalized === 'unverified' || normalized === 'conflicted') return 'inbox';
  return 'history';
}

function statusLabel(status: string, t: TFunction): string {
  switch (status.toLowerCase()) {
    case 'active': return t('Active');
    case 'verified': return t('Verified');
    case 'inbox': return t('Inbox');
    case 'unverified': return t('Unverified');
    case 'conflicted': return t('Conflicted');
    case 'historical': return t('Historical');
    case 'archived': return t('Archived');
    case 'deprecated': return t('Deprecated');
    case 'stale': return t('Stale');
    case 'rejected': return t('Rejected');
    default: return status || t('Unknown');
  }
}

function cardTypeLabel(cardType: string, t: TFunction): string {
  const labels: Record<string, string> = {
    architecture: t('Architecture'),
    anti_pattern: t('Anti-pattern'),
    bug_pattern: t('Bug pattern'),
    decision: t('Decision'),
    deploy_note: t('Deploy note'),
    preference: t('Preference'),
    project_trap: t('Project trap'),
    runbook: t('Runbook'),
  };
  return labels[cardType] || cardType || t('Uncategorized');
}

function scopeLabel(scope: string | undefined, t: TFunction): string {
  if (!scope) return t('Not recorded');
  if (scope === 'project') return t('Project');
  if (scope === 'global' || scope === 'shared') return t('Global');
  if (scope === 'device') return t('Device');
  if (scope === 'user') return t('User');
  return scope;
}

function confidenceLabel(confidence: string | undefined, t: TFunction): string {
  if (!confidence) return t('Not recorded');
  if (confidence === 'high') return t('High');
  if (confidence === 'medium') return t('Medium');
  if (confidence === 'low') return t('Low');
  return confidence;
}
