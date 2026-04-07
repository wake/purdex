import { useEffect } from 'react'
import { GearSix } from '@phosphor-icons/react'
import { useI18nStore } from '../../../stores/useI18nStore'

interface Props {
  position: { x: number; y: number }
  onSettings: () => void
  onClose: () => void
}

export function WorkspaceContextMenu({ position, onSettings, onClose }: Props) {
  const t = useI18nStore((s) => s.t)
  useEffect(() => {
    const handleKeyDown = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose()
    }
    document.addEventListener('keydown', handleKeyDown)
    return () => document.removeEventListener('keydown', handleKeyDown)
  }, [onClose])

  return (
    <>
      <div data-testid="context-menu-backdrop" className="fixed inset-0 z-40" onMouseDown={onClose} />
      <div className="fixed z-50 min-w-44 bg-surface-secondary border border-border-default rounded-lg shadow-xl py-1"
        style={{ left: position.x, top: position.y }}>
        <button onClick={() => { onSettings(); onClose() }}
          className="w-full flex items-center gap-2 px-3 py-1.5 text-xs text-text-secondary hover:text-text-primary hover:bg-surface-hover cursor-pointer transition-colors">
          <GearSix size={14} />
          {t('nav.settings') ?? 'Settings'}
        </button>
      </div>
    </>
  )
}
