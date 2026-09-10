import i18n from 'i18next';
import { initReactI18next } from 'react-i18next';
import zhCN from './locales/zh-CN';

export type AppLocale = 'en' | 'zh-CN';
export type LocalePreference = 'system' | AppLocale;

const localeStorageKey = 'nexus.locale';

function matchLocale(value?: string | null): AppLocale | null {
  const locale = value?.trim().toLowerCase();
  if (!locale) return null;
  if (locale === 'en' || locale.startsWith('en-')) return 'en';
  const simplifiedChinese = locale === 'zh'
    || locale === 'zh-cn'
    || locale === 'zh-sg'
    || locale === 'zh-hans'
    || locale.startsWith('zh-hans-');
  if (simplifiedChinese) return 'zh-CN';
  return null;
}

export function resolveLocale(values: readonly (string | null | undefined)[]): AppLocale {
  for (const value of values) {
    const matched = matchLocale(value);
    if (matched) return matched;
  }
  return 'en';
}

function storedLocale(): AppLocale | null {
  try {
    const saved = window.localStorage.getItem(localeStorageKey);
    return saved === 'en' || saved === 'zh-CN' ? saved : null;
  } catch {
    return null;
  }
}

export function getLocalePreference(): LocalePreference {
  return storedLocale() || 'system';
}

function systemLocale(): AppLocale {
  return resolveLocale([...(window.navigator.languages || []), window.navigator.language]);
}

function preferredLocale(): AppLocale {
  return storedLocale() || systemLocale();
}

function updateDocumentLanguage(locale: AppLocale): void {
  document.documentElement.lang = locale;
}

void i18n
  .use(initReactI18next)
  .init({
    lng: preferredLocale(),
    fallbackLng: 'en',
    supportedLngs: ['en', 'zh-CN'],
    load: 'currentOnly',
    keySeparator: false,
    nsSeparator: false,
    initAsync: false,
    interpolation: { escapeValue: false },
    resources: {
      en: { translation: {} },
      'zh-CN': { translation: zhCN },
    },
  });

updateDocumentLanguage(resolveLocale([i18n.resolvedLanguage, i18n.language]));

export async function setLocale(locale: AppLocale | null): Promise<void> {
  try {
    if (locale) window.localStorage.setItem(localeStorageKey, locale);
    else window.localStorage.removeItem(localeStorageKey);
  } catch {
    // 沙箱或隐私限制环境可能禁止访问 localStorage；语言切换本身仍应继续生效。
  }
  const next = locale || systemLocale();
  await i18n.changeLanguage(next);
  updateDocumentLanguage(next);
}

export function setLocalePreference(preference: LocalePreference): Promise<void> {
  return setLocale(preference === 'system' ? null : preference);
}

window.addEventListener('languagechange', () => {
  if (getLocalePreference() !== 'system') return;
  const next = systemLocale();
  void i18n.changeLanguage(next).then(() => updateDocumentLanguage(next));
});

export default i18n;
