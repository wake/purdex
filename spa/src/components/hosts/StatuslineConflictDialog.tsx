import { useEffect } from 'react'
import { useI18nStore } from '../../stores/useI18nStore'

interface Props {
  existingCommand: string
  onWrap: () => void
  onCancel: () => void
}

export function StatuslineConflictDialog({ existingCommand, onWrap, onCancel }: Props) {
  const t = useI18nStore((s) => s.t)

  // Escape to close
  useEffect(() => {
    const handleKeyDown = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onCancel()
    }
    document.addEventListener('keydown', handleKeyDown)
    return () => document.removeEventListener('keydown', handleKeyDown)
  }, [onCancel])

  return (
    <div
      className="fixed inset-0 bg-black/50 flex items-center justify-center z-50"
      role="dialog"
      aria-modal="true"
      aria-labelledby="statusline-conflict-title"
      onClick={onCancel}
    >
      <div
        className="bg-surface-elevated border border-border-default rounded-lg p-6 max-w-lg w-full mx-4 shadow-xl"
        onClick={(e) => e.stopPropagation()}
      >
        <h3 id="statusline-conflict-title" className="text-base font-semibold mb-3">
          {t('hosts.extensions.conflict_title')}
        </h3>
        <p className="text-sm text-text-secondary mb-2">{t('hosts.extensions.conflict_existing_label')}</p>
        <code className="block bg-surface-secondary border border-border-subtle rounded px-2 py-1.5 text-xs font-mono mb-4 break-all">
          {existingCommand}
        </code>
        <p className="text-xs text-text-muted mb-5">{t('hosts.extensions.conflict_wrap_explainer')}</p>
        <div className="flex justify-end gap-2">
          <button
            onClick={onCancel}
            className="px-3 py-1.5 rounded text-xs bg-surface-secondary hover:bg-surface-tertiary text-text-secondary cursor-pointer"
          >
            {t('hosts.extensions.cancel')}
          </button>
          <button
            onClick={onWrap}
            className="px-3 py-1.5 rounded text-xs bg-accent text-white hover:bg-accent-hover cursor-pointer"
          >
            {t('hosts.extensions.wrap')}
          </button>
        </div>
      </div>
    </div>
  )
}
