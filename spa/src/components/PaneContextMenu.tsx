import { useEffect, useLayoutEffect, useRef } from 'react'
import { useClickOutside } from '../hooks/useClickOutside'
import { useI18nStore } from '../stores/useI18nStore'
import { TITLE_BAR_HEIGHT } from './FloatingPanel'

const PADDING = 4

export type PaneMenuAction = 'split-h' | 'split-v' | 'close' | 'detach' | 'hand-to-nex'

interface Props {
  position: { x: number; y: number }
  canDetach: boolean
  /**
   * Content-specific items the renderer decides on (e.g. "Hand to nex" for a
   * CC terminal pane). Rendered after a separator so the layout items above
   * stay the same on every pane kind.
   */
  extraItems?: MenuItem[]
  onClose: () => void
  onAction: (action: PaneMenuAction) => void
}

export interface MenuItem {
  label: string
  action: PaneMenuAction
}

export function PaneContextMenu({ position, canDetach, extraItems, onClose, onAction }: Props) {
  const t = useI18nStore((s) => s.t)
  const ref = useRef<HTMLDivElement>(null)

  // Viewport boundary correction — directly adjust DOM before paint (no state
  // needed). Mirrors TabContextMenu.
  useLayoutEffect(() => {
    const el = ref.current
    if (!el) return
    // Measure without the previous position's cap, which would otherwise be
    // read back as the menu's height.
    el.style.maxHeight = ''
    el.style.overflowY = ''
    const rect = el.getBoundingClientRect()
    let { x, y } = position
    if (x + rect.width > window.innerWidth) x = window.innerWidth - rect.width - PADDING
    if (y + rect.height > window.innerHeight) y = window.innerHeight - rect.height - PADDING
    if (x < 0) x = PADDING
    // Never inside the title bar's OS drag region (see `FloatingPanel`): a tall
    // menu moved up to fit would put its first items where a click drags the window.
    if (y < TITLE_BAR_HEIGHT) y = TITLE_BAR_HEIGHT
    // Now running off the bottom edge (taller than the window below the title
    // bar): cap it and scroll. The same overflow test as above, so only a menu
    // that would otherwise be cut off gets a cap.
    if (y + rect.height > window.innerHeight) {
      el.style.maxHeight = `${Math.max(0, window.innerHeight - y - PADDING)}px`
      el.style.overflowY = 'auto'
    }
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
    ...(extraItems && extraItems.length > 0 ? ['separator' as const, ...extraItems] : []),
  ]

  return (
    <div
      ref={ref}
      className="fixed z-50 bg-surface-elevated border border-border-default rounded-lg shadow-xl py-1 min-w-[180px] text-xs"
      // no-drag: wherever it lands, the title bar's drag region must not take its clicks.
      style={{ left: position.x, top: position.y, WebkitAppRegion: 'no-drag' } as React.CSSProperties}
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
