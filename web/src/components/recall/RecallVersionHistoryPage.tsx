import { useTranslation } from 'react-i18next';
import { formatTime } from '../../lib/time';
import type { RecallWorkspaceViewModel } from './types';

type Props = Pick<RecallWorkspaceViewModel, 'state' | 'changedCount' | 'dirty' | 'actions'>;

export default function RecallVersionHistoryPage({ state, changedCount, dirty, actions }: Props) {
  const { t } = useTranslation();
  const gitEnabled = Boolean(state.gitDiff?.git_repo);
  return <section className="recall-version-page">
    <article className="recall-tool-panel recall-version-panel">
      <div className="recall-panel-head"><div><h2>{t('Local Versions')}</h2><p>{t('Recall only tracks local Git versions; data protection is managed by the host.')}</p></div></div>
      <div className="recall-version-state">
        <div><span>{t('Repository')}</span><strong>{gitEnabled ? t('Enabled') : t('Not enabled')}</strong></div>
        <div><span>{t('Local changes')}</span><strong>{dirty ? t('{{count}} unrecorded changes', { count: changedCount }) : t('None')}</strong></div>
        <div><span>{t('Recent versions')}</span><strong>{state.commits[0]?.short_hash || '—'}</strong></div>
      </div>
      {dirty && <div className="recall-version-actions">
        <button type="button" className="primary" onClick={actions.recordVersion} disabled={state.busy || !gitEnabled}>{t('Record current version')}</button>
      </div>}
    </article>

    <article className="recall-tool-panel recall-history-panel">
      <div className="recall-panel-head"><div><h2>{t('Version History')}</h2><p>{t('Display recent local version history.')}</p></div></div>
      <div className="recall-commits">
        {state.commits.length === 0 ? <p className="recall-empty">{t('No version history yet.')}</p> : state.commits.map((commit) => (
          <div key={commit.hash}>
            <span />
            <div><strong>{commit.subject || t('(No description)')}</strong><small>{commit.short_hash} · {commit.author} · {formatTime(commit.date)}</small></div>
          </div>
        ))}
      </div>
    </article>
  </section>;
}
