import { useEffect, useState } from 'react';
import RecallWorkspaceView from './components/recall/RecallWorkspaceView';
import type { RecallPage } from './components/recall/types';
import { useRecallWorkspaceController } from './components/recall/useRecallWorkspaceController';
import './recall.css';

const recallPages: RecallPage[] = ['library', 'cards', 'evolution', 'vectors'];

function recallPageFromHash(): RecallPage {
  const [, page] = window.location.hash.replace(/^#\/?/, '').split('/');
  return recallPages.includes(page as RecallPage) ? page as RecallPage : 'library';
}

export default function RecallWorkspace({ refreshToken }: { refreshToken: number }) {
  const [page, setPage] = useState<RecallPage>(recallPageFromHash);
  const viewModel = useRecallWorkspaceController(refreshToken);

  useEffect(() => {
    const onHash = () => setPage(recallPageFromHash());
    window.addEventListener('hashchange', onHash);
    return () => window.removeEventListener('hashchange', onHash);
  }, []);

  function navigate(next: RecallPage) {
    window.location.hash = `recall/${next}`;
    setPage(next);
  }

  return <RecallWorkspaceView {...viewModel} page={page} refreshToken={refreshToken} onNavigate={navigate} />;
}
