// spa/src/components/team/useWorkbookViewState.ts — the workbook view's own state (which tab, which finished groups are open, the
// scroll of each tab), kept in the module-level memo so it survives the tab being switched away and back (WA-2b-2; the
// tab-hosted rule: lib/workbook/view-memory.ts).
import { useCallback, useLayoutEffect, useRef, useState } from 'react'
import { patchViewMemo, readViewMemo, type WorkbookTab } from '../../lib/workbook/view-memory'

export interface WorkbookViewState {
  tab: WorkbookTab
  setTab: (tab: WorkbookTab) => void
  openGroups: string[]
  toggleGroup: (key: string) => void
  /** Callback ref for the scrolling box: its remembered scroll is restored when the box mounts or the tab changes, and remembered as it moves (`onScroll`). */
  bindBox: (el: HTMLDivElement | null) => void
  onScroll: () => void
  /** The element inside the box matching `selector` (call from an effect or handler, not during render). */
  find: (selector: string) => Element | null
}

/** `convKey` null: no conversation yet, nothing to remember (the frame is re-keyed when it appears). */
export function useWorkbookViewState(hostId: string, convKey: string | null): WorkbookViewState {
  const [tab, setTabState] = useState<WorkbookTab>(() => (convKey === null ? 'log' : readViewMemo(hostId, convKey).tab))
  const [openGroups, setOpenGroups] = useState<string[]>(() => (convKey === null ? [] : readViewMemo(hostId, convKey).openGroups))
  const scrollRef = useRef<HTMLDivElement | null>(null)

  const setTab = useCallback((next: WorkbookTab) => {
    setTabState(next)
    if (convKey !== null) patchViewMemo(hostId, convKey, { tab: next })
  }, [hostId, convKey])
  const toggleGroup = useCallback((key: string) => {
    setOpenGroups((cur) => {
      const next = cur.includes(key) ? cur.filter((k) => k !== key) : [...cur, key]
      if (convKey !== null) patchViewMemo(hostId, convKey, { openGroups: next })
      return next
    })
  }, [hostId, convKey])
  const onScroll = useCallback(() => {
    const el = scrollRef.current
    if (!el || convKey === null) return
    patchViewMemo(hostId, convKey, { scroll: { ...readViewMemo(hostId, convKey).scroll, [tab]: el.scrollTop } })
  }, [hostId, convKey, tab])
  useLayoutEffect(() => {
    if (scrollRef.current && convKey !== null) scrollRef.current.scrollTop = readViewMemo(hostId, convKey).scroll[tab]
  }, [hostId, convKey, tab])

  const bindBox = useCallback((el: HTMLDivElement | null) => { scrollRef.current = el }, [])
  const find = useCallback((selector: string) => scrollRef.current?.querySelector(selector) ?? null, [])
  return { tab, setTab, openGroups, toggleGroup, bindBox, onScroll, find }
}
