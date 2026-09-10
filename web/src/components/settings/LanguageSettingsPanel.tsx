import { useState } from 'react';
import { Languages } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import {
  getLocalePreference,
  setLocalePreference,
  type LocalePreference,
} from '../../i18n';

export default function LanguageSettingsPanel() {
  const { t } = useTranslation();
  const [preference, setPreference] = useState<LocalePreference>(getLocalePreference);

  async function changeLanguage(next: LocalePreference) {
    setPreference(next);
    await setLocalePreference(next);
  }

  return (
    <section className="language-settings-panel">
      <header>
        <span className="security-icon"><Languages size={19} /></span>
        <div><h2>{t('Display language')}</h2><p>{t('Choose how NexusDock selects the interface language')}</p></div>
      </header>
      <label>
        <span>{t('Interface language')}</span>
        <select value={preference} onChange={(event) => void changeLanguage(event.target.value as LocalePreference)}>
          <option value="system">{t('Follow system')}</option>
          <option value="zh-CN">{t('Simplified Chinese')}</option>
          <option value="en">{t('English')}</option>
        </select>
      </label>
      <small>{t('Changes apply immediately. Follow system uses your browser language.')}</small>
    </section>
  );
}
