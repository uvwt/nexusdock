import { useTranslation } from 'react-i18next';
import Dialog from '../Dialog';
import type { RecallWorkspaceViewModel } from './types';

type Props = Pick<RecallWorkspaceViewModel, 'state' | 'actions'>;

export default function RecallActionDialog({ state, actions }: Props) {
  const { t } = useTranslation();
  const pending = state.pendingAction;
  if (!pending) return null;
  const pendingError = 'error' in pending ? pending.error : undefined;
  return <Dialog
    title={pending.kind === 'move' ? t('Move recall entry') : t('Delete recall entry')}
    description={pending.kind === 'move' ? t('Changing the path preserves file content and refreshes the currently open recall entry.') : t('Deleting generates a local Git change that will not reach remote until synced.')}
    onClose={() => { if (!state.busy) actions.closePendingAction(); }}
  >
    <div className="recall-dialog-body">
      {pending.kind === 'move' ? (
        <label htmlFor="recall-move-path">
          <span>{t('New recall path')}</span>
          <input id="recall-move-path" name="path" autoComplete="off" spellCheck={false} aria-invalid={Boolean(pendingError)} aria-describedby={pendingError ? 'recall-action-error' : undefined} value={pending.nextPath} onChange={(event) => actions.updatePendingMovePath(event.target.value)} />
        </label>
      ) : (
        <div className="recall-danger-box">
          <strong>{t('Are you sure you want to delete this recall entry?')}</strong>
          <code>{pending.path}</code>
        </div>
      )}
      {pendingError && <p id="recall-action-error" className="recall-dialog-error" role="alert">{pendingError}</p>}
      <div className="recall-dialog-actions">
        <button type="button" onClick={actions.closePendingAction} disabled={state.busy}>{t('Cancel')}</button>
        {pending.kind === 'move'
          ? <button className="primary" type="button" aria-busy={state.busy} onClick={actions.confirmMove} disabled={state.busy}>{state.busy ? t('Moving…') : t('Confirm move')}</button>
          : <button className="danger" type="button" aria-busy={state.busy} onClick={actions.confirmDelete} disabled={state.busy}>{state.busy ? t('Deleting…') : t('Confirm delete')}</button>}
      </div>
    </div>
  </Dialog>;
}
