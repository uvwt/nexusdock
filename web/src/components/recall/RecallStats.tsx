import { useTranslation } from 'react-i18next';
import { Archive, Folder } from 'lucide-react';
import type { RecallWorkspaceViewModel } from './types';

type Props = Pick<RecallWorkspaceViewModel, 'libraryFileCount' | 'directoryCount'>;

export default function RecallStats({ libraryFileCount, directoryCount }: Props) {
  const { t } = useTranslation();
  return <section className="recall-stats">
    <div><Archive size={18} /><span>{t('Files')}</span><strong>{libraryFileCount}</strong></div>
    <div><Folder size={18} /><span>{t('Directories')}</span><strong>{directoryCount}</strong></div>
  </section>;
}
