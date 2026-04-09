import { useEffect, useLayoutEffect, useRef } from 'react'
import type { Tab } from '../types/tab'
import { getPrimaryPane } from '../lib/pane-tree'
import { useClickOutside } from '../hooks/useClickOutside'
import { useI18nStore } from '../stores/useI18nStore'
import { getPlatformCapabilities } from '../lib/platform'

export type ContextMenuAction =
  | 'viewMode-terminal' | 'viewMode-stream'
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
  payload?: string
}

export function TabContextMenu({ tab, position, onClose, onAction, hasOtherUnlocked, hasRightUnlocked, targetTabs }: Props) {
  const t = useI18nStore((s) => s.t)
  const caps = getPlatformCapabilities()
  const ref = useRef<HTMLDivElement>(null)

  // Viewport boundary correction — directly adjust DOM before paint (no state needed)
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

  const primary = getPrimaryPane(tab.layout)
  const isSession = primary.content.kind === 'tmux-session'
  const currentMode = isSession ? (primary.content as { kind: 'tmux-session'; mode: string }).mode : undefined
  const isTerminated = isSession && !!(primary.content as { terminated?: string }).terminated

  const items: (MenuItem | 'separator')[] = [
    // ViewMode section
    ...(isSession && currentMode !== 'terminal' ? [{ label: t('tab.switch_terminal'), action: 'viewMode-terminal' as const, show: true }] : []),
    ...(isSession && currentMode !== 'stream' ? [{ label: t('tab.switch_stream'), action: 'viewMode-stream' as const, show: true }] : []),
    ...(isSession ? ['separator' as const] : []),
    // Rename section (session only, non-terminated)
    ...(isSession && !isTerminated ? [{ label: t('tab.rename_session'), action: 'rename' as const, show: true }] : []),
    // Lock/Pin section
    { label: t('tab.lock'), action: 'lock' as const, show: !tab.locked },
    { label: t('tab.unlock'), action: 'unlock' as const, show: tab.locked },
    { label: t('tab.pin'), action: 'pin' as const, show: !tab.pinned },
    { label: t('tab.unpin'), action: 'unpin' as const, show: tab.pinned },
    // Tear-off section (Electron only)
    ...(caps.canTearOffTab ? [
      'separator' as const,
      { label: t('tab.move_new_window'), action: 'tearOff' as const, show: true, disabled: tab.locked },
    ] : []),
    // MergeToTab section
    ...(targetTabs && targetTabs.length > 0 ? [
      'separator' as const,
      ...targetTabs.map((targetTab) => ({
        label: `加入 ${getPrimaryPane(targetTab.layout).content.kind} tab 成為 pane`,
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
      style={{ left: position.x, top: position.y }}
    >
      {cleaned.map((item, i) => {
        if (item === 'separator') {
          return <div key={`sep-${i}`} className="border-t border-border-default my-1" />
        }
        return (
          <button
            key={item.payload ? `${item.action}-${item.payload}` : item.action}
            disabled={item.disabled}
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
