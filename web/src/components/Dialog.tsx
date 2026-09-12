import { useEffect, useId, useRef, type ReactNode } from 'react';
import { X } from 'lucide-react';
import { useTranslation } from 'react-i18next';

export default function Dialog({ title, description, children, onClose, wide = false }: {
  title: string;
  description?: string;
  children: ReactNode;
  onClose: () => void;
  wide?: boolean;
}) {
  const { t } = useTranslation();
  const dialogRef = useRef<HTMLDialogElement>(null);
  const panelRef = useRef<HTMLDivElement>(null);
  const titleId = useId();
  const descriptionId = description ? `${titleId}-description` : undefined;

  useEffect(() => {
    const previous = document.activeElement as HTMLElement | null;
    const dialog = dialogRef.current;
    if (dialog && !dialog.open) dialog.showModal();
    const initialFocus = panelRef.current?.querySelector<HTMLElement>(
      '[data-dialog-initial-focus], input:not(:disabled), select:not(:disabled), textarea:not(:disabled), button:not(:disabled):not(.nx-icon-button)',
    );
    initialFocus?.focus();
    return () => {
      previous?.focus();
    };
  }, []);

  return (
    <dialog
      ref={dialogRef}
      className="nx-dialog-backdrop"
      aria-labelledby={titleId}
      aria-describedby={descriptionId}
      onCancel={(event) => { event.preventDefault(); onClose(); }}
    >
      <div ref={panelRef} className={`nx-dialog ${wide ? 'is-wide' : ''}`}>
        <header>
          <div><h2 id={titleId}>{title}</h2>{description && <p id={descriptionId}>{description}</p>}</div>
          <button type="button" className="nx-icon-button" aria-label={t('Close')} onClick={onClose}><X size={19} /></button>
        </header>
        <div className="nx-dialog-body">{children}</div>
      </div>
    </dialog>
  );
}
