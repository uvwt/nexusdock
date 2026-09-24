import { useEffect, useMemo, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Cable, CircleAlert, Package, Search, Terminal, Wrench } from 'lucide-react';
import { ApiError, api } from '../../api/client';
import { formatTime } from '../../lib/time';
import MobileDrilldownBar from '../MobileDrilldownBar';

type PluginProvenance = {
  origin: string;
  ref?: string;
  revision?: string;
  subdir?: string;
};

type PluginSummary = {
  name: string;
  version: string;
  enabled: boolean;
  package_digest: string;
  provenance?: PluginProvenance;
  format: string;
  skill_count: number;
  mcp_count: number;
};

type PluginSkill = { name: string; description?: string; path: string; content_digest: string };
type PluginMCP = {
  name: string;
  description?: string;
  transport: string;
  url?: string;
  command?: string;
  cwd?: string;
  runtime_name?: string;
};

type PluginDetail = PluginSummary & {
  description?: string;
  installed_at: string;
  skills: PluginSkill[];
  mcp: PluginMCP[];
  executables: string[];
  warnings: string[];
};

type PluginListResponse = { ok: boolean; items: PluginSummary[]; count: number };
type PluginDetailResponse = { ok: boolean; plugin: PluginDetail };

function errorMessage(error: unknown, fallback: string): string {
  if (error instanceof ApiError) return `${error.code || error.status}: ${error.message}`;
  return error instanceof Error ? error.message : fallback;
}

function shortDigest(value: string): string {
  if (!value) return '—';
  return value.length > 18 ? `${value.slice(0, 10)}…${value.slice(-6)}` : value;
}

function pluginSkillHref(pluginName: string, skillName: string): string {
  return `#skills/plugin/${encodeURIComponent(pluginName)}/${encodeURIComponent(skillName)}`;
}

function pluginMCPHref(mcp: PluginMCP): string {
  return `#mcp/${encodeURIComponent(mcp.runtime_name || mcp.name)}`;
}

