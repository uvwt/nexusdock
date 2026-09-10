import i18n from '../../i18n';

export function createNewRecallTemplate(title: string): string {
  return `---
type: recall
scope: inbox
source: user-confirmed
confidence: medium
---

# ${title}

`;
}


export function normalizePath(value: string): string {
  return String(value || '').replace(/^\/+|\/+$/g, '').replace(/\/+/g, '/');
}

export function nameOf(path: string): string {
  const parts = normalizePath(path).split('/').filter(Boolean);
  return parts.at(-1) || path;
}

export function formatBytes(value?: number): string {
  if (!value) return '—';
  if (value < 1024) return `${value} B`;
  if (value < 1024 * 1024) return `${(value / 1024).toFixed(1)} KiB`;
  return `${(value / 1024 / 1024).toFixed(1)} MiB`;
}

export function initialPath(): string {
  return normalizePath(new URLSearchParams(window.location.search).get('path') || '');
}

export function updateRoute(path = '', query = '') {
  const params = new URLSearchParams(window.location.search);
  params.delete('tab');
  params.delete('prefix');
  if (path) params.set('path', path); else params.delete('path');
  if (query) params.set('q', query); else params.delete('q');
  const next = `${window.location.pathname}${params.size ? `?${params.toString()}` : ''}#recall/library`;
  window.history.replaceState(null, '', next);
}

export function messageOf(reason: unknown, fallback?: string): string {
  if (reason instanceof Error) return reason.message;
  return fallback || i18n.t('Operation failed');
}

export function usesSinglePaneRecallLayout(): boolean {
  return window.matchMedia('(max-width: 980px)').matches;
}
