// spa/src/hooks/useListRetry.ts — the 重試 of a list's error line (#1627 C). The button stays mounted while its retry
// runs (disabled, aria-busy, like StopSyncControl's), so keyboard focus is not dropped to <body> with it. When the retry
// settles, focus goes to the list (success) or back to the button (failure) — only when the button had focus at the
// press, and only while focus has not gone anywhere else since (never taken from elsewhere). A focused button that turns
// disabled can lose focus to <body> (ConfirmDialog's note), so a failure focuses it again explicitly.
import { useCallback, useEffect, useRef, useState } from 'react'

export interface ListRetry {
  /** The retry runs: keep the error line, its text and its button (disabled, aria-busy). */
  busy: boolean
  /** The error line's text: while busy, the error the press answered (a list hook may clear its own on refetch). */
  error: string | null
  /** The button's onClick. */
  onRetry: () => void
  /** The button's `ref`. */
  bindButton: (el: HTMLButtonElement | null) => void
  /** The `ref` of the list (with `tabIndex={-1}`), or of what stands in its place when it is empty: a success focuses it. */
  bindList: (el: HTMLElement | null) => void
}

/**
 * `phase` is the list hook's: anything but `'loading'` after the press is the retry settled, `'ready'` a success.
 * Destructure the result at the call site: the React Compiler lint takes an object one of whose members reaches a `ref`
 * prop for a ref, and then flags every other member read during render.
 */
export function useListRetry(phase: string, error: string | null, refetch: () => void): ListRetry {
  const button = useRef<HTMLButtonElement | null>(null)
  const list = useRef<HTMLElement | null>(null)
  const bindButton = useCallback((el: HTMLButtonElement | null) => { button.current = el }, [])
  const bindList = useCallback((el: HTMLElement | null) => { list.current = el }, [])
  // The press until its retry settles: the error it answered, and whether the button had focus then.
  const [press, setPress] = useState<{ error: string | null; focused: boolean } | null>(null)
  // One per settled press that had focus (a fresh object each time, so the effect runs once for it).
  const [settled, setSettled] = useState<{ ok: boolean } | null>(null)
  // Render-time (react.dev "adjusting some state when a prop changes"): the line never draws busy past the settle,
  // also when the refetch started nothing and the phase never left 'error'.
  if (press !== null && phase !== 'loading') {
    setPress(null)
    if (press.focused) setSettled({ ok: phase === 'ready' })
  }

  useEffect(() => {
    if (settled === null) return
    const active = document.activeElement
    if (active !== null && active !== document.body && active !== button.current) return
    ;(settled.ok ? list.current : button.current)?.focus()
  }, [settled])

  const onRetry = () => {
    setPress({ error, focused: document.activeElement === button.current })
    refetch()
  }
  return { busy: press !== null, error: press ? press.error : error, onRetry, bindButton, bindList }
}