export default function PluginPage({ nodeID, refreshToken }: { nodeID: string; refreshToken: number }) {
  const { t } = useTranslation();
  const runtimeBase = `/v1/runtime/nodes/${encodeURIComponent(nodeID)}`;
  const [plugins, setPlugins] = useState<PluginSummary[]>([]);
  const [selectedName, setSelectedName] = useState('');
  const [detail, setDetail] = useState<PluginDetail | null>(null);
  const [query, setQuery] = useState('');
  const [loading, setLoading] = useState(true);
  const [listError, setListError] = useState('');
  const [detailError, setDetailError] = useState('');
  const [mobileDetailOpen, setMobileDetailOpen] = useState(false);
  const detailRequestRef = useRef(0);

  useEffect(() => {
    let active = true;
    setLoading(true);
    setListError('');
    api<PluginListResponse>(`${runtimeBase}/plugins`)
      .then((result) => {
        if (!active) return;
        setPlugins(result.items || []);
        setSelectedName((current) => result.items?.some((item) => item.name === current) ? current : result.items?.[0]?.name || '');
      })
      .catch((error) => {
        if (active) setListError(errorMessage(error, t('Failed to load Plugins')));
      })
      .finally(() => {
        if (active) setLoading(false);
      });
    return () => { active = false; };
  }, [nodeID, refreshToken, runtimeBase, t]);

  const filtered = useMemo(() => {
    const needle = query.trim().toLowerCase();
    if (!needle) return plugins;
    return plugins.filter((plugin) => [
      plugin.name, plugin.version, plugin.provenance?.origin, plugin.provenance?.ref, plugin.provenance?.revision,
      plugin.provenance?.subdir, plugin.format,
    ].filter(Boolean).join(' ').toLowerCase().includes(needle));
  }, [plugins, query]);

  const selected = filtered.find((plugin) => plugin.name === selectedName) || filtered[0] || null;

  useEffect(() => {
    const name = selected?.name || '';
    setDetail(null);
    setDetailError('');
    if (!name) return;
    const requestID = ++detailRequestRef.current;
    api<PluginDetailResponse>(`${runtimeBase}/plugins/${encodeURIComponent(name)}`)
      .then((result) => {
        if (requestID === detailRequestRef.current) setDetail(result.plugin);
      })
      .catch((error) => {
        if (requestID === detailRequestRef.current) setDetailError(errorMessage(error, t('Failed to load Plugin details')));
      });
  }, [runtimeBase, selected?.name, t]);

  return <section className={`plugin-layout mobile-drilldown ${mobileDetailOpen ? 'is-detail-open' : 'is-list-open'}`}>
    <aside className="plugin-list-panel mobile-drilldown-list">
      <header className="plugin-list-head">
        <div><span className="nexus-eyebrow">PLUGINS</span><strong>{filtered.length}</strong><small>{t('Total {{count}}', { count: plugins.length })}</small></div>
        <label className="ops-search"><Search size={15} /><input aria-label={t('Search Plugin')} value={query} onChange={(event) => { setQuery(event.target.value); setMobileDetailOpen(false); }} placeholder={t('Search Plugin name or provenance')} /></label>
      </header>
      {listError && <div className="nx-alert is-error">{listError}</div>}
      <div className="plugin-list">
        {loading ? <p className="empty-mini">{t('Loading Plugins…')}</p> : filtered.length === 0 ? <p className="empty-mini">{plugins.length === 0 ? t('No Plugins installed.') : t('No matching Plugins.')}</p> : filtered.map((plugin) => (
          <button type="button" key={plugin.name} className={`plugin-list-item ${selected?.name === plugin.name ? 'is-selected' : ''}`} aria-pressed={selected?.name === plugin.name} onClick={() => { setSelectedName(plugin.name); setMobileDetailOpen(true); }}>
            <span className="ops-card-icon"><Package size={16} /></span>
            <span className="plugin-list-copy"><strong>{plugin.name}</strong><small>v{plugin.version} · {plugin.skill_count} Skill · {plugin.mcp_count} MCP</small></span>
            <span className={`status-badge tone-${plugin.enabled ? 'ok' : 'muted'}`}><span />{plugin.enabled ? t('Enabled') : t('Disabled')}</span>
          </button>
        ))}
      </div>
    </aside>

    <div className="plugin-detail mobile-drilldown-detail">
      {selected && <MobileDrilldownBar label="Plugin" title={selected.name} meta={selected.enabled ? t('Enabled') : t('Disabled')} backLabel={t('Back to Plugin list')} onBack={() => setMobileDetailOpen(false)} />}
      {!selected ? <article className="ops-detail-empty"><p className="empty-mini">{t('Please select a Plugin.')}</p></article> : (
        <article className="plugin-detail-card">
          <header>
            <div><span className="nexus-eyebrow">PLUGIN</span><h3>{selected.name}</h3><p>{detail?.description || t('No description provided.')}</p></div>
            <span className={`status-badge tone-${selected.enabled ? 'ok' : 'muted'}`}><span />{selected.enabled ? t('Enabled') : t('Disabled')}</span>
          </header>
          {detailError && <div className="nx-alert is-error">{detailError}</div>}
          {!detail && !detailError && <div className="nx-alert is-info">{t('Loading Plugin details…')}</div>}

          <section className="plugin-metrics" aria-label={t('Plugin summary')}>
            <div><span>{t('Version')}</span><strong>{selected.version}</strong></div>
            <div><span>Skill</span><strong>{detail?.skills.length ?? selected.skill_count}</strong></div>
            <div><span>MCP</span><strong>{detail?.mcp.length ?? selected.mcp_count}</strong></div>
            <div><span>{t('Format')}</span><strong>{detail?.format || selected.format}</strong></div>
          </section>

          <section className="plugin-section">
            <h4>{t('Provenance & installation')}</h4>
            <dl className="plugin-meta">
              <div><dt>{t('Origin')}</dt><dd title={selected.provenance?.origin}>{selected.provenance?.origin || t('Local Portable package')}</dd></div>
              {selected.provenance?.ref && <div><dt>{t('Ref')}</dt><dd>{selected.provenance.ref}</dd></div>}
              {selected.provenance?.revision && <div><dt>{t('Revision')}</dt><dd>{selected.provenance.revision}</dd></div>}
              {selected.provenance?.subdir && <div><dt>{t('Subdirectory')}</dt><dd>{selected.provenance.subdir}</dd></div>}
              {detail?.installed_at && <div><dt>{t('Installed at')}</dt><dd>{formatTime(detail.installed_at)}</dd></div>}
              <div><dt>{t('Package digest')}</dt><dd title={selected.package_digest}>{shortDigest(selected.package_digest)}</dd></div>
            </dl>
          </section>

          {detail && <section className="plugin-section">
            <h4>{t('Components')}</h4>
            <div className="plugin-component-grid">
              <div className="plugin-component-card">
                <header><Wrench size={15} /><strong>Skill</strong><span>{detail.skills.length}</span></header>
                {detail.skills.length === 0 ? <p className="empty-mini">{t('No Skill components.')}</p> : detail.skills.map((skill) => <a className="plugin-component-row is-link" href={pluginSkillHref(selected.name, skill.name)} key={skill.name}><span><strong>{skill.name}</strong><small>{skill.description || skill.path}</small></span><code>{skill.path}</code></a>)}
              </div>
              <div className="plugin-component-card">
                <header><Cable size={15} /><strong>MCP</strong><span>{detail.mcp.length}</span></header>
                {detail.mcp.length === 0 ? <p className="empty-mini">{t('No MCP components.')}</p> : detail.mcp.map((mcp) => <a className="plugin-component-row is-link" href={pluginMCPHref(mcp)} key={mcp.name}><span><strong>{mcp.name}</strong><small>{mcp.description || mcp.transport}</small></span><code>{mcp.runtime_name || mcp.transport}</code></a>)}
              </div>
            </div>
          </section>}

          {detail && detail.executables.length > 0 && <section className="plugin-section">
            <h4><Terminal size={15} />{t('Executables')}</h4>
            <div className="plugin-chip-list">{detail.executables.map((item) => <code key={item}>{item}</code>)}</div>
          </section>}

          {detail && detail.warnings.length > 0 && <section className="plugin-section plugin-warnings">
            <h4><CircleAlert size={15} />{t('Runtime notes')}</h4>
            {detail.warnings.map((message, index) => <p key={`${index}:${message}`}>{message}</p>)}
          </section>}
        </article>
      )}
    </div>
  </section>;
}
