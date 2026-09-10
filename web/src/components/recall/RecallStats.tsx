import { useTranslation } from 'react-i18next';
import { Archive, Clock3, Folder, GitBranch } from 'lucide-react';
import type { RecallWorkspaceViewModel } from './types';

type Props = Pick<RecallWorkspaceViewModel, 'libraryFileCount' | 'directoryCount' | 'changedCount' | 'state'>;

export default function RecallStats({ libraryFileCount, directoryCount, changedCount, state }: Props) {
  const { t } = useTranslation();
  return <section className="recall-stats">
    <div><Archive size={18} /><span>{t('Files')}</span><strong>{libraryFileCount}</strong></div>
    <div><Folder size={18} /><span>{t('Directories')}</span><strong>{directoryCount}</strong></div>
    <div><GitBranch size={18} /><span>{t('Local changes')}</span><strong>{changedCount}</strong></div>
    <div><Clock3 size={18} /><span>{t('Recent versions')}</span><strong>{state.commits.length}</strong></div>
  </section>;
}
