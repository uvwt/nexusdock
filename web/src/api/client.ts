import i18n from '../i18n';

export type ApiErrorBody = {
  error?: {
    code?: string;
    message?: string;
    details?: unknown;
    request_id?: string;
    trace_id?: string;
    upstream_code?: string;
  };
  code?: string;
  message?: string;
  details?: unknown;
  request_id?: string;
  trace_id?: string;
  upstream_code?: string;
};

export class ApiError extends Error {
  readonly status: number;
  readonly code?: string;
  readonly details?: unknown;
  readonly traceId?: string;
  readonly upstreamCode?: string;

  constructor(message: string, status: number, body: ApiErrorBody = {}) {
    super(message);
    this.name = 'ApiError';
    this.status = status;
    this.code = body.error?.code ?? body.code;
    this.details = body.error?.details ?? body.details;
    this.traceId = body.error?.trace_id ?? body.error?.request_id ?? body.trace_id ?? body.request_id;
    this.upstreamCode = body.error?.upstream_code ?? body.upstream_code;
  }
}

export type ApiOptions = RequestInit & { timeoutMs?: number };

let csrfToken = '';

export function setCSRFToken(value: string): void {
  csrfToken = value;
}

export function clearCSRFToken(): void {
  csrfToken = '';
}

function requestURL(path: string): URL {
  const url = new URL(path, window.location.origin);
  url.username = '';
  url.password = '';
  if (url.origin !== window.location.origin) {
    throw new ApiError(i18n.t('Cross-origin API requests are blocked to protect administrative credentials.'), 0);
  }
  return url;
}

function isUnsafeMethod(method?: string): boolean {
  const normalized = (method || 'GET').toUpperCase();
  return !['GET', 'HEAD', 'OPTIONS'].includes(normalized);
}

function shouldSignalSessionExpiry(path: string, status: number): boolean {
  return status === 401 && !path.startsWith('/v1/auth/login') && !path.startsWith('/v1/auth/status');
}

export async function api<T>(path: string, options: ApiOptions = {}): Promise<T> {
  const controller = new AbortController();
  const timeoutMs = options.timeoutMs ?? 15_000;
  const timeout = window.setTimeout(() => controller.abort(), timeoutMs);
  const externalSignal = options.signal;
  const abort = () => controller.abort();
  if (externalSignal?.aborted) {
    controller.abort();
  } else {
    // 调用方可能在请求创建前已经取消，必须同步接住，避免过期页面继续发起 API 请求。
    externalSignal?.addEventListener('abort', abort, { once: true });
  }

  try {
    const headers = new Headers(options.headers);
    headers.set('Accept', 'application/json');
    if (options.body && !headers.has('Content-Type')) headers.set('Content-Type', 'application/json');
    if (csrfToken && isUnsafeMethod(options.method) && !headers.has('X-CSRF-Token')) {
      headers.set('X-CSRF-Token', csrfToken);
    }
    const url = requestURL(path);

    const response = await fetch(url.toString(), {
      ...options,
      headers,
      signal: controller.signal,
      credentials: 'same-origin',
    });

    if (response.status === 204) return undefined as T;

    const text = await response.text();
    let body: ApiErrorBody | T = {} as T;
    if (text) {
      try {
        body = JSON.parse(text) as ApiErrorBody | T;
      } catch {
        throw new ApiError(i18n.t('The service returned an unreadable response.'), response.status, { error: { code: 'INVALID_JSON' } });
      }
    }

    const errorBody = body as ApiErrorBody;
    if (!response.ok || errorBody.error || errorBody.code) {
      if (shouldSignalSessionExpiry(path, response.status)) {
        clearCSRFToken();
        window.dispatchEvent(new CustomEvent('nexus:session-expired'));
      }
      throw new ApiError(
        errorBody.error?.message || errorBody.message || response.statusText || i18n.t('Request failed'),
        response.status,
        errorBody,
      );
    }
    return body as T;
  } catch (error) {
    if (error instanceof ApiError) throw error;
    if (error instanceof DOMException && error.name === 'AbortError') {
      throw new Error(externalSignal?.aborted ? i18n.t('Request cancelled') : i18n.t('Request timed out'));
    }
    throw error instanceof Error ? error : new Error(i18n.t('Network request failed'));
  } finally {
    window.clearTimeout(timeout);
    externalSignal?.removeEventListener('abort', abort);
  }
}
