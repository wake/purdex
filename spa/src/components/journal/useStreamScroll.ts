// spa/src/components/journal/useStreamScroll.ts — the stream's scrolling (J-2c, spec §11): it opens at the bottom and keeps
// following new entries while the reader is there; elsewhere a pill counts them; scrolling to the top loads the previous month
// and keeps the reading position. `scroll` and `pinnedToBottom` are the unit's view memo (Tab-hosted rule: the stream unmounts on
// a tab switch and opens exactly where it was); the pill's count is transient.
import { useCallback, useEffect, useLayoutEffect, useRef, useState } from 'react'
import { patchViewMemo, readViewMemo } from '../../lib/journal/view-memory'
import type { JournalEntry } from '../../lib/journal/types'

/** Within this many px of the end counts as "at the bottom" / "at the top". */
export const NEAR_BOTTOM = 24
export const NEAR_TOP = 8
const SAVE_MS = 150
/** Loading the previous month adds no row when the filter hides all of it: ask again at most this many times in a row. */
export const MAX_CHAIN = 5

interface Opts {
  host: string | null
  unitKey: string | null
  /** Names the unit the list belongs to (`host\0unitKey`): a different key is a different list, not new events. */
  streamKey?: string
  /** What the stream shows (filtered), oldest first. */
  shown: readonly JournalEntry[]
  filter: string
  more: boolean
  loadOlder: () => Promise<unknown>
}

/** How many entries came in at the end: those after the old last one (else the growth). */
function appended(old: readonly JournalEntry[], now: readonly JournalEntry[]): number {
  const lastOld = old[old.length - 1]
  const at = lastOld ? now.findIndex((e) => e.id === lastOld.id) : -1
  return at >= 0 ? now.length - 1 - at : Math.max(1, now.length - old.length)
}

/** Where the row of that entry starts inside the scrolling element, or null when it is not drawn. */
function rowTop(el: HTMLElement, id: string): number | null {
  for (const row of el.querySelectorAll<HTMLElement>('[data-entry-id]')) if (row.getAttribute('data-entry-id') === id) return row.offsetTop
  return null
}

