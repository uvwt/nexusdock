import { ArrowLeft } from 'lucide-react';
import { useTranslation } from 'react-i18next';

type MobileDrilldownBarProps = {
  label: string;
  title: string;
  meta?: string;
  backLabel?: string;
  onBack: () => void;
};

export default function MobileDrilldownBar({ label, title, meta, backLabel, onBack }: MobileDrilldownBarProps) {
  const { t } = useTranslation();
  return <header className="mobile-drilldown-bar">
    <button type="button" onClick={onBack} aria-label={backLabel || t('Back to {{label}} list', { label })}><ArrowLeft size={18} /></button>
    <div><span>{label}</span><strong>{title}</strong></div>
    {meta && <em>{meta}</em>}
  </header>;
}
