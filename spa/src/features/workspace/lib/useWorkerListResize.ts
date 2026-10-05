// spa/src/features/workspace/lib/useWorkerListResize.ts — the wide bar's worker list height (shell cleanup spec §4.2).
// Measures the split box, caps the rendered height so the workspace zone keeps WORKSPACE_ZONE_MIN, and runs the
// divider's draft-then-commit drag.
import { useEffect, useRef, useState, type RefObject } from 'react'
import { useLayoutStore, WORKER_LIST_MIN, WORKER_LIST_MAX } from '../../../stores/useLayoutStore'

/** The worker list never takes the workspace zone below this (spec §4.2). */
export const WORKSPACE_ZONE_MIN = 96
/** PaneSplitter's 'v' bar is `h-1`. */
export const WORKER_DIVIDER_HEIGHT = 4

export interface WorkerListResize {
  /** Goes on the split box: the element that holds exactly the workspace zone, the divider and the list. */
  splitBoxRef: RefObject<HTMLDivElement | null>
  /** The list height to render: the draft while dragging, else the stored height; capped either way. */
  height: number
  /** The divider's onResize. Dragging up (dy < 0) grows the list. */
  onResize: (dy: number) => void
  /** The divider's onResizeEnd: commits the draft to the store. */
  onResizeEnd: () => void
}

/**
 * `available` is the split box's height, measured while the list is open. The rendered height is
 * `min(draft ?? stored, available − WORKSPACE_ZONE_MIN − WORKER_DIVIDER_HEIGHT)`; a short window only caps it and never
 * writes the store. A drag starts from the rendered height and commits what is on screen. While the cap is below
 * WORKER_LIST_MIN the box is too short to resize: a drag starts no draft and its end writes nothing, and the list
 * renders at the cap until room comes back. Without a ResizeObserver report (e.g. jsdom) there is no cap.
 *
 * Unmount contract: closing the list or unmounting disconnects the observer and drops any draft; after that,
 * onResize and onResizeEnd do nothing, so nothing reaches the store.
 */
export function useWorkerListResize(open: boolean): WorkerListResize {
  const stored = useLayoutStore((s) => s.workerListHeight)
  const setStored = useLayoutStore((s) => s.setWorkerListHeight)

  const splitBoxRef = useRef<HTMLDivElement>(null)
  const [available, setAvailable] = useState<number | null>(null)
  const [draft, setDraft] = useState<number | null>(null)
  const draftRef = useRef<number | null>(null)
  // True only while the list is open and mounted; onResize starts no draft otherwise, and the effect's cleanup drops
  // the one in progress, so onResizeEnd has nothing to commit.
  const liveRef = useRef(false)

  useEffect(() => {
    if (!open) return
    liveRef.current = true
    const el = splitBoxRef.current
    const ro =
      el && typeof ResizeObserver !== 'undefined'
        ? new ResizeObserver(([entry]) => {
            if (entry) setAvailable(entry.contentRect.height)
          })
        : null
    if (el) ro?.observe(el)
    return () => {
      liveRef.current = false
      ro?.disconnect()
      draftRef.current = null
      setDraft(null)
    }
  }, [open])

  const cap = available === null ? null : Math.max(0, available - WORKSPACE_ZONE_MIN - WORKER_DIVIDER_HEIGHT)
  const uncapped = draft ?? stored
  const height = cap === null ? uncapped : Math.min(uncapped, cap)
  // Below WORKER_LIST_MIN the screen shows a height the store cannot hold, so a drag could never store what is seen.
  const resizable = cap === null || cap >= WORKER_LIST_MIN

  // Held inside what can be shown, so the divider tracks the pointer even while the cap applies.
  const onResize = (dy: number) => {
    if (!liveRef.current || !resizable) return
    const base = draftRef.current ?? height
    const upper = cap === null ? WORKER_LIST_MAX : Math.min(WORKER_LIST_MAX, cap)
    const next = Math.min(Math.max(base - dy, WORKER_LIST_MIN), upper)
    draftRef.current = next
    setDraft(next)
  }
  const onResizeEnd = () => {
    const committed = draftRef.current
    if (committed === null) return
    draftRef.current = null
    setDraft(null)
    // The box shrank below the minimum mid-drag: drop the draft rather than store a height the screen does not show.
    if (resizable) setStored(committed)
  }

  return { splitBoxRef, height, onResize, onResizeEnd }
}
