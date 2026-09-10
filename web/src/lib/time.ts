import i18n from '../i18n';

const DEFAULT_TIME_ZONE = 'Asia/Shanghai';
const ISO_WITHOUT_ZONE = /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}(:\d{2}(\.\d+)?)?$/;
const TIME_FORMATTERS = new Map<string, Intl.DateTimeFormat>();

function displayTimeZone(): string {
  try {
    return Intl.DateTimeFormat().resolvedOptions().timeZone || DEFAULT_TIME_ZONE;
  } catch {
    return DEFAULT_TIME_ZONE;
  }
}

export function timeZoneLabel(): string {
  return displayTimeZone().replace(/_/g, ' ');
}

function parseTimestamp(value?: string): Date | null {
  if (!value) return null;
  const raw = value.trim();
  if (!raw) return null;
  const normalized = ISO_WITHOUT_ZONE.test(raw) ? `${raw}Z` : raw;
  const date = new Date(normalized);
  return Number.isNaN(date.getTime()) ? null : date;
}

export function formatTime(value?: string, options: { seconds?: boolean; compact?: boolean; fallback?: string } = {}): string {
  const date = parseTimestamp(value);
  if (!date) return value || options.fallback || i18n.t('None');
  const timeZone = displayTimeZone();
  const locale = i18n.resolvedLanguage || i18n.language || 'en';
  const key = [locale, timeZone, options.seconds ? 'seconds' : 'minutes', options.compact ? 'compact' : 'offset'].join(':');
  let formatter = TIME_FORMATTERS.get(key);
  if (!formatter) {
    formatter = new Intl.DateTimeFormat(locale, {
      timeZone,
      year: '2-digit',
      month: '2-digit',
      day: '2-digit',
      hour: '2-digit',
      minute: '2-digit',
      ...(options.seconds ? { second: '2-digit' as const } : {}),
      hour12: false,
      timeZoneName: options.compact ? undefined : 'shortOffset',
    });
    TIME_FORMATTERS.set(key, formatter);
  }
  return formatter.format(date);
}

