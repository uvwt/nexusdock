import { useTranslation } from 'react-i18next';
import { RefreshCw } from 'lucide-react';
import type { RecallWorkspaceViewModel } from './types';

type Props = Pick<RecallWorkspaceViewModel, 'state' | 'changedCount' | 'dirty' | 'actions'>;

export default function RecallHeader({ state, changedCount, dirty, actions }: Props) {
  const { t } = useTranslation();
  return <header className="recall-header">
    <div>
      <span className="recall-kicker">NEXUS RECALL</span>
      <h1>{t('Recall Library')}</h1>
    </div>
    <div className="recall-header-actions">
      <span className={`recall-health ${dirty ? 'warn' : 'ok'}`}>{dirty ? t('{{count}} unrecorded changes', { count: changedCount }) : t('Version recorded')}</span>
      <button type="button" aria-label={t('Refresh recall library')} title={t('Refresh recall library')} aria-busy={state.loading} onClick={actions.refreshAll} disabled={state.loading || state.busy}><RefreshCw size={15} /><span>{t('Refresh')}</span></button>
    </div>
  </header>;
}
