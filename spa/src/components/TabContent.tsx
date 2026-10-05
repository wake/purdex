import { useLayoutEffect, useRef } from 'react'
import { PaneLayoutRenderer } from './PaneLayoutRenderer'
import { useTabAlivePool } from '../hooks/useTabAlivePool'
import { isLightTab } from '../lib/pane-weight'
import { useI18nStore } from '../stores/useI18nStore'
import type { Tab } from '../types/tab'

interface Props {
  activeTab: Tab | null
  allTabs: Tab[]
}

export function TabContent({ activeTab, allTabs }: Props) {
  const t = useI18nStore((s) => s.t)
  const { aliveIds, poolVersion } = useTabAlivePool(
    activeTab?.id ?? null,
    allTabs.map((t) => ({ id: t.id, pinned: t.pinned, light: isLightTab(t.layout) })),
  )

  const tabMap = new Map(allTabs.map((t) => [t.id, t]))
  const hasAliveTab = aliveIds.some((id) => tabMap.has(id))

  if (!activeTab && !hasAliveTab) {
    return (
      <div className="flex-1 flex items-center justify-center text-text-secondary text-sm">
        {t('tab.empty_state')}
      </div>
    )
  }

  return (
    <div className="flex-1 relative overflow-hidden">
      {aliveIds.map((id) => {
        const tab = tabMap.get(id)
        if (!tab) return null
        return <TabSlot key={`${id}-${poolVersion}`} tab={tab} isActive={id === activeTab?.id} />
      })}
    </div>
  )
}

/**
 * One kept-alive tab, hidden in place while inactive (`visibility: hidden` + `inert`, same box, so a terminal keeps
 * its layout).
 *
 * Shell polish spec §4: when the tab goes from active to inactive, it lets go of the focus and of the text selection
 * if they are inside it. Hiding alone is not enough in WebKit: `activeElement` then reports body, yet key presses still
 * land in the hidden field, because WebKit routes typing by the selection (measured with `playwright cli`; Chromium
 * moves focus to body and is safe). Blurring and clearing the selection stops it in both engines.
 *
 * Only what is inside this tab is touched, so the newly active tab's own activation focus (`useActivationFocus`, which
 * runs later — in a passive effect or the next frame) and anything outside the tabs keep theirs.
 */
function TabSlot({ tab, isActive }: { tab: Tab; isActive: boolean }) {
  const slotRef = useRef<HTMLDivElement>(null)
  const wasActiveRef = useRef(isActive)
  // A layout effect: it runs in the same task as the commit that hides the tab, before any passive effect or frame,
  // and both engines still report the hidden field as `activeElement` at that point (measured).
  useLayoutEffect(() => {
    const wasActive = wasActiveRef.current
    wasActiveRef.current = isActive
    if (wasActive && !isActive && slotRef.current) letGoOfFocusWithin(slotRef.current)
  }, [isActive])

  return (
    <div
      ref={slotRef}
      className="absolute"
      style={{ inset: 0, visibility: isActive ? 'visible' : 'hidden' }}
      inert={!isActive || undefined}
    >
      <PaneLayoutRenderer layout={tab.layout} tabId={tab.id} isActive={isActive} />
    </div>
  )
}

/** Blurs the focused element and clears the selection, each only if it is inside `container`. */
function letGoOfFocusWithin(container: HTMLElement): void {
  const focused = document.activeElement
  // HTML or SVG: both have blur().
  if (focused && container.contains(focused) && 'blur' in focused) (focused as HTMLElement).blur()
  const sel = document.getSelection()
  if (sel && sel.rangeCount > 0 && (container.contains(sel.anchorNode) || container.contains(sel.focusNode))) {
    sel.removeAllRanges()
  }
}
