// spa/src/lib/conversations/send-now-keys.ts — the Deck input's send-now keys (message-stages D8, plan L6), as a pure decision like input-arrows.ts: Ctrl+Enter, and
// Ctrl+X then Ctrl+S inside CHORD_MS (the terminal's own chord). The component keeps the DOM (`preventDefault`) and the armed time.
import type { KeyFacts } from './input-arrows'

/** Ctrl+X arms for this long; Ctrl+S inside the window sends now. */
export const CHORD_MS = 1500

export type SendNowKeyAction = 'send_now' | 'arm' | 'none'

const MODIFIERS = new Set(['Control', 'Shift', 'Alt', 'Meta'])

/**
 * What a key press means for send now, and the armed time to keep. `arm` is Ctrl+X: the caller must NOT swallow it (its normal cut stays).
 * Any key but a bare modifier press ends an armed chord.
 */
export function sendNowKey(e: KeyFacts, armedAt: number | null, now: number): { action: SendNowKeyAction; armedAt: number | null } {
  if (MODIFIERS.has(e.key)) return { action: 'none', armedAt }
  const plainCtrl = e.ctrlKey && !e.metaKey && !e.altKey && !e.shiftKey
  const key = e.key.toLowerCase()
  if (plainCtrl && key === 'enter') return { action: 'send_now', armedAt: null }
  if (plainCtrl && key === 'x') return { action: 'arm', armedAt: now }
  if (plainCtrl && key === 's' && armedAt !== null && now - armedAt <= CHORD_MS) return { action: 'send_now', armedAt: null }
  return { action: 'none', armedAt: null }
}
