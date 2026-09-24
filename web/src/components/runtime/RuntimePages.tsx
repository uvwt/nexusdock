import { useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from 'react';
import { useTranslation } from 'react-i18next';
import type { TFunction } from 'i18next';
import { Check, Clock3, FileText, Layers, Search, ShieldAlert, Trash2 } from 'lucide-react';
import { ApiError, api } from '../../api/client';
import { formatTime } from '../../lib/time';
import Dialog from '../Dialog';
import MobileDrilldownBar from '../MobileDrilldownBar';

type Tone = 'ok' | 'warn' | 'danger' | 'muted';
type TaskStatus = 'all' | 'active' | 'completed' | 'blocked';

const runtimeTaskListLimit = 200;
const taskPollIntervalMs = 2000;
const recentTaskWindowMs = 24 * 60 * 60 * 1000;

function taskStatusLabel(status: string | undefined, t: TFunction): string {
  switch (status) {
    case 'all': return t('All');
    case 'active': return t('In progress');
    case 'completed': return t('Completed');
    case 'blocked': return t('Blocked');
    default: return status || t('Unknown');
  }
}

type TaskStep = { id: string; title: string; status: string };
type OpsTask = { id: string; title: string; goal: string; status: string; summary?: string; blocker?: string; current_step?: TaskStep; completed_step_count: number; step_count: number; updated_at: string; file_name: string };
type OpsTaskDetail = OpsTask & { steps?: unknown[] };
type OpsSkill = { id: string; title: string; source: string; path: string; description: string; file_count: number; status: string; skill_ref: string; source_type: string; source_id: string; plugin_name?: string; content_digest: string };
type OpsSkillFile = { path: string; kind: string; size_bytes: number; updated_at: string };
type OpsSkillDetail = OpsSkill & { files?: OpsSkillFile[]; runtime_state?: Record<string, unknown> };
type TaskCounts = { active: number; blocked: number; completed: number };
type TaskListResponse = { ok: boolean; items: OpsTask[]; count: number; total: number; root?: string; source?: string };
type TaskDetailResponse = { ok: boolean; task: OpsTaskDetail; source?: string };
type DeleteTaskResponse = { ok: boolean; task_id: string; deleted_task?: OpsTask; source?: string };
type SkillsResponse = { ok: boolean; items: OpsSkill[]; count: number; root?: string; source?: string };
type SkillDetailResponse = { ok: boolean; skill: OpsSkillDetail; source?: string };
type SkillFileContent = OpsSkillFile & { content: string; truncated: boolean };
type SkillFileResponse = { ok: boolean; file?: SkillFileContent };

const emptyTasks: TaskListResponse = { ok: false, items: [], count: 0, total: 0, root: '' };

function formatBytes(value?: number): string {
  if (value === undefined) return '—';
  const units = ['B', 'KiB', 'MiB', 'GiB'];
  let size = value;
  let unit = 0;
  while (size >= 1024 && unit < units.length - 1) {
    size /= 1024;
    unit += 1;
  }
  return `${size.toFixed(unit === 0 ? 0 : 1)} ${units[unit]}`;
}

function apiMessage(error: unknown, fallback: string): string {
  if (error instanceof ApiError) return `${error.code || error.status}: ${error.message}`;
  return error instanceof Error ? error.message : fallback;
}

function toneForTask(task: Pick<OpsTask, 'status'>): Tone {
  if (task.status === 'completed') return 'ok';
  if (task.status === 'blocked') return 'danger';
  if (task.status === 'active') return 'warn';
  return 'muted';
}

function countTasks(tasks: OpsTask[]): TaskCounts {
  return {
    active: tasks.filter((item) => item.status === 'active').length,
    blocked: tasks.filter((item) => item.status === 'blocked').length,
    completed: tasks.filter((item) => item.status === 'completed').length,
  };
}

function taskUpdatedRecently(task: Pick<OpsTask, 'updated_at'>, now = Date.now()): boolean {
  const updatedAt = Date.parse(task.updated_at);
  return Number.isFinite(updatedAt) && updatedAt >= now - recentTaskWindowMs;
}

function taskDisplayTitle(task: Pick<OpsTask, 'title' | 'goal' | 'id'> | undefined, fallback: string): string {
  const title = task?.title?.trim();
  if (title) return title;
  const goal = task?.goal?.trim();
  if (goal) return goal.split(/[。.!?！？\n]/)[0] || goal;
  return task?.id || fallback;
}

type TaskProgressState = { completed: number; total: number; percent: number; determinate: boolean; label: string };

function taskProgress(task: Pick<OpsTask, 'status' | 'completed_step_count' | 'step_count'>, t: TFunction): TaskProgressState {
  const total = Math.max(0, Number(task.step_count) || 0);
  if (total === 0) {
    return { completed: 0, total: 0, percent: 0, determinate: false, label: task.status === 'completed' ? t('Completed') : t('Unsegmented steps') };
  }
  const reported = Math.max(0, Number(task.completed_step_count) || 0);
  const completed = task.status === 'completed' ? total : Math.min(reported, total);
  return { completed, total, percent: Math.round((completed / total) * 100), determinate: true, label: `${completed} / ${total}` };
}

function taskCurrentText(task: OpsTask, t: TFunction): string {
  if (task.status === 'blocked' && task.blocker) return t('Blocked: {{blocker}}', { blocker: task.blocker });
  if (task.current_step?.title) return t('Current: {{step}}', { step: task.current_step.title });
  if (task.summary) return task.summary;
  if (task.status === 'completed') return t('Task completed');
  return task.step_count > 0 ? t('Waiting for next step') : t('No execution steps split');
}

function toneForStatus(status?: string): Tone {
  if (!status) return 'muted';
  if (['ok', 'healthy', 'available', 'installed', 'active', 'success', 'completed'].includes(status)) return 'ok';
  if (['failed', 'blocked', 'offline', 'unknown'].includes(status)) return 'danger';
  if (['pending', 'draft', 'running', 'degraded'].includes(status)) return 'warn';
  return 'muted';
}

type ReloadOptions = { silent?: boolean };

function useOpsResource<T>(path: string, fallback: T, refreshToken: number, fallbackError: string) {
  const fallbackRef = useRef(fallback);
  const silentReloadRef = useRef(false);
  fallbackRef.current = fallback;
  const [localToken, setLocalToken] = useState(0);
  const [state, setState] = useState<{ data: T; loading: boolean; error?: string }>({ data: fallback, loading: true });
  useEffect(() => {
    const silent = silentReloadRef.current;
    silentReloadRef.current = false;
    let cancelled = false;
    if (!silent) setState((current) => ({ ...current, loading: true, error: undefined }));
    api<T>(path).then((data) => { if (!cancelled) setState({ data, loading: false }); }).catch((error) => { if (!cancelled) setState((current) => ({ data: current.data, loading: false, error: apiMessage(error, fallbackError) })); });
    return () => { cancelled = true; };
  }, [path, refreshToken, localToken, fallbackError]);
  const reload = useCallback((options: ReloadOptions = {}) => {
    silentReloadRef.current = Boolean(options.silent);
    setLocalToken((value) => value + 1);
  }, []);
  return { ...state, reload };
}

function useOptionalOpsResource<T>(path: string, fallback: T, refreshToken: number, fallbackError: string) {
  const fallbackRef = useRef(fallback);
  const silentReloadRef = useRef(false);
  fallbackRef.current = fallback;
  const [localToken, setLocalToken] = useState(0);
  const [state, setState] = useState<{ data: T; loading: boolean; error?: string }>({ data: fallback, loading: false });
  useEffect(() => {
    if (!path) {
      setState({ data: fallbackRef.current, loading: false });
      return undefined;
    }
    const silent = silentReloadRef.current;
    silentReloadRef.current = false;
    let cancelled = false;
    if (!silent) setState((current) => ({ ...current, loading: true, error: undefined }));
    api<T>(path).then((data) => { if (!cancelled) setState({ data, loading: false }); }).catch((error) => { if (!cancelled) setState((current) => ({ data: current.data, loading: false, error: apiMessage(error, fallbackError) })); });
    return () => { cancelled = true; };
  }, [path, refreshToken, localToken, fallbackError]);
  const reload = useCallback((options: ReloadOptions = {}) => {
    silentReloadRef.current = Boolean(options.silent);
    setLocalToken((value) => value + 1);
  }, []);
  return { ...state, reload };
}

export function TaskCenterPage({ nodeID, refreshToken }: { nodeID: string; refreshToken: number }) {
  const { t } = useTranslation();
  const [status, setStatus] = useState<TaskStatus>('active');
  const [query, setQuery] = useState('');
  const [recentOnly, setRecentOnly] = useState(true);
  const [selectedId, setSelectedId] = useState('');
  const [mobileDetailOpen, setMobileDetailOpen] = useState(false);
  const [pendingDelete, setPendingDelete] = useState<OpsTask | null>(null);
  const [deletingId, setDeletingId] = useState('');
  const [deleteError, setDeleteError] = useState('');
  const [notice, setNotice] = useState('');

  const runtimeBase = `/v1/runtime/nodes/${encodeURIComponent(nodeID)}`;
  const path = `${runtimeBase}/tasks?status=${status}&limit=${runtimeTaskListLimit}${query.trim() ? `&q=${encodeURIComponent(query.trim())}` : ''}`;
  const resource = useOpsResource<TaskListResponse>(path, emptyTasks, refreshToken, t('Request failed'));
  const allResource = useOpsResource<TaskListResponse>(`${runtimeBase}/tasks?status=all&limit=${runtimeTaskListLimit}`, emptyTasks, refreshToken, t('Request failed'));
  const tasks = recentOnly ? resource.data.items.filter((task) => taskUpdatedRecently(task)) : resource.data.items;
  const recentTasks = useMemo(() => allResource.data.items.filter((task) => taskUpdatedRecently(task)), [allResource.data.items]);
  const selected = tasks.find((item) => item.id === selectedId) || tasks[0];
  const detail = useOptionalOpsResource<TaskDetailResponse>(selected?.file_name ? `${runtimeBase}/tasks/${encodeURIComponent(selected.file_name)}` : '', { ok: false, task: selected as OpsTaskDetail }, refreshToken, t('Request failed'));
  const totalStats = useMemo(() => countTasks(allResource.data.items), [allResource.data.items]);
  const recentStats = useMemo(() => countTasks(recentTasks), [recentTasks]);
  const totalCount = allResource.data.total || allResource.data.count || resource.data.total || tasks.length;
  const statusCounts: Record<TaskStatus, number> = recentOnly
    ? { ...recentStats, all: recentTasks.length }
    : { ...totalStats, all: totalCount };

  const reloadTasks = useCallback((options: ReloadOptions = {}) => {
    resource.reload(options);
    allResource.reload(options);
    detail.reload(options);
  }, [allResource.reload, detail.reload, resource.reload]);

  useEffect(() => {
    const refreshVisibleTasks = () => {
      if (document.visibilityState === 'visible') reloadTasks({ silent: true });
    };
    const timer = window.setInterval(refreshVisibleTasks, taskPollIntervalMs);
    document.addEventListener('visibilitychange', refreshVisibleTasks);
    return () => {
      window.clearInterval(timer);
      document.removeEventListener('visibilitychange', refreshVisibleTasks);
    };
  }, [reloadTasks]);

  async function confirmDelete() {
    if (!pendingDelete || deletingId) return;
    const task = pendingDelete;
    setDeletingId(task.id);
    setDeleteError('');
    try {
      await api<DeleteTaskResponse>(`${runtimeBase}/tasks/${encodeURIComponent(task.id)}`, { method: 'DELETE' });
      setPendingDelete(null);
      setSelectedId('');
      setMobileDetailOpen(false);
      setNotice(t('Task “{{title}}” has been deleted.', { title: taskDisplayTitle(task, t('Untitled task')) }));
      reloadTasks();
    } catch (error) {
      setDeleteError(apiMessage(error, t('Request failed')));
    } finally {
      setDeletingId('');
    }
  }

  return <>
    <OpsShell error={resource.error || allResource.error}>
      {notice && <div className="nx-alert is-success" role="status">{notice}<button type="button" onClick={() => setNotice('')}>{t('Close')}</button></div>}
      <div className="ops-toolbar is-console"><div className="ops-segmented">{(['all', 'active', 'blocked', 'completed'] as TaskStatus[]).map((item) => <button type="button" key={item} className={status === item ? 'is-active' : ''} aria-pressed={status === item} onClick={() => { setStatus(item); setMobileDetailOpen(false); }}><span>{taskStatusLabel(item, t)}</span><em>{statusCounts[item]}</em></button>)}</div><label className="ops-search"><Search size={15} /><input aria-label={t('Search tasks')} value={query} onChange={(event) => setQuery(event.target.value)} placeholder={t('Search tasks or current step')} /></label><button type="button" className={`nx-button is-secondary is-small ${recentOnly ? 'is-active' : ''}`} aria-pressed={recentOnly} onClick={() => { setRecentOnly((current) => !current); setMobileDetailOpen(false); }}><Clock3 size={15} />{t('Recent 24 hours')}</button><span className="ops-task-toolbar-meta"><span className="ops-auto-refresh"><i aria-hidden="true" />{t('Auto refresh')}</span><span className="ops-count">{t('Showing {{count}} items', { count: tasks.length })}</span></span></div>
      <section className={`ops-master-detail mobile-drilldown ${mobileDetailOpen ? 'is-detail-open' : 'is-list-open'}`}>
        <div className="ops-task-rail mobile-drilldown-list">
          {tasks.length === 0 ? <EmptyOps text={recentOnly ? t('No matching tasks in the last 24 hours.') : t('No matching tasks.')} /> : tasks.map((task) => <button type="button" key={task.id} className={`ops-task-line ${selected?.id === task.id ? 'is-selected' : ''}`} aria-pressed={selected?.id === task.id} onClick={() => { setSelectedId(task.id); setMobileDetailOpen(true); }}><span className="ops-task-line-title"><strong>{taskDisplayTitle(task, t('Untitled task'))}</strong><span className={`ops-task-state tone-${toneForTask(task)}`}>{taskStatusLabel(task.status, t)}</span></span><TaskProgress task={task} compact /><small>{taskCurrentText(task, t)}</small></button>)}
        </div>
        <div className="mobile-drilldown-detail">
          {selected && <MobileDrilldownBar label={t('Task details')} title={taskDisplayTitle(selected, t('Untitled task'))} meta={taskStatusLabel(selected.status, t)} backLabel={t('Back to task list')} onBack={() => setMobileDetailOpen(false)} />}
          <TaskDetail task={selected} detail={detail.data.task} loading={detail.loading} error={detail.error} deleting={deletingId === selected?.id} onDelete={(task) => { setDeleteError(''); setPendingDelete(task); }} />
        </div>
      </section>
    </OpsShell>
    {pendingDelete && <Dialog title={t('Delete task')} description={t('Task records and steps will be permanently deleted. This action cannot be undone.')} onClose={() => { if (!deletingId) setPendingDelete(null); }}>
      <div className="ops-delete-dialog">
        <p>{t('Are you sure you want to delete task “{{title}}”?', { title: taskDisplayTitle(pendingDelete, t('Untitled task')) })}</p>
        <code>{pendingDelete.id}</code>
        {deleteError && <div className="nx-alert is-error" role="alert">{deleteError}</div>}
        <div className="nx-dialog-actions">
          <button type="button" className="nx-button is-secondary" data-dialog-initial-focus onClick={() => setPendingDelete(null)} disabled={Boolean(deletingId)}>{t('Cancel')}</button>
          <button type="button" className="nx-button is-danger" aria-busy={Boolean(deletingId)} onClick={() => { void confirmDelete(); }} disabled={Boolean(deletingId)}><Trash2 size={15} />{deletingId ? t('Deleting…') : t('Confirm delete')}</button>
        </div>
      </div>
    </Dialog>}
  </>;
}

export function SkillsPage({ nodeID, refreshToken }: { nodeID: string; refreshToken: number }) {
  const { t } = useTranslation();
  const runtimeBase = `/v1/runtime/nodes/${encodeURIComponent(nodeID)}`;
  const resource = useOpsResource<SkillsResponse>(`${runtimeBase}/skills`, { ok: false, items: [], count: 0, root: '' }, refreshToken, t('Request failed'));
  const deepLinkTarget = pluginSkillTargetFromHash();
  const [query, setQuery] = useState('');
  const [selectedKey, setSelectedKey] = useState('');
  const [mobileDetailOpen, setMobileDetailOpen] = useState(() => Boolean(pluginSkillTargetFromHash()));
  const filtered = useMemo(() => {
    const needle = query.trim().toLowerCase();
    if (!needle) return resource.data.items;
    return resource.data.items.filter((item) => [item.id, item.title, item.description, item.skill_ref, item.source_type, item.source_id, item.plugin_name].filter(Boolean).join(' ').toLowerCase().includes(needle));
  }, [query, resource.data.items]);
  const deepLinkedSkill = deepLinkTarget
    ? filtered.find((item) => item.source_type === 'plugin' && item.plugin_name === deepLinkTarget.pluginName && (item.id === deepLinkTarget.skillName || item.title === deepLinkTarget.skillName))
    : undefined;
  const selected = filtered.find((item) => skillSelectionKey(item) === selectedKey) || deepLinkedSkill || filtered[0];
  const detailURL = selected ? withSkillRef(`${runtimeBase}/skills/${encodeURIComponent(selected.source)}/${encodeURIComponent(selected.id)}`, selected.skill_ref) : '';
  const detail = useOptionalOpsResource<SkillDetailResponse>(detailURL, { ok: false, skill: selected as OpsSkillDetail }, refreshToken, t('Request failed'));
  return <OpsShell error={resource.error}>
    <section className={`skills-workspace mobile-drilldown ${mobileDetailOpen ? 'is-detail-open' : 'is-list-open'}`}>
      <aside className="skills-catalog mobile-drilldown-list">
        <header className="skills-catalog-head">
          <div><span className="nexus-eyebrow">SKILLS</span><strong>{filtered.length}</strong><small>{t('Total {{count}}', { count: resource.data.count })}</small></div>
          <label className="ops-search"><Search size={15} /><input aria-label={t('Search Skill')} value={query} onChange={(event) => { setQuery(event.target.value); setMobileDetailOpen(false); }} placeholder={t('Search name or description')} /></label>
        </header>
        <div className="skills-rail">
          {filtered.length === 0 ? <EmptyOps text={t('No matching skills.')} /> : filtered.map((skill) => <button type="button" key={skillSelectionKey(skill)} className={`skill-list-item ${selected && skillSelectionKey(selected) === skillSelectionKey(skill) ? 'is-selected' : ''}`} aria-pressed={Boolean(selected && skillSelectionKey(selected) === skillSelectionKey(skill))} onClick={() => { setSelectedKey(skillSelectionKey(skill)); setMobileDetailOpen(true); }}><span className="ops-card-icon"><Layers size={16} /></span><span><strong>{skill.title || skill.id}</strong><small>{skillSourceDisplayLabel(skill, t)} · {skill.file_count > 0 ? t('{{count}} files', { count: skill.file_count }) : t('Files loaded on demand')}</small></span></button>)}
        </div>
      </aside>
      <div className="skills-detail mobile-drilldown-detail">
        {selected && <MobileDrilldownBar label="Skill" title={selected.title || selected.id} meta={skillSourceDisplayLabel(selected, t)} backLabel={t('Back to skill list')} onBack={() => setMobileDetailOpen(false)} />}
        <SkillDetail nodeID={nodeID} skill={selected} detail={detail.data.skill} loading={detail.loading} error={detail.error} refreshToken={refreshToken} />
      </div>
    </section>
  </OpsShell>;
}

function TaskDetail({ task, detail, loading, error, deleting, onDelete }: { task?: OpsTask; detail?: OpsTaskDetail; loading: boolean; error?: string; deleting: boolean; onDelete: (task: OpsTask) => void }) {
  const { t } = useTranslation();
  if (!task) return <article className="ops-detail-empty"><EmptyOps text={t('Please select a task.')} /></article>;
  const full = detail?.id ? detail : task;
  const steps = taskSteps(detail?.steps, t);
  const currentStep = full.current_step || steps.find((step) => step.status === 'in_progress') || steps.find((step) => step.status === 'pending');
  const currentTitle = currentStep?.title || (full.status === 'completed' ? t('Task completed') : full.status === 'blocked' ? t('Task blocked') : t('Waiting for next step'));
  return <article className="ops-task-detail">
    <header>
      <div><span>{t('Task')}</span><h3>{taskDisplayTitle(full, t('Untitled task'))}</h3>{full.goal && <p>{full.goal}</p>}</div>
      <div className="ops-task-detail-actions">
        <button
          type="button"
          className="nx-icon-button ops-task-delete-icon"
          title={t('Delete task')}
          aria-label={t('Delete task {{title}}', { title: taskDisplayTitle(full, t('Untitled task')) })}
          aria-busy={deleting}
          onClick={() => onDelete(full)}
          disabled={deleting}
        ><Trash2 size={16} /></button>
      </div>
    </header>
    {loading && <div className="nx-alert is-info">{t('Loading task details…')}</div>}
    {error && <div className="nx-alert is-error">{error}</div>}
    {full.blocker && <div className="ops-blocker"><ShieldAlert size={15} />{full.blocker}</div>}
    <section className="ops-current-step-text" aria-label={t('Current progress')}>
      <span>{full.status === 'completed' ? t('Result') : full.status === 'blocked' ? t('Current status') : t('Current step')}</span>
      <strong>{currentTitle}</strong>
      {full.summary && full.summary !== currentTitle && <p>{full.summary}</p>}
    </section>
    <TaskStepList steps={steps} status={full.status} />
    <footer className="ops-task-updated">{t('Updated at {{time}}', { time: formatTime(full.updated_at) })}</footer>
  </article>;
}

function SkillDetail({ nodeID, skill, detail, loading, error, refreshToken }: { nodeID: string; skill?: OpsSkill; detail?: OpsSkillDetail; loading: boolean; error?: string; refreshToken: number }) {
  const { t } = useTranslation();
  if (!skill) return <article className="ops-detail-empty"><EmptyOps text={t('Please select a Skill.')} /></article>;
  return <SkillDetailContent nodeID={nodeID} skill={skill} detail={detail} loading={loading} error={error} refreshToken={refreshToken} />;
}

function SkillDetailContent({ nodeID, skill, detail, loading, error, refreshToken }: { nodeID: string; skill: OpsSkill; detail?: OpsSkillDetail; loading: boolean; error?: string; refreshToken: number }) {
  const { t } = useTranslation();
  const [selectedPath, setSelectedPath] = useState('');
  const full = detail?.id ? detail : skill;
  const files = detail?.files || [];
  const preferredPath = files.find((file) => file.path.toLowerCase() === 'skill.md')?.path || files[0]?.path || '';
  const activePath = files.some((file) => file.path === selectedPath) ? selectedPath : preferredPath;
  const fileURL = activePath ? withSkillRef(`/v1/runtime/nodes/${encodeURIComponent(nodeID)}/skills/${encodeURIComponent(full.source)}/${encodeURIComponent(full.id)}/files/${encodePathSegments(activePath)}`, full.skill_ref) : '';
  const preview = useOptionalOpsResource<SkillFileResponse>(fileURL, { ok: false }, refreshToken, t('Request failed'));
  const raw = detail?.runtime_state;

  return <article className="skill-detail-panel">
    <header className="skill-detail-head">
      <div><span className="nexus-eyebrow">SKILL</span><h3>{full.title || full.id}</h3><p>{full.description || t('No description provided.')}</p></div>
      <StatusBadge tone={toneForStatus(full.status)}>{t('Current content')}</StatusBadge>
    </header>
    {loading && <div className="nx-alert is-info">{t('Loading Skill details…')}</div>}
    {error && <div className="nx-alert is-error">{error}</div>}

    <dl className="skill-meta" aria-label={t('Skill summary')}>
      <div><dt>{t('Source type')}</dt><dd>{skillSourceLabel(full.source_type, t)}</dd></div>
      {full.plugin_name && <div><dt>{t('Plugin')}</dt><dd>{full.plugin_name}</dd></div>}
      <div><dt>{t('Files')}</dt><dd>{files.length}</dd></div>
      <div><dt>{t('Content digest')}</dt><dd title={full.content_digest}>{shortDigest(full.content_digest)}</dd></div>
    </dl>

    <section className="skill-file-workspace">
      <aside className="skill-file-nav">
        <header><div><strong>{t('Files')}</strong><small>{t('{{count}} items', { count: files.length })}</small></div></header>
        {files.length === 0 ? <EmptyOps text={t('No files to display in current package.')} /> : <>
          <div className="skill-mobile-file-tabs" role="tablist" aria-label={t('Select file')}>{files.map((file) => <button type="button" role="tab" aria-selected={activePath === file.path} key={file.path} className={activePath === file.path ? 'is-active' : ''} onClick={() => setSelectedPath(file.path)}><FileText size={13} /><span>{file.path}</span></button>)}</div>
          <div className="skill-file-list">{files.map((file) => <button type="button" key={file.path} className={`skill-file-row ${activePath === file.path ? 'is-active' : ''}`} onClick={() => setSelectedPath(file.path)}><FileText size={15} /><span><strong>{file.path}</strong><small>{fileKindLabel(file.kind, t)} · {formatBytes(file.size_bytes)}</small></span></button>)}</div>
        </>}
      </aside>
      <div className="skill-file-preview">
        {!activePath ? <EmptyOps text={t('Select a file to view its content here.')} /> : preview.loading ? <EmptyOps text={t('Loading file…')} /> : preview.error ? <div className="nx-alert is-error">{preview.error}</div> : preview.data.file ? <>
          <header><div><strong>{preview.data.file.path}</strong><span>{fileKindLabel(preview.data.file.kind, t)} · {formatBytes(preview.data.file.size_bytes)}</span></div>{preview.data.file.truncated && <em>{t('Only first 256 KiB shown')}</em>}</header>
          <pre>{preview.data.file.content}</pre>
        </> : <EmptyOps text={t('File content unavailable.')} />}
      </div>
    </section>

    <details className="ops-secondary-details skill-technical-details">
      <summary>{t('Current content and technical information')}</summary>
      <div className="ops-detail-grid">
        <Info label="ID" value={full.id} />
        <Info label={t('Skill reference')} value={full.skill_ref} />
        <Info label={t('Source type')} value={skillSourceLabel(full.source_type, t)} />
        {full.plugin_name && <Info label={t('Plugin')} value={full.plugin_name} />}
        <Info label={t('Source ID')} value={full.source_id} />
        <Info label={t('Content digest')} value={full.content_digest} />
      </div>
      {raw && <RawJsonPanel title={t('Runtime raw response')} value={raw} />}
    </details>
  </article>;
}

function pluginSkillTargetFromHash(): { pluginName: string; skillName: string } | null {
  const [section, source, pluginName, skillName] = window.location.hash.replace(/^#\/?/, '').split('/');
  if (section !== 'skills' || source !== 'plugin' || !pluginName || !skillName) return null;
  try {
    return { pluginName: decodeURIComponent(pluginName), skillName: decodeURIComponent(skillName) };
  } catch {
    return null;
  }
}

function skillSelectionKey(skill: OpsSkill): string {
  return skill.skill_ref || `${skill.source}:${skill.id}`;
}
function withSkillRef(url: string, skillRef: string | undefined): string {
  if (!skillRef) return url;
  const separator = url.includes('?') ? '&' : '?';
  return `${url}${separator}skill_ref=${encodeURIComponent(skillRef)}`;
}
function encodePathSegments(value: string): string {
  return value.split('/').map((segment) => encodeURIComponent(segment)).join('/');
}

function fileKindLabel(kind: string, t: TFunction): string {
  if (kind === 'doc') return t('Document');
  if (kind === 'code') return t('Code');
  if (kind === 'config') return t('Config');
  if (kind === 'manifest') return t('Manifest');
  return t('File');
}

function taskSteps(values: unknown[] | undefined, t: TFunction): TaskStep[] {
  if (!Array.isArray(values)) return [];
  return values.flatMap((value, index) => {
    const record = asRecord(value);
    if (!record) return [];
    const title = pickText(record, ['title', 'name', 'text']) || t('Step {{step}}', { step: index + 1 });
    return [{ id: pickText(record, ['id']) || `step-${index + 1}`, title, status: pickText(record, ['status']) || 'pending' }];
  });
}

function TaskProgress({ task, compact = false }: { task: OpsTask; compact?: boolean }) {
  const { t } = useTranslation();
  const progress = taskProgress(task, t);
  const progressText = progress.determinate ? `${progress.label} · ${progress.percent}%` : progress.label;
  return <div className={`ops-task-progress ${compact ? 'is-compact' : ''}`}>
    {!compact && <div className="ops-task-progress-head"><strong>{t('Progress')}</strong><span>{progressText}</span></div>}
    <div className={`ops-progress-track tone-${toneForTask(task)} ${progress.determinate ? '' : 'is-undetermined'}`} role="progressbar" aria-label={t('Task progress')} aria-valuemin={0} aria-valuemax={100} aria-valuenow={progress.determinate ? progress.percent : undefined} aria-valuetext={progressText}>
      <i style={{ width: `${progress.percent}%` }} />
    </div>
    {compact && <span className="ops-progress-count">{progressText}</span>}
  </div>;
}

function taskStepStatusLabel(status: string, t: TFunction): string {
  if (status === 'completed') return t('Completed');
  if (status === 'in_progress') return t('In progress');
  return t('Pending');
}

function TaskStepList({ steps, status }: { steps: TaskStep[]; status: string }) {
  const { t } = useTranslation();
  return <section className="ops-task-steps">
    <header><h4>{t('Steps')}</h4><span>{t('{{count}} items', { count: steps.length })}</span></header>
    {steps.length === 0 ? <p className="ops-no-steps">{t('This task has no segmented steps; only task status can be displayed.')}</p> : <div className="ops-task-step-list">{steps.map((step) => {
      const stepStatus = status === 'completed' ? 'completed' : step.status;
      return <div className={`ops-task-step is-${stepStatus}`} key={step.id}>
        <span className={`ops-task-step-marker is-${stepStatus}`} role="img" aria-label={taskStepStatusLabel(stepStatus, t)}>
          {stepStatus === 'completed' ? <Check size={13} strokeWidth={3} /> : stepStatus === 'in_progress' ? <span /> : null}
        </span>
        <strong>{step.title}</strong>
      </div>;
    })}</div>}
  </section>;
}

function asRecord(value: unknown): Record<string, unknown> | null { return value && typeof value === 'object' && !Array.isArray(value) ? value as Record<string, unknown> : null; }
function pickText(record: Record<string, unknown>, keys: string[]): string {
  for (const key of keys) {
    const value = record[key];
    if (typeof value === 'string' && value.trim()) return value;
    if (typeof value === 'number' || typeof value === 'boolean') return String(value);
  }
  return '';
}
function skillSourceLabel(sourceType: string | undefined, t: TFunction): string {
  if (sourceType === 'managed') return t('AgentDock Skills');
  if (sourceType === 'shared') return t('Shared');
  if (sourceType === 'workspace') return t('Workspace');
  if (sourceType === 'plugin') return t('Plugin');
  return sourceType || t('Unknown');
}
function skillSourceDisplayLabel(skill: Pick<OpsSkill, 'source_type' | 'plugin_name'>, t: TFunction): string {
  if (skill.source_type === 'plugin' && skill.plugin_name) return skill.plugin_name;
  return skillSourceLabel(skill.source_type, t);
}
function shortDigest(value: string | undefined): string {
  if (!value) return '—';
  const [algorithm, digest] = value.split(':', 2);
  if (!digest) return value.length > 18 ? `${value.slice(0, 18)}…` : value;
  return `${algorithm}:${digest.slice(0, 12)}`;
}
function RawJsonPanel({ title, value }: { title: string; value: unknown }) {
  return <details className="ops-json-panel"><summary>{title}</summary><pre>{JSON.stringify(value, null, 2)}</pre></details>;
}

function OpsShell({ error, children }: { error?: string; children: ReactNode }) { return <section className="ops-page">{error && <div className="nx-alert is-error">{error}</div>}{children}</section>; }
function Info({ label, value }: { label: string; value: string }) { return <div><dt>{label}</dt><dd>{value}</dd></div>; }
function StatusBadge({ tone, children }: { tone: Tone; children: ReactNode }) { return <span className={`status-badge tone-${tone}`}><span />{children}</span>; }
function EmptyOps({ text }: { text: string }) { return <p className="empty-mini">{text}</p>; }
