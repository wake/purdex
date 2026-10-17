// spa/src/stores/useJournalUiStore.ts — how the person arranged the journal cards on THIS device (J-2b T-2b.3, spec §11).
// Persisted (purdex-journal-ui), device-local. Two things only: the last used widths (the default for a unit that has none of
// its own yet) and, per team key, whether the team card is open — the team card on a journal.v1 host is open or closed and
// nothing else, so this never touches `useTeamUiStore` (older hosts keep their four modes). Everything else per unit (which
// cards are open, widths, filter, scroll, detail) is lib/journal/view-memory.ts.
import { create } from 'zustand'
import { persist } from 'zustand/middleware'
import { purdexStorage, STORAGE_KEYS } from '../lib/storage'

export type CardKey = 'journal' | 'roadmap' | 'tasks' | 'team'
export const CARD_KEYS: readonly CardKey[] = ['team', 'journal', 'roadmap', 'tasks']
export const CARD_MIN = 320
export const CARD_MAX = 720
export const CARD_DEFAULT: Record<CardKey, number> = { team: 360, journal: 420, roadmap: 360, tasks: 340 }
export const clampCard = (w: number): number => Math.max(CARD_MIN, Math.min(CARD_MAX, Math.round(w)))

interface JournalUiState {
  /** The last width the person set per card; absent = CARD_DEFAULT. */
  lastWidths: Partial<Record<CardKey, number>>
  /** Per team key: the team card is open. */
  teamOpen: Record<string, boolean>
  setLastWidth: (card: CardKey, width: number) => void
  setTeamOpen: (teamKey: string, open: boolean) => void
}

const isRecord = (v: unknown): v is Record<string, unknown> => typeof v === 'object' && v !== null && !Array.isArray(v)

export const useJournalUiStore = create<JournalUiState>()(
  persist(
    (set) => ({
      lastWidths: {},
      teamOpen: {},
      setLastWidth: (card, width) => set((s) => (Number.isFinite(width) ? { lastWidths: { ...s.lastWidths, [card]: clampCard(width) } } : s)),
      setTeamOpen: (teamKey, open) => set((s) => {
        if (open === (s.teamOpen[teamKey] === true)) return s
        const { [teamKey]: _gone, ...rest } = s.teamOpen
        return { teamOpen: open ? { ...rest, [teamKey]: true } : rest }
      }),
    }),
    {
      name: STORAGE_KEYS.JOURNAL_UI,
      storage: purdexStorage,
      version: 1,
      partialize: (s) => ({ lastWidths: s.lastWidths, teamOpen: s.teamOpen }),
      // A hand-edited or damaged save must not crash the shell: keep only well-formed entries.
      merge: (persisted, current) => {
        const p = isRecord(persisted) ? persisted : {}
        const lastWidths: Partial<Record<CardKey, number>> = {}
        if (isRecord(p.lastWidths)) for (const k of CARD_KEYS) { const v = p.lastWidths[k]; if (typeof v === 'number' && Number.isFinite(v)) lastWidths[k] = clampCard(v) }
        const teamOpen: Record<string, boolean> = {}
        if (isRecord(p.teamOpen)) for (const [k, v] of Object.entries(p.teamOpen)) if (v === true && k !== '__proto__') teamOpen[k] = true
        return { ...current, lastWidths, teamOpen }
      },
    },
  ),
)

/** The width a card opens at: the unit's own, else the last the person used, else the default. */
export const cardWidth = (card: CardKey, own: number | null | undefined, last: Partial<Record<CardKey, number>>): number => own ?? last[card] ?? CARD_DEFAULT[card]
