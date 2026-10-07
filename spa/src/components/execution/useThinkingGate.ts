// spa/src/components/execution/useThinkingGate.ts — hysteresis for the main
// transcript's thinking dots. While background tasks / subagents exist, each
// task notification starts and ends a short main turn, so the raw signal
// flaps every few seconds. The dots show only after the signal has held for
// SHOW_AFTER_MS and stay HIDE_AFTER_MS after it drops. Not `gated` → raw,
// immediately (no tasks, no flap to hide).
import { useEffect, useState } from 'react'

export const SHOW_AFTER_MS = 1500
export const HIDE_AFTER_MS = 1000

export function useThinkingGate(raw: boolean, gated: boolean): boolean {
  const [shown, setShown] = useState(false)
  useEffect(() => {
    if (!gated || raw === shown) return
    const id = setTimeout(() => setShown(raw), raw ? SHOW_AFTER_MS : HIDE_AFTER_MS)
    return () => clearTimeout(id)
  }, [raw, gated, shown])
  return gated ? shown : raw
}
