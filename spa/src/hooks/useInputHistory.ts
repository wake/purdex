// spa/src/hooks/useInputHistory.ts — shell-style history for a reply box: ArrowUp at the very
// start of the box recalls the previous message, ArrowDown at the very end walks forward again,
// and walking past the newest returns to the unsent draft stashed on the first Up. Pure state
// (refs, no rendering): the box asks `shouldNavigate` first, then calls `up` / `down`, and
// puts whatever comes back into the box. `E` is one history entry, `D` the stashed draft.
import { useCallback, useLayoutEffect, useRef } from 'react'

export type HistoryDir = 'up' | 'down'

export type HistoryMove<E, D> =
  | { kind: 'entry'; entry: E }
  | { kind: 'draft'; draft: D }

export interface InputHistory<E, D> {
  /** Previous entry; the first call stashes the draft via `captureDraft`. Null when there is nothing older (or no entries). */
  up(captureDraft: () => D): HistoryMove<E, D> | null
  /** Next entry, or the stashed draft once past the newest; null when not navigating. */
  down(): HistoryMove<E, D> | null
  /** Back to "not navigating"; the stash is dropped. */
  reset(): void
  /** The box shows a recalled entry. */
  isNavigating(): boolean
}

/** `entries` run oldest → newest. The position is an index from the oldest, so entries appended meanwhile do not move it. */
export function useInputHistory<E, D>(entries: readonly E[]): InputHistory<E, D> {
  const entriesRef = useRef(entries)
  useLayoutEffect(() => { entriesRef.current = entries })
  const index = useRef<number | null>(null)
  const stash = useRef<{ draft: D } | null>(null)

  const up = useCallback((captureDraft: () => D): HistoryMove<E, D> | null => {
    const list = entriesRef.current
    if (list.length === 0) return null
    if (index.current === null) {
      stash.current = { draft: captureDraft() }
      index.current = list.length - 1
    } else if (index.current > 0) {
      index.current = Math.min(index.current, list.length - 1) - 1
    } else {
      return null
    }
    return { kind: 'entry', entry: list[index.current] }
  }, [])

  const down = useCallback((): HistoryMove<E, D> | null => {
    const list = entriesRef.current
    if (index.current === null) return null
    if (index.current < list.length - 1) {
      index.current += 1
      return { kind: 'entry', entry: list[index.current] }
    }
    const draft = stash.current
    index.current = null
    stash.current = null
    return draft ? { kind: 'draft', draft: draft.draft } : null
  }, [])

  const reset = useCallback(() => { index.current = null; stash.current = null }, [])
  const isNavigating = useCallback(() => index.current !== null, [])
  return { up, down, reset, isNavigating }
}

export interface CaretBox {
  value: string
  selectionStart: number
  selectionEnd: number
}

/**
 * Where the box last put the caret after a recall, with the text it showed: while that text is
 * untouched and the caret is still there, both arrows keep walking (Up, Up, Down must not need a
 * caret move in between). Any edit or caret move falls back to the strict rule.
 */
export interface RecallMark { text: string; pos: number }

/**
 * Whether an arrow press is a history step. Strict rule: no IME composition, no selection, and
 * the caret at position 0 for Up / at the very end for Down (the first or last line's edge is not
 * enough). While walking, the caret parked by the last recall counts too (see `RecallMark`).
 */
export function shouldNavigate(dir: HistoryDir, box: CaretBox, composing: boolean, mark: RecallMark | null): boolean {
  if (composing) return false
  if (box.selectionStart !== box.selectionEnd) return false
  const caret = box.selectionStart
  if (dir === 'up' ? caret === 0 : caret === box.value.length) return true
  return !!mark && mark.text === box.value && mark.pos === caret
}
