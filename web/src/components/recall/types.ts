import type { FormEvent, RefObject } from 'react';

export type EntryType = 'file' | 'directory';
export type RecallPage = 'library' | 'cards' | 'evolution' | 'vectors';
export type RecallEntry = { path: string; name?: string; type: EntryType; size_bytes?: number; modified?: string };
export type Recall = {
  path: string;
  content: string;
  body?: string;
  frontmatter?: Record<string, string>;
  size_bytes?: number;
};
export type RecallCardSummary = {
  path: string;
  title: string;
  project: string;
  status: string;
  card_type: string;
  scope?: string;
  confidence?: string;
  tags?: string[];
  size_bytes?: number;
  modified?: string;
};
export type Notice = { text: string; danger?: boolean } | null;
export type PendingRecallAction =
  | { kind: 'move'; path: string; nextPath: string; error?: string }
  | { kind: 'delete'; path: string; error?: string }
  | null;

export type EmbeddingStatus = {
  enabled?: boolean;
  configured?: boolean;
  reachable?: boolean;
  model?: string;
  endpoint?: string;
  index_path?: string;
  index?: { model?: string; count?: number; dimension?: number; updated_at?: string };
  error?: string;
  reason?: string;
};
export type EmbeddingSearchResult = { path: string; title?: string; score: number };
export type EmbeddingSearchResponse = { ok?: boolean; count?: number; model?: string; index?: { count?: number; dimension?: number }; results?: EmbeddingSearchResult[] };

export type EmbeddingPanelState = {
  status: EmbeddingStatus | null;
  query: string;
  results: EmbeddingSearchResult[];
};

export type RecallWorkspaceState = {
  entries: RecallEntry[];
  libraryEntries: RecallEntry[];
  cardEntries: RecallCardSummary[];
  current: Recall | null;
  draftPath: string;
  draftContent: string;
  editing: boolean;
  creating: boolean;
  query: string;
  appliedQuery: string;
  loading: boolean;
  busy: boolean;
  notice: Notice;
  draftAvailable: boolean;
  pendingAction: PendingRecallAction;
  embedding: EmbeddingPanelState;
};

export type RecallWorkspaceActions = {
  refreshAll: () => void;
  restoreDraft: () => void;
  discardDraft: () => void;
  clearNotice: () => void;
  closePendingAction: () => void;
  updatePendingMovePath: (value: string) => void;
  confirmMove: () => void;
  confirmDelete: () => void;
  searchMemories: (event?: FormEvent) => void;
  clearSearch: () => void;
  setQuery: (value: string) => void;
  openRecall: (path: string) => void;
  startNew: () => void;
  backToFileList: () => void;
  startEdit: () => void;
  requestMove: () => void;
  requestDelete: () => void;
  cancelEdit: () => void;
  saveRecall: () => void;
  setDraftPath: (value: string) => void;
  setDraftContent: (value: string) => void;
  openSimilarCard: (path: string) => void;
  setEmbeddingQuery: (value: string) => void;
  searchCardEmbeddings: (event?: FormEvent) => void;
  reindexCards: () => void;
};

export type RecallWorkspaceViewModel = {
  state: RecallWorkspaceState;
  fileEntries: RecallEntry[];
  libraryFileCount: number;
  directoryCount: number;
  hasUnsavedChanges: boolean;
  detailOpen: boolean;
  editorRef: RefObject<HTMLElement | null>;
  actions: RecallWorkspaceActions;
};
