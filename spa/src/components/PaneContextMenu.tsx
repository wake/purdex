import { useEffect, useLayoutEffect, useRef } from 'react'
import { useClickOutside } from '../hooks/useClickOutside'
import { useI18nStore } from '../stores/useI18nStore'

export type PaneMenuAction = 'split-h' | 'split-v' | 'close' | 'detach'

interface Props {
  position: { x: number; y: number }
  canDetach: boolean
  onClose: () => void
  onAction: (action: PaneMenuAction) => void
}

interface MenuItem {
  label: string
  action: PaneMenuAction
}

export function PaneContextMenu({ position, canDetach, onClose, onAction }: Props) {
  const t = useI18nStore((s) => s.t)
  const ref = useRef<HTMLDivElement>(null)

  // Viewport boundary correction — directly adjust DOM before paint (no state
  // needed). Mirrors TabContextMenu.
  useLayoutEffect(() => {
    const el = ref.current
    if (!el) return
    const rect = el.getBoundingClientRect()
    let { x, y } = position
    if (x + rect.width > window.innerWidth) x = window.innerWidth - rect.width - 4
    if (y + rect.height > window.innerHeight) y = window.innerHeight - rect.height - 4
    if (x < 0) x = 4
    if (y < 0) y = 4
    el.style.left = `${x}px`
    el.style.top = `${y}px`
  }, [position])

  useClickOutside(ref, onClose)

  useEffect(() => {
    const escHandler = (e: KeyboardEvent) => { if (e.key === 'Escape') onClose() }
    document.addEventListener('keydown', escHandler)
    return () => document.removeEventListener('keydown', escHandler)
  }, [onClose])

  const items: (MenuItem | 'separator')[] = [
    { label: t('pane.split_horizontal'), action: 'split-h' },
    { label: t('pane.split_vertical'), action: 'split-v' },
    // Close / Detach only make sense when the pane lives inside a split
    // (single-pane detach returns null, single-pane close is meaningless).
    ...(canDetach
      ? [
          'separator' as const,
          { label: t('pane.close'), action: 'close' as const },
          { label: t('pane.detach'), action: 'detach' as const },
        ]
      : []),
  ]

  return (
    <div
      ref={ref}
      className="fixed z-50 bg-surface-elevated border border-border-default rounded-lg shadow-xl py-1 min-w-[180px] text-xs"
      style={{ left: position.x, top: position.y }}
    >
      {items.map((item, i) => {
        if (item === 'separator') {
          return <div key={`sep-${i}`} className="border-t border-border-default my-1" />
        }
        return (
          <button
            key={item.action}
            onClick={() => { onAction(item.action); onClose() }}
            className="w-full text-left px-3 py-1.5 transition-colors cursor-pointer hover:bg-surface-hover"
          >
            {item.label}
          </button>
        )
      })}
    </div>
  )
}
