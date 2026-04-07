import { useState } from 'react'
import { useI18nStore } from '../../../stores/useI18nStore'

interface Props {
  currentName: string
  onConfirm: (newName: string) => void
  onCancel: () => void
}

export function WorkspaceRenameDialog({ currentName, onConfirm, onCancel }: Props) {
  const t = useI18nStore((s) => s.t)
  const [name, setName] = useState(currentName)

  const handleConfirm = () => {
    const trimmed = name.trim()
    if (!trimmed) return
    onConfirm(trimmed)
  }

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/50">
      <div className="bg-surface-secondary border border-border-default rounded-lg shadow-xl w-full max-w-sm mx-4 p-5">
        <h3 className="text-sm font-semibold text-text-primary mb-3">{t('workspace.rename')}</h3>
        <input type="text" value={name} onChange={(e) => setName(e.target.value)}
          onKeyDown={(e) => { if (e.key === 'Enter') handleConfirm(); if (e.key === 'Escape') onCancel() }}
          className="w-full bg-surface-primary border border-border-default rounded px-3 py-2 text-sm text-text-primary" autoFocus />
        <div className="flex justify-end gap-2 mt-4">
          <button onClick={onCancel} className="px-3 py-1.5 rounded text-xs bg-surface-tertiary text-text-secondary hover:text-text-primary cursor-pointer">
            {t('common.cancel')}
          </button>
          <button onClick={handleConfirm} className="px-3 py-1.5 rounded text-xs bg-accent text-white hover:bg-accent/80 cursor-pointer">
            {t('common.save')}
          </button>
        </div>
      </div>
    </div>
  )
}
