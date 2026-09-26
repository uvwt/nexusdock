import { useCallback, useEffect, useMemo, useState, type FormEvent, type ReactNode } from 'react';
import { useTranslation } from 'react-i18next';
import { CirclePlus, Copy, Pencil, Server, Trash2 } from 'lucide-react';
import { api } from '../../api/client';
import Dialog from '../Dialog';

const selectedNodeStorageKey = 'nexus:runtime-node-id';

type Notice = { tone: 'success' | 'error' | 'info'; text: string };

export type AgentDockNode = {
  id: string;
  device_id: string;
  name: string;
  enabled: boolean;
  version?: string;
  protocol_version?: string;
  os?: string;
  arch?: string;
  capabilities: string[];
  tool_contract_hash?: string;
  online: boolean;
  last_seen_at?: string;
  created_at: string;
  updated_at: string;
};

type NodeListResponse = { ok: boolean; nodes: AgentDockNode[]; count: number };
type NodeResponse = { ok: boolean; node: AgentDockNode };
type PairingResponse = { ok: boolean; pairing: { code: string; expires_at: string } };

export function useAgentDockNodes(refreshToken: number) {
  const { t } = useTranslation();
  const [nodes, setNodes] = useState<AgentDockNode[]>([]);
  const [selectedNodeID, setSelectedNodeID] = useState(() => window.localStorage.getItem(selectedNodeStorageKey) || '');
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [revision, setRevision] = useState(0);
  const reload = useCallback(() => setRevision((value) => value + 1), []);
  const selectNode = useCallback((nodeID: string) => {
    setSelectedNodeID(nodeID);
    if (nodeID) window.localStorage.setItem(selectedNodeStorageKey, nodeID);
    else window.localStorage.removeItem(selectedNodeStorageKey);
  }, []);

  useEffect(() => {
    let cancelled = false;
    setLoading(true);
    setError('');
    api<NodeListResponse>('/v1/runtime/nodes').then((result) => {
      if (cancelled) return;
      const next = result.nodes || [];
      setNodes(next);
      if (selectedNodeID && !next.some((node) => node.id === selectedNodeID && node.enabled)) selectNode('');
    }).catch((cause) => {
      if (!cancelled) setError(cause instanceof Error ? cause.message : t('Failed to load AgentDock nodes'));
    }).finally(() => {
      if (!cancelled) setLoading(false);
    });
    return () => { cancelled = true; };
  }, [refreshToken, revision, selectedNodeID, selectNode, t]);

  const selectedNode = useMemo(
    () => nodes.find((node) => node.id === selectedNodeID && node.enabled) || null,
    [nodes, selectedNodeID],
  );
  return { nodes, selectedNode, selectedNodeID, loading, error, reload, selectNode };
}

export function AgentDockNodeSelector({ nodes, selectedNodeID, onSelect }: {
  nodes: AgentDockNode[];
  selectedNodeID: string;
  onSelect: (nodeID: string) => void;
}) {
  const { t } = useTranslation();
  return <label className="runtime-node-selector">
    <Server size={15} />
    <span>AgentDock</span>
    <select aria-label={t('Select AgentDock node')} value={selectedNodeID} onChange={(event) => onSelect(event.target.value)}>
      <option value="">{t('Please select a node')}</option>
      {nodes.filter((node) => node.enabled).map((node) => <option key={node.id} value={node.id}>{node.name}{node.online ? '' : t(' (offline)')}</option>)}
    </select>
  </label>;
}

export function AgentDockNodeRequired({ children }: { children?: ReactNode }) {
  const { t } = useTranslation();
  return <section className="runtime-node-required">
    <span><Server size={25} /></span>
    <h2>{t('Please select an AgentDock node')}</h2>
    <p>{t('Node operations must explicitly specify a device. Please select from above, or pair an AgentDock first.')}</p>
    {children}
  </section>;
}

