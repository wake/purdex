import { useEffect, useRef } from 'react'
import { PencilSimple, Palette, Smiley, Trash } from '@phosphor-icons/react'
import { useI18nStore } from '../../../stores/useI18nStore'

interface Props {
  position: { x: number; y: number }
  workspaceName: string
  onRename: () => void
  onChangeColor: () => void
  onChangeIcon: () => void
  onDelete: () => void
  onClose: () => void
}

export function WorkspaceContextMenu({ position, onRename, onChangeColor, onChangeIcon, onDelete, onClose }: Props) {
  const t = useI18nStore((s) => s.t)
  const menuRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    const handleKeyDown = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose()
    }
    document.addEventListener('keydown', handleKeyDown)
    return () => document.removeEventListener('keydown', handleKeyDown)
  }, [onClose])

  const menuItems = [
    { label: t('workspace.rename'), icon: PencilSimple, onClick: onRename },
    { label: t('workspace.change_color'), icon: Palette, onClick: onChangeColor },
    { label: t('workspace.change_icon'), icon: Smiley, onClick: onChangeIcon },
    { type: 'separator' as const },
    { label: t('workspace.delete'), icon: Trash, onClick: onDelete, danger: true },
  ]

  return (
    <>
      <div data-testid="context-menu-backdrop" className="fixed inset-0 z-40" onMouseDown={onClose} />
      <div ref={menuRef} className="fixed z-50 min-w-44 bg-surface-secondary border border-border-default rounded-lg shadow-xl py-1"
        style={{ left: position.x, top: position.y }}>
        {menuItems.map((item, i) => {
          if ('type' in item && item.type === 'separator') {
            return <div key={i} className="h-px bg-border-subtle my-1 mx-2" />
          }
          const Icon = item.icon!
          return (
            <button key={i} onClick={() => { item.onClick!(); onClose() }}
              className={`w-full flex items-center gap-2 px-3 py-1.5 text-xs cursor-pointer transition-colors ${
                item.danger ? 'text-red-400 hover:bg-red-500/10' : 'text-text-secondary hover:text-text-primary hover:bg-surface-hover'
              }`}>
              <Icon size={14} />
              {item.label}
            </button>
          )
        })}
      </div>
    </>
  )
}
