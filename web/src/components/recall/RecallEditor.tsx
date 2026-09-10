import { useTranslation } from 'react-i18next';
import { FileText, Pencil, Plus, Save, Trash2 } from 'lucide-react';
import type { RecallWorkspaceViewModel } from './types';
import { nameOf } from './utils';

type Props = Pick<RecallWorkspaceViewModel, 'state' | 'hasUnsavedChanges' | 'editorRef' | 'actions'>;

export default function RecallEditor({ state, hasUnsavedChanges, editorRef, actions }: Props) {
  const { t } = useTranslation();
  const title = state.editing
    ? state.creating ? t('New recall entry') : t('Edit recall entry')
    : state.current ? nameOf(state.current.path) : t('Select a recall entry');
  const subtitle = state.editing ? state.draftPath : state.current?.path || t('Open from the file list on the left, or create a new recall entry.');
  return <article className="recall-editor" ref={editorRef} aria-labelledby="recall-editor-title" aria-busy={state.busy}>
    <div className="recall-panel-head">
      <div><h2 id="recall-editor-title" title={title}>{title}</h2><p title={subtitle}>{subtitle}</p></div>
      <button className="recall-mobile-back" type="button" onClick={actions.backToFileList}>{t('Back to files')}</button>
      <div className="recall-editor-actions">
        {!state.editing && state.current && <button type="button" onClick={actions.startEdit}><Pencil size={15} />{t('Edit')}</button>}
        {!state.editing && state.current && <button type="button" onClick={actions.requestMove}>{t('Move')}</button>}
        {!state.editing && state.current && <button type="button" className="danger" onClick={actions.requestDelete}><Trash2 size={15} />{t('Delete')}</button>}
        {state.editing && <button type="button" onClick={actions.cancelEdit}>{t('Cancel')}</button>}
        {state.editing && <button type="button" className="primary" onClick={actions.saveRecall} disabled={state.busy || !hasUnsavedChanges}><Save size={15} />{t('Save')}</button>}
      </div>
    </div>
    {state.editing ? (
      <div className="recall-edit-body">
        <label htmlFor="recall-draft-path"><span>{t('Path')}</span><input id="recall-draft-path" name="path" autoComplete="off" spellCheck={false} value={state.draftPath} onChange={(event) => actions.setDraftPath(event.target.value)} disabled={!state.creating || state.busy} /></label>
        <label className="content" htmlFor="recall-draft-content"><span>{t('Content')}</span><textarea id="recall-draft-content" name="content" aria-describedby="recall-draft-meta" value={state.draftContent} onChange={(event) => actions.setDraftContent(event.target.value)} disabled={state.busy} spellCheck={false} /></label>
        <small id="recall-draft-meta" aria-live="polite">{t('{{count}} characters · Draft saved automatically in current browser session', { count: state.draftContent.length })}</small>
      </div>
    ) : state.current ? (
      <pre className="recall-preview">{state.current.content}</pre>
    ) : (
      <div className="recall-empty large"><FileText size={28} /><strong>{t('No recall entry open')}</strong><span>{t('Select a file or create a new recall entry.')}</span><button type="button" className="primary" onClick={actions.startNew}><Plus size={15} />{t('New recall entry')}</button></div>
    )}
  </article>;
}
