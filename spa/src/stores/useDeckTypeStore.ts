import { create } from 'zustand'
import { persist } from 'zustand/middleware'
import { purdexStorage, STORAGE_KEYS } from '../lib/storage'

/**
 * The reader's type size and line height for the deck and the chat (c9d9c7-60). Device-local like the editor's font size (a small laptop and a
 * big monitor want different numbers): not synced. `null` = the built-in look, which is what a fresh install has, so an untouched setting
 * changes nothing on screen; the controls show the built-in numbers as their position and "reset" goes back to `null`.
 */
export const DECK_FONT_MIN = 11
export const DECK_FONT_MAX = 18
export const DECK_FONT_DEFAULT = 13 // what the agent text types at (and the input, same size since #2579)
export const DECK_LINE_MIN = 1.3
export const DECK_LINE_MAX = 2.0
export const DECK_LINE_STEP = 0.05
export const DECK_LINE_DEFAULT = 1.65

export const clampDeckFont = (v: number): number => Math.min(DECK_FONT_MAX, Math.max(DECK_FONT_MIN, Math.round(v)))
export const clampDeckLine = (v: number): number => {
  const stepped = Math.round(v / DECK_LINE_STEP) * DECK_LINE_STEP
  return Math.min(DECK_LINE_MAX, Math.max(DECK_LINE_MIN, Math.round(stepped * 100) / 100))
}

export interface DeckTypeState {
  /** px, or null for the built-in size. */
  fontSize: number | null
  /** unitless, or null for the built-in 1.65. */
  lineHeight: number | null
  setFontSize: (v: number) => void
  setLineHeight: (v: number) => void
  resetFontSize: () => void
  resetLineHeight: () => void
}

/** A corrupted payload (hand edit, another version) falls back to the built-in look, never to a broken size. */
function sanitize(raw: unknown): { fontSize: number | null; lineHeight: number | null } {
  const src = raw !== null && typeof raw === 'object' ? (raw as Record<string, unknown>) : {}
  const font = src.fontSize
  const line = src.lineHeight
  return {
    fontSize: typeof font === 'number' && Number.isFinite(font) ? clampDeckFont(font) : null,
    lineHeight: typeof line === 'number' && Number.isFinite(line) ? clampDeckLine(line) : null,
  }
}

export const useDeckTypeStore = create<DeckTypeState>()(
  persist(
    (set) => ({
      fontSize: null,
      lineHeight: null,
      setFontSize: (v) => { if (Number.isFinite(v)) set({ fontSize: clampDeckFont(v) }) },
      setLineHeight: (v) => { if (Number.isFinite(v)) set({ lineHeight: clampDeckLine(v) }) },
      resetFontSize: () => set({ fontSize: null }),
      resetLineHeight: () => set({ lineHeight: null }),
    }),
    {
      name: STORAGE_KEYS.DECK_TYPE,
      storage: purdexStorage,
      version: 1,
      partialize: (s) => ({ fontSize: s.fontSize, lineHeight: s.lineHeight }),
      merge: (persisted, current) => ({ ...current, ...sanitize(persisted) }),
    },
  ),
)
