import { useEffect, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Check, CircleAlert, Copy, RefreshCw, WifiOff } from 'lucide-react';
import type { RuntimeDiagnosticCall, RuntimeDiagnosticStage, RuntimeDiagnosticsResponse } from '../../api/generated';
import { ApiError, api } from '../../api/client';
import { formatTime } from '../../lib/time';
import MobileDrilldownBar from '../MobileDrilldownBar';

type Props = {
  nodeID: string;
  online: boolean;
  refreshToken: number;
};

type DiagnosticsState = {
  data: RuntimeDiagnosticsResponse;
  loading: boolean;
  error?: string;
  unsupported: boolean;
};

const emptyDiagnostics: RuntimeDiagnosticsResponse = {
  ok: false,
  node_id: '',
  items: [],
  count: 0,
  source: 'agentdock-runtime-api',
};

function formatDuration(value: number): string {
  if (!Number.isFinite(value)) return '—';
  if (value < 1000) return `${value.toFixed(value < 10 ? 1 : 0)} ms`;
  return `${(value / 1000).toFixed(value < 10_000 ? 2 : 1)} s`;
}

function stageLabel(stage: RuntimeDiagnosticStage['name'], t: ReturnType<typeof useTranslation>['t']): string {
  switch (stage) {
    case 'mcp.refresh': return t('MCP refresh');
    case 'mcp.remote_call': return t('MCP remote call');
    case 'command.start': return t('Command start');
    case 'command.foreground_wait': return t('Foreground wait');
    default: return stage;
  }
}

function sourceLabel(source: RuntimeDiagnosticCall['source'], t: ReturnType<typeof useTranslation>['t']): string {
  if (source === 'nexus') return 'Nexus';
  if (source === 'mcp') return 'MCP';
  return t('Internal');
}

function loadErrorMessage(error: unknown, fallback: string): string {
  if (error instanceof ApiError) return `${error.code || error.status}: ${error.message}`;
  return error instanceof Error ? error.message : fallback;
}

