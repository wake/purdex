// spa/src/hooks/useActivationFocus.ts — the one rule for programmatic pane focus (shell cleanup spec §8.2).
//
// Programmatic focus happens only at ACTIVATION: `isActive` going false→true (a keep-alive tab shown again), or the
// first mount with `isActive` true. At that moment the pane focuses iff it is its tab's focus target
// (`PaneRendererProps.isFocusTarget`). A change of `isFocusTarget` while `isActive` stays true never focuses: that
// is a click inside a visible tab, and the click already put focus where the user wanted it.
//
// So the effect depends on `isActive` ONLY; `isFocusTarget`, `focusFn` and the rAF option are read through a ref at
// activation time. With the rAF option the frame checks both again before focusing: a click on another pane between
// the activation and the frame moved the target there, and this pane must not take focus back (P5 review A1).
//
// The one other trigger is an explicit request (`usePaneFocusStore.requestFocus`, a notification click; #1840 review
// A1): it focuses the requested pane also in a tab already on screen, where no activation happens. The hook knows its
// pane from `PaneIdentityContext` (set by `PaneLayoutRenderer`). A request is used once (`takeFocusRequest`): a
// re-render or a remount never serves it again, and an activation in the same commit takes it, so the pane focuses
// once, not twice. A request for a pane whose tab is hidden waits for that tab's activation.
import { createContext, useContext, useEffect, useRef, type RefObject } from 'react'
import { usePaneFocusStore } from '../stores/usePaneFocusStore'

export interface ActivationFocusOptions {
  /** Call `focusFn` in the next animation frame (for sites that must lay out first, e.g. a terminal fit). */
  raf?: boolean
}

/** Why `focusFn` runs: the tab was shown, or the pane was asked for explicitly (then it may take focus from a field). */
export type FocusCause = 'activation' | 'request'

/** The pane a renderer draws (`PaneLayoutRenderer` provides it); null outside a pane, where no request reaches. */
export const PaneIdentityContext = createContext<{ tabId: string; paneId: string } | null>(null)

interface Live {
  isActive: boolean
  isFocusTarget: boolean
  focusFn: (cause: FocusCause) => void
  raf: boolean
  requestNonce: number | null
}

/** Focus now, or in the next frame (checked again then). The cleanup cancels a frame not run yet, then calls `onCancelled`. */
function focusSoon(live: RefObject<Live>, cause: FocusCause, onCancelled: () => void): (() => void) | undefined {
  if (!live.current.raf) {
    if (live.current.isFocusTarget) live.current.focusFn(cause)
    return
  }
  let fired = false
  const id = requestAnimationFrame(() => {
    fired = true
    if (live.current.isActive && live.current.isFocusTarget) live.current.focusFn(cause)
  })
  return () => {
    cancelAnimationFrame(id)
    if (!fired) onCancelled()
  }
}

export function useActivationFocus(
  isActive: boolean,
  isFocusTarget: boolean,
  focusFn: (cause: FocusCause) => void,
  opts?: ActivationFocusOptions,
): void {
  const pane = useContext(PaneIdentityContext)
  // The current request's nonce when it is for this pane, else null — used up or not, so claiming it does not
  // re-render (which would cancel the frame the claim scheduled).
  const requestNonce = usePaneFocusStore((s) => {
    const r = s.focusRequest
    return pane && r && r.tabId === pane.tabId && r.paneId === pane.paneId ? r.nonce : null
  })
  // false, so the first mount with isActive true counts as an activation.
  const prevActiveRef = useRef(false)
  const live = useRef<Live>({ isActive, isFocusTarget, focusFn, raf: opts?.raf ?? false, requestNonce })
  // Declared before the other effects, so it runs first in the same commit and `live` is current.
  useEffect(() => {
    live.current = { isActive, isFocusTarget, focusFn, raf: opts?.raf ?? false, requestNonce }
  })

  useEffect(() => {
    const activated = isActive && !prevActiveRef.current
    prevActiveRef.current = isActive
    if (!activated) return
    // The activation serves a pending request for this pane: its focus is the request's, and there is no second one.
    const nonce = live.current.requestNonce
    const took = nonce !== null && usePaneFocusStore.getState().takeFocusRequest(nonce)
    if (!live.current.isFocusTarget) return
    // Cancelled before focus: hidden again or unmounted before the frame, or StrictMode's dev unmount/remount. Undo
    // it, so a re-run of this effect with isActive still true activates (and takes the request) again.
    return focusSoon(live, took ? 'request' : 'activation', () => {
      prevActiveRef.current = false
      if (took) usePaneFocusStore.getState().releaseFocusRequest(nonce)
    })
  }, [isActive])

  useEffect(() => {
    // A hidden tab leaves the request to its activation (above).
    if (requestNonce === null || !live.current.isActive) return
    if (!usePaneFocusStore.getState().takeFocusRequest(requestNonce)) return
    return focusSoon(live, 'request', () => usePaneFocusStore.getState().releaseFocusRequest(requestNonce))
  }, [requestNonce])
}