export function AgentDockNodesPanel({ nodes, selectedNodeID, loading, error, onReload, onSelect }: {
  nodes: AgentDockNode[];
  selectedNodeID: string;
  loading: boolean;
  error: string;
  onReload: () => void;
  onSelect: (nodeID: string) => void;
}) {
  const { t } = useTranslation();
  const [editing, setEditing] = useState<AgentDockNode | null>(null);
  const [editName, setEditName] = useState('');
  const [editEnabled, setEditEnabled] = useState(true);
  const [deleting, setDeleting] = useState<AgentDockNode | null>(null);
  const [pairing, setPairing] = useState<PairingResponse['pairing'] | null>(null);
  const [busy, setBusy] = useState('');
  const [notice, setNotice] = useState<Notice | null>(null);

  async function createPairingCode() {
    setBusy('pair');
    setNotice(null);
    try {
      const result = await api<PairingResponse>('/v1/runtime/nodes/pairing-codes', { method: 'POST' });
      setPairing(result.pairing);
    } catch (cause) {
      setNotice({ tone: 'error', text: cause instanceof Error ? cause.message : t('Failed to generate pairing code') });
    } finally {
      setBusy('');
    }
  }

  function openEdit(node: AgentDockNode) {
    setEditing(node);
    setEditName(node.name);
    setEditEnabled(node.enabled);
  }

  async function submitEdit(event: FormEvent) {
    event.preventDefault();
    if (!editing || busy) return;
    setBusy('save');
    try {
      const result = await api<NodeResponse>(`/v1/runtime/nodes/${encodeURIComponent(editing.id)}`, {
        method: 'PATCH', body: JSON.stringify({ name: editName.trim(), enabled: editEnabled }),
      });
      setEditing(null);
      onReload();
      setNotice({ tone: 'success', text: t('Updated {{name}}', { name: result.node.name }) });
    } catch (cause) {
      setNotice({ tone: 'error', text: cause instanceof Error ? cause.message : t('Failed to save node') });
    } finally {
      setBusy('');
    }
  }

  async function remove() {
    if (!deleting || busy) return;
    setBusy('delete');
    try {
      await api(`/v1/runtime/nodes/${encodeURIComponent(deleting.id)}`, { method: 'DELETE' });
      if (selectedNodeID === deleting.id) onSelect('');
      setDeleting(null);
      onReload();
      setNotice({ tone: 'success', text: t('Deleted {{name}}', { name: deleting.name }) });
    } catch (cause) {
      setNotice({ tone: 'error', text: cause instanceof Error ? cause.message : t('Failed to delete node') });
    } finally {
      setBusy('');
    }
  }

  const pairCommand = pairing
    ? `agentdock nexus pair --endpoint ${window.location.origin} --code ${pairing.code}`
    : '';

  const orderedNodes = [
    ...nodes.filter((node) => node.enabled),
    ...nodes.filter((node) => !node.enabled),
  ];

  return <section className="agentdock-nodes-panel">
    <header className="agentdock-nodes-head">
      <div className="agentdock-nodes-title">
        <span className="nexus-panel-icon"><Server size={17} /></span>
        <div><h3>{t('AgentDock nodes')}</h3></div>
      </div>
      <div className="agentdock-node-actions">
        <button type="button" className="nx-button" onClick={() => void createPairingCode()} disabled={busy === 'pair'}><CirclePlus size={15} />{busy === 'pair' ? t('Generating…') : t('Pair device')}</button>
      </div>
    </header>

    {(error || notice) && <div className={`nx-alert is-${error || notice?.tone === 'error' ? 'error' : notice?.tone || 'info'}`}>{error || notice?.text}</div>}

    <div className="agentdock-node-list">
      {loading && nodes.length === 0 ? <p className="empty-mini">{t('Loading AgentDock nodes…')}</p> : nodes.length === 0 ? <p className="empty-mini">{t('No AgentDock nodes have been paired.')}</p> : orderedNodes.map((node) => <article key={node.id} className={selectedNodeID === node.id ? 'is-selected' : ''}>
        <span className="agentdock-node-icon"><Server size={18} /></span>
        <div className="agentdock-node-copy">
          <div><strong>{node.name}</strong><code>{node.id}</code>{!node.enabled && <em>{t('Node disabled')}</em>}</div>
          <small>{node.os && node.arch ? `${node.os}/${node.arch}` : t('Waiting for first connection')}{node.version ? ` · AgentDock ${node.version}` : ''}</small>
          <span className={`agentdock-node-status ${node.online ? 'is-online' : 'is-offline'}`}><strong>{node.online ? t('Online') : t('Offline')}</strong><span>· {t('{{count}} node tools', { count: node.capabilities?.length || 0 })}{node.last_seen_at ? ` · ${t('Recent {{time}}', { time: new Date(node.last_seen_at).toLocaleString() })}` : ''}</span></span>
        </div>
        <div className="agentdock-node-row-actions">
          <button type="button" className="nx-button is-secondary is-small" disabled={!!busy} onClick={() => openEdit(node)}><Pencil size={14} />{t('Edit')}</button>
          <button type="button" className="nx-button is-danger is-small" disabled={!!busy} onClick={() => setDeleting(node)}><Trash2 size={14} />{t('Delete')}</button>
        </div>
      </article>)}
    </div>

    {pairing && <Dialog title={t('Pair AgentDock')} description={t('Pairing code expires at {{time}} and can only be used once.', { time: new Date(pairing.expires_at).toLocaleString() })} onClose={() => setPairing(null)} wide>
      <div className="agentdock-pairing">
        <p>{t('Run the following command on the target device, then restart AgentDock:')}</p>
        <div className="agentdock-pair-command">
          <span aria-hidden="true">$</span>
          <code>{pairCommand}</code>
          <button type="button" aria-label={t('Copy command')} title={t('Copy command')} onClick={() => void navigator.clipboard.writeText(pairCommand)}><Copy size={18} /></button>
        </div>
        <p className="agentdock-pairing-hint">{t('Windows and Mac users can fill this in directly in the AgentDock control panel.')}</p>
      </div>
    </Dialog>}

    {editing && <Dialog title={t('Edit {{name}}', { name: editing.name })} description={t('Device identity and connection credentials are managed by the pairing process.')} onClose={() => setEditing(null)}>
      <form className="agentdock-node-form" onSubmit={submitEdit}>
        <label className="is-wide"><span>{t('Display name')}</span><input required maxLength={100} value={editName} onChange={(event) => setEditName(event.target.value)} /></label>
        <label className="agentdock-node-check"><input type="checkbox" checked={editEnabled} onChange={(event) => setEditEnabled(event.target.checked)} /><span>{t('Enable node')}</span></label>
        <footer><button type="button" className="nx-button is-secondary" onClick={() => setEditing(null)}>{t('Cancel')}</button><button type="submit" className="nx-button" disabled={busy === 'save'}>{busy === 'save' ? t('Saving…') : t('Save')}</button></footer>
      </form>
    </Dialog>}

    {deleting && <Dialog title={t('Delete AgentDock node')} description={t('The Device Token for this device will be revoked immediately; local AgentDock service is unaffected.')} onClose={() => setDeleting(null)}>
      <div className="agentdock-node-delete"><p>{t('Are you sure you want to delete “{{name}}”?', { name: deleting.name })}</p><code>{deleting.id}</code><footer><button type="button" className="nx-button is-secondary" onClick={() => setDeleting(null)}>{t('Cancel')}</button><button type="button" className="nx-button is-danger" disabled={busy === 'delete'} onClick={() => void remove()}>{busy === 'delete' ? t('Deleting…') : t('Confirm delete')}</button></footer></div>
    </Dialog>}
  </section>;
}
