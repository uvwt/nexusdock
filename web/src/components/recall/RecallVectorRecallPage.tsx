import { useTranslation } from 'react-i18next';
import { Cpu, Search } from 'lucide-react';
import type { RecallWorkspaceViewModel } from './types';
import { nameOf } from './utils';

type Props = Pick<RecallWorkspaceViewModel, 'state' | 'actions'> & {
  onOpenCard: (path: string) => void;
};

export default function RecallVectorRecallPage({ state, actions, onOpenCard }: Props) {
  const { t } = useTranslation();
  const { embedding, busy } = state;
  return <section className="recall-tool-page">
    <article className="recall-tool-panel">
      <div className="recall-panel-head">
        <div><h2>{t('Vector Recall')}</h2><p>{t('Searches only experience card index for high signal-to-noise semantic recall.')}</p></div>
        <span className={`recall-health ${embedding.status?.reachable ? 'ok' : 'warn'}`}>{embedding.status?.reachable ? t('BGE-M3 reachable') : t('Not ready')}</span>
      </div>
      <div className="recall-vector-status">
        <div><span>{t('Model')}</span><strong>{embedding.status?.index?.model || embedding.status?.model || t('Unknown')}</strong></div>
        <div><span>{t('Dimension')}</span><strong>{String(embedding.status?.index?.dimension ?? '—')}</strong></div>
        <div><span>{t('Index')}</span><strong>{embedding.status?.index?.count === undefined ? '—' : t('{{count}} items', { count: embedding.status.index.count })}</strong></div>
      </div>
      <form className="recall-vector-search" onSubmit={actions.searchCardEmbeddings}>
        <Search size={15} />
        <input aria-label={t('Search experience cards')} value={embedding.query} onChange={(event) => actions.setEmbeddingQuery(event.target.value)} placeholder={t('Enter natural language to search experience cards')} />
        <button type="submit" className="primary" disabled={busy}>{t('Vector search')}</button>
        <button type="button" onClick={actions.reindexCards} disabled={busy}><Cpu size={15} />{t('Rebuild index')}</button>
      </form>
      <div className="recall-vector-results">
        {embedding.results.length === 0 ? <p className="recall-empty">{t('No vector search results.')}</p> : embedding.results.map((item) => <button key={item.path} type="button" onClick={() => onOpenCard(item.path)}><strong>{item.title || nameOf(item.path)}</strong><small>{item.path}</small><em>{item.score.toFixed(4)}</em></button>)}
      </div>
    </article>
  </section>;
}
