import { useEffect, useMemo, useState, type FormEvent } from 'react';
import { useTranslation } from 'react-i18next';
import { FolderKanban, Save, ShieldCheck } from 'lucide-react';
import { ApiError, api } from '../../api/client';
import { formatTime } from '../../lib/time';
import type { AgentDockNode } from './AgentDockNodes';

type Workspace = {
  id: string;
  name: string;
  node_id: string;
  project_root: string;
  domain?: string;
  allowed_mcp: string[];
  context_roots: string[];
  design_authorities: string[];
  route_authority?: string;
  created_at: string;
  updated_at: string;
};

type WorkspaceListResponse = { ok: boolean; items: Workspace[] };
type WorkspaceResponse = { ok: boolean; workspace: Workspace };

type WorkspaceDraft = {
  name: string;
  nodeID: string;
  projectRoot: string;
  domain: string;
  allowedMCP: string;
  contextRoots: string;
  designAuthorities: string;
  routeAuthority: string;
};

function apiMessage(error: unknown, fallback: string): string {
  if (error instanceof ApiError) return `${error.code || error.status}: ${error.message}`;
  return error instanceof Error ? error.message : fallback;
}

function lines(values: string[]): string {
  return values.join('\n');
}

function parseLines(value: string): string[] {
  const seen = new Set<string>();
  return value
    .split(/\r?\n/)
    .map((item) => item.trim())
    .filter((item) => {
      if (!item || seen.has(item)) return false;
      seen.add(item);
      return true;
    });
}

function draftFromWorkspace(item: Workspace): WorkspaceDraft {
  return {
    name: item.name,
    nodeID: item.node_id,
    projectRoot: item.project_root,
    domain: item.domain || '',
    allowedMCP: lines(item.allowed_mcp || []),
    contextRoots: lines(item.context_roots || []),
    designAuthorities: lines(item.design_authorities || []),
    routeAuthority: item.route_authority || '',
  };
}

function payloadFromDraft(draft: WorkspaceDraft) {
  return {
    name: draft.name.trim(),
    node_id: draft.nodeID.trim(),
    project_root: draft.projectRoot.trim(),
    domain: draft.domain.trim(),
    allowed_mcp: parseLines(draft.allowedMCP),
    context_roots: parseLines(draft.contextRoots),
    design_authorities: parseLines(draft.designAuthorities),
    route_authority: draft.routeAuthority.trim(),
  };
}

function payloadFromWorkspace(item: Workspace) {
  return {
    name: item.name,
    node_id: item.node_id,
    project_root: item.project_root,
    domain: item.domain || '',
    allowed_mcp: item.allowed_mcp || [],
    context_roots: item.context_roots || [],
    design_authorities: item.design_authorities || [],
    route_authority: item.route_authority || '',
  };
}

