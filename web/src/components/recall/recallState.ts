import { loadRecallDraft } from '../../lib/drafts';
import type { EmbeddingPanelState, PendingRecallAction, Recall, RecallCardSummary, RecallEntry, RecallWorkspaceState, Notice } from './types';

function initialEmbedding(): EmbeddingPanelState {
  return { status: null, query: '', results: [] };
}

export function initialRecallState(): RecallWorkspaceState {
  const saved = loadRecallDraft();
  const initialQuery = new URLSearchParams(window.location.search).get('q') || '';
  return {
    entries: [],
    libraryEntries: [],
    cardEntries: [],
    current: null,
    draftPath: '',
    draftContent: '',
    editing: false,
    creating: false,
    query: initialQuery,
    appliedQuery: initialQuery,
    loading: true,
    busy: false,
    notice: null,
    draftAvailable: Boolean(saved?.path || saved?.content),
    pendingAction: null,
    embedding: initialEmbedding(),
  };
}

type RecallAction =
  | { type: 'load:start' }
  | { type: 'load:finish' }
  | { type: 'busy'; busy: boolean }
  | { type: 'notice'; notice: Notice }
  | { type: 'libraryEntries'; entries: RecallEntry[] }
  | { type: 'cardEntries'; entries: RecallCardSummary[] }
  | { type: 'searchApplied'; query: string; entries: RecallEntry[] }
  | { type: 'embedding:status'; status: EmbeddingPanelState['status'] }
  | { type: 'embedding:query'; query: string }
  | { type: 'embedding:results'; results: EmbeddingPanelState['results'] }
  | { type: 'query'; query: string }
  | { type: 'opened'; recall: Recall; preserveDraftAvailable?: boolean }
  | { type: 'newDraft'; path: string; content: string }
  | { type: 'editCurrent' }
  | { type: 'draft:path'; path: string }
  | { type: 'draft:content'; content: string }
  | { type: 'cancelEdit' }
  | { type: 'clearSelection' }
  | { type: 'restoreDraft'; path: string; content: string; current?: Recall }
  | { type: 'draftAvailable'; available: boolean }
  | { type: 'pending'; pendingAction: PendingRecallAction }
  | { type: 'pendingMovePath'; path: string }
  | { type: 'pendingError'; error: string };

export function recallReducer(state: RecallWorkspaceState, action: RecallAction): RecallWorkspaceState {
  switch (action.type) {
    case 'load:start':
      return { ...state, loading: true };
    case 'load:finish':
      return { ...state, loading: false };
    case 'busy':
      return { ...state, busy: action.busy };
    case 'notice':
      return { ...state, notice: action.notice };
    case 'libraryEntries':
      return {
        ...state,
        libraryEntries: action.entries,
        entries: state.appliedQuery ? state.entries : action.entries,
      };
    case 'cardEntries':
      return { ...state, cardEntries: action.entries };
    case 'searchApplied':
      return { ...state, query: action.query, appliedQuery: action.query, entries: action.entries };
    case 'embedding:status':
      return { ...state, embedding: { ...state.embedding, status: action.status } };
    case 'embedding:query':
      return { ...state, embedding: { ...state.embedding, query: action.query } };
    case 'embedding:results':
      return { ...state, embedding: { ...state.embedding, results: action.results } };
    case 'query':
      return { ...state, query: action.query };
    case 'opened':
      return {
        ...state,
        current: action.recall,
        draftPath: action.recall.path,
        draftContent: action.recall.content,
        editing: false,
        creating: false,
        draftAvailable: action.preserveDraftAvailable ? state.draftAvailable : false,
      };
    case 'newDraft':
      return { ...state, current: null, draftPath: action.path, draftContent: action.content, editing: true, creating: true };
    case 'editCurrent':
      if (!state.current) return state;
      return { ...state, draftPath: state.current.path, draftContent: state.current.content, editing: true, creating: false };
    case 'draft:path':
      return { ...state, draftPath: action.path };
    case 'draft:content':
      return { ...state, draftContent: action.content };
    case 'cancelEdit':
      return {
        ...state,
        draftPath: state.current?.path || '',
        draftContent: state.current?.content || '',
        editing: false,
        creating: false,
        draftAvailable: false,
      };
    case 'clearSelection':
      return { ...state, current: null, draftPath: '', draftContent: '', editing: false, creating: false };
    case 'restoreDraft':
      return {
        ...state,
        current: action.current ?? null,
        draftPath: action.path,
        draftContent: action.content,
        editing: true,
        creating: !action.current,
        draftAvailable: false,
      };
    case 'draftAvailable':
      return { ...state, draftAvailable: action.available };
    case 'pending':
      return { ...state, pendingAction: action.pendingAction };
    case 'pendingMovePath':
      return state.pendingAction?.kind === 'move'
        ? { ...state, pendingAction: { ...state.pendingAction, nextPath: action.path, error: undefined } }
        : state;
    case 'pendingError':
      return state.pendingAction ? { ...state, pendingAction: { ...state.pendingAction, error: action.error } } : state;
  }
}
