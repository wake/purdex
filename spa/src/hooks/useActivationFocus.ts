// spa/src/hooks/useActivationFocus.ts — the one rule for programmatic pane focus (shell cleanup spec §8.2).
//
// Programmatic focus happens only at ACTIVATION: `isActive` going false→true (a keep-alive tab shown again), or the
// first mount with `isActive` true. At that moment the pane focuses iff it is its tab's focus target
// (`PaneRendererProps.isFocusTarget`). A change of `isFocusTarget` while `isActive` stays true never focuses: that
// is a click inside a visible tab, and the click already put focus where the user wanted it.
//
// So the effect depends on `isActive` ONLY; `isFocusTarget`, `focusFn` and the rAF option are read through refs at
// activation time. With the rAF option the frame checks both again before focusing: a click on another pane between
// the activation and the frame moved the target there, and this pane must not take focus back (P5 review A1).
import { useEffect, useRef } from 'react'

export interface ActivationFocusOptions {
  /** Call `focusFn` in the next animation frame (for sites that must lay out first, e.g. a terminal fit). */
  raf?: boolean
}

export function useActivationFocus(
  isActive: boolean,
  isFocusTarget: boolean,
  focusFn: () => void,
  opts?: ActivationFocusOptions,
): void {
  // false, so the first mount with isActive true counts as an activation.
  const prevActiveRef = useRef(false)
  const isActiveRef = useRef(isActive)
  const isFocusTargetRef = useRef(isFocusTarget)
  const focusFnRef = useRef(focusFn)
  const rafRef = useRef(opts?.raf ?? false)
  // Declared before the activation effect, so it runs first in the same commit and the refs are current.
  useEffect(() => {
    isActiveRef.current = isActive
    isFocusTargetRef.current = isFocusTarget
    focusFnRef.current = focusFn
    rafRef.current = opts?.raf ?? false
  })

  useEffect(() => {
    const activated = isActive && !prevActiveRef.current
    prevActiveRef.current = isActive
    if (!activated || !isFocusTargetRef.current) return
    if (!rafRef.current) {
      focusFnRef.current()
      return
    }
    let fired = false
    const id = requestAnimationFrame(() => {
      fired = true
      if (isActiveRef.current && isFocusTargetRef.current) focusFnRef.current()
    })
    return () => {
      cancelAnimationFrame(id)
      // The activation never reached focus: hidden again or unmounted before the frame, or StrictMode's dev
      // unmount/remount. Undo it, so a re-run of this effect with isActive still true activates again.
      if (!fired) prevActiveRef.current = false
    }
  }, [isActive])
}