export default function DiagnosticsPage({ nodeID, online, refreshToken }: Props) {
  const { t } = useTranslation();
  const [reloadToken, setReloadToken] = useState(0);
  const [selectedID, setSelectedID] = useState('');
  const [mobileDetailOpen, setMobileDetailOpen] = useState(false);
  const [copiedTraceID, setCopiedTraceID] = useState('');
  const [state, setState] = useState<DiagnosticsState>({
    data: emptyDiagnostics,
    loading: false,
    unsupported: false,
  });

  useEffect(() => {
    if (!online) {
      setState({ data: emptyDiagnostics, loading: false, unsupported: false });
      return undefined;
    }

    let cancelled = false;
    const controller = new AbortController();
    setState((current) => ({ ...current, loading: true, error: undefined, unsupported: false }));

    api<RuntimeDiagnosticsResponse>(
      `/v1/runtime/nodes/${encodeURIComponent(nodeID)}/diagnostics`,
      { signal: controller.signal },
    ).then((data) => {
      if (!cancelled) setState({ data, loading: false, unsupported: false });
    }).catch((error) => {
      if (cancelled) return;
      const unsupported = error instanceof ApiError
        && (error.status === 501 || error.code === 'AGENTDOCK_DIAGNOSTICS_UNSUPPORTED');
      setState({
        data: emptyDiagnostics,
        loading: false,
        unsupported,
        error: unsupported ? undefined : loadErrorMessage(error, t('Failed to load diagnostics')),
      });
    });

    return () => {
      cancelled = true;
      controller.abort();
    };
  }, [nodeID, online, refreshToken, reloadToken, t]);

  const selected = useMemo(
    () => state.data.items.find((item) => item.id === selectedID) || state.data.items[0],
    [selectedID, state.data.items],
  );

  useEffect(() => {
    if (!selected) setMobileDetailOpen(false);
  }, [selected]);

  if (!online) {
    return <section className="diagnostics-state-card">
      <WifiOff size={22} />
      <div>
        <strong>{t('Node offline')}</strong>
        <p>{t('Recent diagnostics are kept in the device memory and can be read again after the node reconnects.')}</p>
      </div>
    </section>;
  }

  if (state.unsupported) {
    return <section className="diagnostics-state-card">
      <CircleAlert size={22} />
      <div>
        <strong>{t('Remote diagnostics unavailable')}</strong>
        <p>{t('This AgentDock version does not support remote runtime diagnostics. Please update AgentDock.')}</p>
      </div>
    </section>;
  }

  return <section className="diagnostics-page">
    <div className="diagnostics-toolbar">
      <div>
        <strong>{t('Recent calls')}</strong>
        <span>{t('Read on demand from this node memory; Nexus does not store call history.')}</span>
      </div>
      <button
        type="button"
        className="nx-button is-secondary is-small"
        disabled={state.loading}
        onClick={() => setReloadToken((value) => value + 1)}
      >
        <RefreshCw size={15} className={state.loading ? 'spin' : ''} />
        {t('Refresh')}
      </button>
    </div>

    {state.error && <div className="nx-alert is-error" role="alert">{state.error}</div>}
    {state.loading && state.data.items.length === 0 && <div className="diagnostics-state-card is-compact">{t('Loading recent calls…')}</div>}
    {!state.loading && !state.error && state.data.items.length === 0 && <div className="diagnostics-state-card is-compact">{t('No recent calls on this node.')}</div>}

    {state.data.items.length > 0 && <section className={`diagnostics-workspace mobile-drilldown ${mobileDetailOpen ? 'is-detail-open' : 'is-list-open'}`}>
      <div className="diagnostics-rail mobile-drilldown-list">
        {state.data.items.map((call) => (
          <button
            type="button"
            key={call.id}
            className={`diagnostics-call-row ${selected?.id === call.id ? 'is-selected' : ''}`}
            aria-pressed={selected?.id === call.id}
            onClick={() => {
              setSelectedID(call.id);
              setMobileDetailOpen(true);
            }}
          >
            <span className="diagnostics-call-main">
              <strong>{call.tool}</strong>
              <span className={`diagnostics-result ${call.success ? 'is-success' : 'is-failure'}`}>
                {call.success ? t('Success') : t('Failed')}
              </span>
            </span>
            <span className="diagnostics-call-meta">
              <time>{formatTime(call.started_at, { seconds: true, compact: true })}</time>
              <span>{formatDuration(call.duration_ms)}</span>
              <span>{sourceLabel(call.source, t)}</span>
            </span>
          </button>
        ))}
      </div>

      <div className="diagnostics-detail-wrap mobile-drilldown-detail">
        {selected && <MobileDrilldownBar
          label={t('Call details')}
          title={selected.tool}
          meta={selected.success ? t('Success') : t('Failed')}
          backLabel={t('Back to recent calls')}
          onBack={() => setMobileDetailOpen(false)}
        />}
        {selected && <article className="diagnostics-detail">
          <header>
            <div>
              <span>{t('Runtime call')}</span>
              <h3>{selected.tool}</h3>
              <p>{formatTime(selected.started_at, { seconds: true })}</p>
            </div>
            <span className={`diagnostics-result is-large ${selected.success ? 'is-success' : 'is-failure'}`}>
              {selected.success ? t('Success') : t('Failed')}
            </span>
          </header>

          <dl className="diagnostics-summary">
            <div><dt>{t('Duration')}</dt><dd>{formatDuration(selected.duration_ms)}</dd></div>
            <div><dt>{t('Source')}</dt><dd>{sourceLabel(selected.source, t)}</dd></div>
            {!selected.success && selected.error_code && <div><dt>{t('Error code')}</dt><dd><code>{selected.error_code}</code></dd></div>}
            {!selected.success && selected.error_category && <div><dt>{t('Error category')}</dt><dd>{selected.error_category}</dd></div>}
          </dl>

          <section className="diagnostics-stages">
            <header>
              <span>{t('Stages')}</span>
              <small>{selected.stages?.length || 0}</small>
            </header>
            {!selected.stages?.length && <p className="diagnostics-muted">{t('No stage details for this call.')}</p>}
            {selected.stages?.map((stage, index) => {
              const maxDuration = Math.max(...(selected.stages || []).map((item) => item.duration_ms), 1);
              const width = Math.max(2, Math.min(100, (stage.duration_ms / maxDuration) * 100));
              return <div className="diagnostics-stage" key={`${stage.name}-${index}`}>
                <div className="diagnostics-stage-line">
                  <span>{stageLabel(stage.name, t)}</span>
                  <strong>{formatDuration(stage.duration_ms)}</strong>
                </div>
                <div className="diagnostics-stage-track" aria-hidden="true">
                  <span style={{ width: `${width}%` }} className={stage.success ? 'is-success' : 'is-failure'} />
                </div>
                <small>{t('Starts at +{{offset}} ms', { offset: stage.started_offset_ms.toFixed(1) })}</small>
              </div>;
            })}
          </section>

          {selected.trace_id && <details className="diagnostics-advanced">
            <summary>{t('Advanced information')}</summary>
            <div>
              <span>{t('Trace ID')}</span>
              <code title={selected.trace_id}>{selected.trace_id}</code>
              <button
                type="button"
                className="nx-icon-button"
                aria-label={t('Copy Trace ID')}
                title={t('Copy Trace ID')}
                onClick={() => {
                  void navigator.clipboard.writeText(selected.trace_id || '').then(() => {
                    setCopiedTraceID(selected.trace_id || '');
                    window.setTimeout(() => setCopiedTraceID(''), 1600);
                  });
                }}
              >
                {copiedTraceID === selected.trace_id ? <Check size={15} /> : <Copy size={15} />}
              </button>
            </div>
          </details>}
        </article>}
      </div>
    </section>}
  </section>;
}