export default function WorkspacePage({ refreshToken, nodes }: { refreshToken: number; nodes: AgentDockNode[] }) {
  const { t } = useTranslation();
  const [items, setItems] = useState<Workspace[]>([]);
  const [selectedID, setSelectedID] = useState('');
  const [draft, setDraft] = useState<WorkspaceDraft | null>(null);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState('');
  const [notice, setNotice] = useState('');
  const [revision, setRevision] = useState(0);

  useEffect(() => {
    let cancelled = false;
    setLoading(true);
    setError('');
    api<WorkspaceListResponse>('/v1/runtime/workspaces').then((result) => {
      if (cancelled) return;
      const next = result.items || [];
      setItems(next);
      setSelectedID((current) => current && next.some((item) => item.id === current) ? current : (next[0]?.id || ''));
    }).catch((cause) => {
      if (!cancelled) setError(apiMessage(cause, t('Failed to load Workspaces')));
    }).finally(() => {
      if (!cancelled) setLoading(false);
    });
    return () => { cancelled = true; };
  }, [refreshToken, revision, t]);

  const selected = useMemo(
    () => items.find((item) => item.id === selectedID) || null,
    [items, selectedID],
  );

  useEffect(() => {
    setDraft(selected ? draftFromWorkspace(selected) : null);
  }, [selected]);

  const dirty = Boolean(selected && draft
    && JSON.stringify(payloadFromDraft(draft)) !== JSON.stringify(payloadFromWorkspace(selected)));

  async function save(event: FormEvent) {
    event.preventDefault();
    if (!selected || !draft || saving) return;
    setSaving(true);
    setError('');
    setNotice('');
    try {
      const result = await api<WorkspaceResponse>(`/v1/runtime/workspaces/${encodeURIComponent(selected.id)}`, {
        method: 'PATCH',
        body: JSON.stringify(payloadFromDraft(draft)),
      });
      setItems((current) => current.map((item) => item.id === result.workspace.id ? result.workspace : item));
      setDraft(draftFromWorkspace(result.workspace));
      setNotice(t('Workspace “{{name}}” updated.', { name: result.workspace.name }));
    } catch (cause) {
      setError(apiMessage(cause, t('Failed to save Workspace')));
    } finally {
      setSaving(false);
    }
  }

  function reset() {
    if (selected) setDraft(draftFromWorkspace(selected));
    setNotice('');
    setError('');
  }

  return <section className="workspace-admin">
    <header className="workspace-admin-head">
      <div>
        <span className="nexus-eyebrow">PROJECT WORKSPACES</span>
        <h2>{t('Workspace management')}</h2>
        <p>{t('View and edit the project boundaries used by strict Runtime calls. Changes take effect immediately after saving.')}</p>
      </div>
      <button type="button" className="nx-button is-secondary is-small" onClick={() => setRevision((value) => value + 1)} disabled={loading}>
        {loading ? t('Loading…') : t('Reload')}
      </button>
    </header>

    {error && <div className="nx-alert is-error">{error}</div>}
    {notice && <div className="nx-alert is-success">{notice}</div>}

    <div className="workspace-admin-layout">
      <aside className="workspace-list" aria-label={t('Configured Workspaces')}>
        <div className="workspace-list-summary">
          <strong>{t('{{count}} Workspaces', { count: items.length })}</strong>
          <small>{t('Stored in Nexus and enforced before routed AgentDock calls.')}</small>
        </div>
        {loading && items.length === 0 && <p className="empty-mini">{t('Loading Workspaces…')}</p>}
        {!loading && items.length === 0 && <p className="empty-mini">{t('No Workspaces configured.')}</p>}
        <div className="workspace-list-items">
          {items.map((item) => {
            const node = nodes.find((candidate) => candidate.id === item.node_id);
            return <button
              type="button"
              key={item.id}
              className={item.id === selectedID ? 'is-selected' : ''}
              onClick={() => {
                setNotice('');
                setError('');
                setSelectedID(item.id);
              }}
            >
              <span className="workspace-list-icon"><FolderKanban size={16} /></span>
              <span className="workspace-list-copy">
                <strong>{item.name}</strong>
                <code>{item.id}</code>
                <small>{item.domain || t('No domain restriction')}</small>
              </span>
              <span className={`workspace-node-dot ${node?.online ? 'is-online' : 'is-offline'}`} title={node?.online ? t('Online') : t('Offline')} />
            </button>;
          })}
        </div>
      </aside>

      <div className="workspace-detail">
        {!selected || !draft ? <div className="workspace-empty">
          <FolderKanban size={28} />
          <strong>{t('Select a Workspace')}</strong>
          <span>{t('Choose a Workspace on the left to inspect or edit its boundaries.')}</span>
        </div> : <form onSubmit={save}>
          <header className="workspace-detail-head">
            <div>
              <span className="workspace-detail-title"><ShieldCheck size={17} /><strong>{selected.name}</strong></span>
              <code>{selected.id}</code>
            </div>
            <span className="workspace-detail-updated">{t('Updated at {{time}}', { time: formatTime(selected.updated_at) })}</span>
          </header>

          <div className="workspace-form-grid">
            <label>
              <span>{t('Workspace ID')}</span>
              <input value={selected.id} readOnly disabled />
              <small>{t('Stable identifier used by Runtime calls; it cannot be edited here.')}</small>
            </label>
            <label>
              <span>{t('Display name')}</span>
              <input required maxLength={100} value={draft.name} onChange={(event) => setDraft({ ...draft, name: event.target.value })} />
            </label>
            <label>
              <span>{t('Bound AgentDock node')}</span>
              <select required value={draft.nodeID} onChange={(event) => setDraft({ ...draft, nodeID: event.target.value })}>
                <option value="">{t('Please select a node')}</option>
                {nodes.map((node) => <option key={node.id} value={node.id}>{node.name}{node.online ? '' : t(' (offline)')}</option>)}
              </select>
              <small>{draft.nodeID}</small>
            </label>
            <label>
              <span>{t('Allowed domain')}</span>
              <input placeholder="example.com" value={draft.domain} onChange={(event) => setDraft({ ...draft, domain: event.target.value })} />
              <small>{t('Optional. URL-bearing tool arguments are restricted to this host and its subdomains.')}</small>
            </label>
            <label className="is-wide">
              <span>{t('Project root')}</span>
              <input required value={draft.projectRoot} onChange={(event) => setDraft({ ...draft, projectRoot: event.target.value })} />
              <small>{t('Workspace writes are restricted to this root.')}</small>
            </label>
            <label className="is-wide">
              <span>{t('Allowed MCP servers')}</span>
              <textarea rows={4} value={draft.allowedMCP} onChange={(event) => setDraft({ ...draft, allowedMCP: event.target.value })} placeholder="elementor-project\nwordpress-project" />
              <small>{t('One MCP server name per line. Strict Workspace calls can only use servers in this allowlist.')}</small>
            </label>
            <label className="is-wide">
              <span>{t('Read-only context roots')}</span>
              <textarea rows={3} value={draft.contextRoots} onChange={(event) => setDraft({ ...draft, contextRoots: event.target.value })} placeholder="D:\\website\\Project" />
              <small>{t('One absolute path per line. These roots extend read access but never write access.')}</small>
            </label>
            <label className="is-wide">
              <span>{t('Design authorities')}</span>
              <textarea rows={3} value={draft.designAuthorities} onChange={(event) => setDraft({ ...draft, designAuthorities: event.target.value })} placeholder="DESIGN.md" />
              <small>{t('Metadata for project design/content authority files, one entry per line.')}</small>
            </label>
            <label className="is-wide">
              <span>{t('Route authority')}</span>
              <input value={draft.routeAuthority} onChange={(event) => setDraft({ ...draft, routeAuthority: event.target.value })} placeholder="website_link.md" />
              <small>{t('Optional route authority file used to validate URL-bearing MCP operations.')}</small>
            </label>
          </div>

          <footer className="workspace-form-actions">
            <span>{dirty ? t('Unsaved changes') : t('No unsaved changes')}</span>
            <div>
              <button type="button" className="nx-button is-secondary" onClick={reset} disabled={!dirty || saving}>{t('Reset')}</button>
              <button type="submit" className="nx-button" disabled={!dirty || saving}><Save size={15} />{saving ? t('Saving…') : t('Save changes')}</button>
            </div>
          </footer>
        </form>}
      </div>
    </div>
  </section>;
}
