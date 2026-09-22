import { ApiError } from './api/client';
import i18n from './i18n';

export function safeReturnTo(raw: string | null): string {
  if (!raw || !raw.startsWith('/') || raw.startsWith('//') || /[\r\n]/.test(raw)) return '/ui/';
  try {
    const value = new URL(raw, window.location.origin);
    return value.origin === window.location.origin ? `${value.pathname}${value.search}${value.hash}` : '/ui/';
  } catch {
    return '/ui/';
  }
}

export function errorMessage(error: unknown): string {
  if (error instanceof ApiError) {
    if (error.code === 'INVALID_CREDENTIALS') return i18n.t('Incorrect username or password.');
    if (error.code === 'LOGIN_RATE_LIMITED') return i18n.t('Too many attempts. Please try again later.');
    if (error.code === 'ADMIN_NOT_INITIALIZED') return i18n.t('The administrator has not been initialized. Run the initialization command on the NexusDock host.');
    if (error.code === 'HTTPS_REQUIRED') return i18n.t('Sign-in is only allowed over a secure HTTPS connection.');
    if (error.code === 'CURRENT_CREDENTIAL_INVALID') return i18n.t('The current password is incorrect.');
    if (error.code === 'CREDENTIAL_POLICY_FAILED') return error.message;
    return error.message;
  }
  return error instanceof Error ? error.message : i18n.t('Request failed. Please try again later.');
}
