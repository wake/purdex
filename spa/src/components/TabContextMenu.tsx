import { useEffect, useLayoutEffect, useRef } from 'react'
import { useTeamDisplay } from './team/team-display'
import type { Tab } from '../types/tab'
import { getPrimaryPane } from '../lib/pane-tree'
import { useClickOutside } from '../hooks/useClickOutside'
import { useI18nStore } from '../stores/useI18nStore'
import { TITLE_BAR_HEIGHT } from './FloatingPanel'

const PADDING = 4

export type ContextMenuAction =
  | 'lock' | 'unlock' | 'pin' | 'unpin'
  | 'close' | 'closeOthers' | 'closeRight'
  | 'tearOff' | 'mergeTo' | 'mergeToTab'
  | 'rename'

interface Props {
  tab: Tab
  position: { x: number; y: number }
  onClose: () => void
  onAction: (action: ContextMenuAction, payload?: string) => void
  hasOtherUnlocked: boolean
  hasRightUnlocked: boolean
  targetTabs?: Tab[]
}

interface MenuItem {
  label: string
  action: ContextMenuAction
  show: boolean
  disabled?: boolean
  /** Why it is disabled (shown as the tooltip). */
  title?: string
  payload?: string
}

export function TabContextMenu({ tab, position, onClose, onAction, hasOtherUnlocked, hasRightUnlocked, targetTabs }: Props) {
  const t = useI18nStore((s) => s.t)
  const ref = useRef<HTMLDivElement>(null)

  // Viewport boundary correction — directly adjust DOM before paint (no state needed)
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
    // bar — many "merge to tab" rows): cap it and scroll. The same overflow test
    // as above, so only a menu that would otherwise be cut off gets a cap.
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

  // Spec R12: a tab in a team group cannot be pinned (the store refuses it too).
  const inTeam = useTeamDisplay()?.tabMark(tab.id) != null
  const primary = getPrimaryPane(tab.layout)
  const isSession = primary.content.kind === 'tmux-session'
  const isTerminated = isSession && !!(primary.content as { terminated?: string }).terminated

  const items: (MenuItem | 'separator')[] = [
    // Rename section (session only, non-terminated)
    ...(isSession && !isTerminated ? [{ label: t('tab.rename_session'), action: 'rename' as const, show: true }] : []),
    // Lock/Pin section
    { label: t('tab.lock'), action: 'lock' as const, show: !tab.locked },
    { label: t('tab.unlock'), action: 'unlock' as const, show: tab.locked },
    { label: t('tab.pin'), action: 'pin' as const, show: !tab.pinned, disabled: inTeam, title: inTeam ? t('tab.pin_team_disabled') : undefined },
    { label: t('tab.unpin'), action: 'unpin' as const, show: tab.pinned },
    // Tear-off section (the 'tearOff' handler checks window.electronAPI)
    'separator',
    { label: t('tab.move_new_window'), action: 'tearOff' as const, show: true, disabled: tab.locked },
    // MergeToTab section
    ...(targetTabs && targetTabs.length > 0 ? [
      'separator' as const,
      ...targetTabs.map((targetTab) => ({
        label: t('tab.merge_to_tab', { kind: getPrimaryPane(targetTab.layout).content.kind }),
        action: 'mergeToTab' as const,
        show: true,
        payload: targetTab.id,
      })),
    ] : []),
    'separator',
    // Close section
    { label: t('tab.close'), action: 'close' as const, show: true, disabled: tab.locked },
    { label: t('tab.close_others'), action: 'closeOthers' as const, show: hasOtherUnlocked },
    { label: t('tab.close_right'), action: 'closeRight' as const, show: hasRightUnlocked },
  ]

  const visibleItems = items.filter((item) => item === 'separator' || item.show)
  // Remove leading/trailing/consecutive separators
  const cleaned: typeof visibleItems = []
  for (const item of visibleItems) {
    if (item === 'separator') {
      if (cleaned.length > 0 && cleaned[cleaned.length - 1] !== 'separator') cleaned.push(item)
    } else {
      cleaned.push(item)
    }
  }
  if (cleaned[cleaned.length - 1] === 'separator') cleaned.pop()

  return (
    <div
      ref={ref}
      className="fixed z-50 bg-surface-elevated border border-border-default rounded-lg shadow-xl py-1 min-w-[200px] text-xs"
      // no-drag: wherever it lands, the title bar's drag region must not take its clicks.
      style={{ left: position.x, top: position.y, WebkitAppRegion: 'no-drag' } as React.CSSProperties}
    >
      {cleaned.map((item, i) => {
        if (item === 'separator') {
          return <div key={`sep-${i}`} className="border-t border-border-default my-1" />
        }
        return (
          <button
            key={item.payload ? `${item.action}-${item.payload}` : item.action}
            disabled={item.disabled}
            title={item.title}
            onClick={() => { onAction(item.action, item.payload); onClose() }}
            className={`w-full text-left px-3 py-1.5 transition-colors ${
              item.disabled ? 'opacity-40 cursor-not-allowed' : 'cursor-pointer hover:bg-surface-hover'
            }`}
          >
            {item.label}
          </button>
        )
      })}
    </div>
  )
}