export function useStreamScroll({ host, unitKey, streamKey = `${host}\0${unitKey}`, shown, filter, more, loadOlder }: Opts) {
  const ref = useRef<HTMLDivElement>(null)
  const [below, setBelow] = useState(0)
  const prev = useRef<readonly JournalEntry[]>(shown)
  const prevFilter = useRef(filter)
  const prevKey = useRef(streamKey)
  const anchor = useRef<{ height: number; top: number; row: { id: string; offset: number } | null } | null>(null)
  const loading = useRef(false)
  const chain = useRef(0)
  const failed = useRef(false)
  const handledLoad = useRef(0)
  const [loaded, setLoaded] = useState(0)
  const lastTop = useRef(0)
  const lastAtBottom = useRef(true)
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null)

  const save = useCallback(() => {
    if (host !== null && unitKey !== null) patchViewMemo(host, unitKey, { scroll: lastTop.current, pinnedToBottom: lastAtBottom.current })
  }, [host, unitKey])

  // Opening: the bottom when the reader left it there (or never scrolled), else where they were.
  useLayoutEffect(() => {
    const el = ref.current
    if (!el || host === null || unitKey === null) return
    const m = readViewMemo(host, unitKey)
    el.scrollTop = m.pinnedToBottom ? el.scrollHeight : m.scroll
    lastTop.current = el.scrollTop
    lastAtBottom.current = m.pinnedToBottom
    return () => { if (timer.current) { clearTimeout(timer.current); timer.current = null } save() }
  }, [host, unitKey, save])

  // A different filter shows a different list: start at its bottom.
  useLayoutEffect(() => {
    if (prevFilter.current === filter) return
    prevFilter.current = filter
    const el = ref.current
    anchor.current = null
    if (!el) return
    el.scrollTop = el.scrollHeight
    lastTop.current = el.scrollTop
    lastAtBottom.current = true
    setBelow(0)
    save()
  }, [filter, save])

  // The list changed: older months prepended keep the reading position; new entries at the end follow or count.
  useLayoutEffect(() => {
    const old = prev.current
    prev.current = shown
    const el = ref.current
    if (prevKey.current !== streamKey) {
      // Another unit's list (the opening effect above already put the view where that unit was left): none of it is new.
      prevKey.current = streamKey
      anchor.current = null
      setBelow(0)
      return
    }
    if (!el || old === shown) return
    // Older rows came in above: keep the row that was at the top where it was. Rows that came in below at the same time
    // (a new event) do not move it, so the shift is measured at that row, not as the growth of the whole list.
    if (anchor.current && shown[0]?.id !== old[0]?.id) {
      const a = anchor.current
      const now = a.row ? rowTop(el, a.row.id) : null
      el.scrollTop = a.top + (a.row && now !== null ? now - a.row.offset : el.scrollHeight - a.height)
      lastTop.current = el.scrollTop
      anchor.current = null
    }
    // New entries at the end follow the reader at the bottom, else they are counted for the pill.
    if (shown[shown.length - 1]?.id === old[old.length - 1]?.id) return
    const pinned = host !== null && unitKey !== null ? readViewMemo(host, unitKey).pinnedToBottom : true
    if (pinned) { el.scrollTop = el.scrollHeight; lastTop.current = el.scrollTop }
    else setBelow((b) => b + appended(old, shown))
  }, [shown, host, unitKey, streamKey])

  const run = useCallback(() => {
    const el = ref.current
    if (!el || loading.current) return
    loading.current = true
    chain.current++
    const first = prev.current[0]
    const offset = first ? rowTop(el, first.id) : null
    anchor.current = { height: el.scrollHeight, top: el.scrollTop, row: first && offset !== null ? { id: first.id, offset } : null }
    // A failed read leaves things as they were: no automatic second ask.
    void loadOlder().then(() => true, () => false).then((ok) => { loading.current = false; failed.current = !ok; setLoaded((n) => n + 1) })
  }, [loadOlder])

  /** The reader (scrolling to the top, or the button) asks for the previous month: a new run of automatic asks begins. */
  const older = useCallback(() => {
    if (loading.current) return
    chain.current = 0
    run()
  }, [run])

  // After a month arrived (declared after the list effect, so a prepended list has already been laid out): drop the anchor; if the
  // month added no row under this filter the reader is still at the top and no scroll event will come, so ask for the next one.
  useLayoutEffect(() => {
    if (loaded === handledLoad.current) return
    handledLoad.current = loaded
    anchor.current = null
    const el = ref.current
    if (el && more && !failed.current && chain.current < MAX_CHAIN && el.scrollTop <= NEAR_TOP) run()
  }, [loaded, more, run])

  const onScroll = useCallback(() => {
    const el = ref.current
    if (!el) return
    lastTop.current = el.scrollTop
    const atBottom = el.scrollHeight - el.scrollTop - el.clientHeight <= NEAR_BOTTOM
    if (atBottom !== lastAtBottom.current) { lastAtBottom.current = atBottom; save() }
    else if (!timer.current) timer.current = setTimeout(() => { timer.current = null; save() }, SAVE_MS)
    if (atBottom) setBelow(0)
    if (more && el.scrollTop <= NEAR_TOP) older()
  }, [more, older, save])

  const toBottom = useCallback(() => {
    const el = ref.current
    if (!el) return
    el.scrollTop = el.scrollHeight
    lastTop.current = el.scrollTop
    lastAtBottom.current = true
    setBelow(0)
    save()
  }, [save])

  useEffect(() => () => { if (timer.current) { clearTimeout(timer.current); timer.current = null } }, [])

  return { ref, below, onScroll, toBottom, older }
}
