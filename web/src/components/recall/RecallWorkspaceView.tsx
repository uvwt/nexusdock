import { useTranslation } from 'react-i18next';
import { useMemo } from 'react';
import type { RecallPage, RecallWorkspaceViewModel } from './types';
import RecallActionDialog from './RecallActionDialog';
import RecallEditor from './RecallEditor';
import RecallExperienceCardsPage from './RecallExperienceCardsPage';
import RecallEvolutionPage from './RecallEvolutionPage';
import RecallFileBrowser from './RecallFileBrowser';
import RecallNoticeArea from './RecallNoticeArea';
import RecallStats from './RecallStats';
import RecallVersionHistoryPage from './RecallVersionHistoryPage';
import RecallVectorRecallPage from './RecallVectorRecallPage';

type Props = RecallWorkspaceViewModel & {
  page: RecallPage;
  refreshToken: number;
  onNavigate: (page: RecallPage) => void;
};

export default function RecallWorkspaceView(props: Props) {
  const { t } = useTranslation();

  const recallNavigation = useMemo<Array<{ id: RecallPage; label: string }>>(() => [
    { id: 'library', label: t('Library') },
    { id: 'cards', label: t('Experience Cards') },
    { id: 'evolution', label: t('Evolution') },
    { id: 'vectors', label: t('Vector Recall') },
    { id: 'history', label: t('Version History') },
  ], [t]);

  function openCardFromTools(path: string) {
    props.onNavigate('library');
    props.actions.openSimilarCard(path);
  }

  return <section className={`recall-workspace ${props.detailOpen ? 'is-detail-open' : ''}`}>
    <nav className="recall-subnav" aria-label={t('Recall navigation')}>
      {recallNavigation.map((item) => <button type="button" key={item.id} className={props.page === item.id ? 'is-active' : ''} aria-current={props.page === item.id ? 'page' : undefined} onClick={() => props.onNavigate(item.id)}><strong>{item.label}</strong></button>)}
      <span className={`recall-health ${props.dirty ? 'warn' : 'ok'}`}>{props.dirty ? t('{{count}} unrecorded changes', { count: props.changedCount }) : t('Version recorded')}</span>
    </nav>
    <RecallNoticeArea {...props} />
    <RecallActionDialog {...props} />

    {props.page === 'library' && <>
      <RecallStats {...props} />
      <section className="recall-grid">
        <RecallFileBrowser {...props} />
        <RecallEditor {...props} />
      </section>
    </>}
    {props.page === 'cards' && <RecallExperienceCardsPage entries={props.state.cardEntries} loading={props.state.loading} />}
    {props.page === 'evolution' && <RecallEvolutionPage refreshToken={props.refreshToken} />}
    {props.page === 'vectors' && <RecallVectorRecallPage state={props.state} actions={props.actions} onOpenCard={openCardFromTools} />}
    {props.page === 'history' && <RecallVersionHistoryPage state={props.state} changedCount={props.changedCount} dirty={props.dirty} actions={props.actions} />}
  </section>;
}
