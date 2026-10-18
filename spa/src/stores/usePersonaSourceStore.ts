// spa/src/stores/usePersonaSourceStore.ts — which host holds the personas' source copy (name pool v2 spec §4.3, 「正本主機」).
// Every daemon keeps its own copy; the App edits the source's and copies it to the others. The choice is per device (a
// laptop and a desktop may reach different hosts), so it is device-local like the deck's type size: not synced, not in
// Profile. `null` = the default, the App's first host, which is what a fresh install has.
import { create } from 'zustand'
import { persist } from 'zustand/middleware'
import { purdexStorage, STORAGE_KEYS } from '../lib/storage'

interface PersonaSourceState {
  /** The chosen host's local id, or `null` for the default. */
  hostId: string | null
  setSource: (hostId: string | null) => void
}

export const usePersonaSourceStore = create<PersonaSourceState>()(
  persist(
    (set) => ({
      hostId: null,
      setSource: (hostId) => set({ hostId }),
    }),
    {
      name: STORAGE_KEYS.PERSONA_SOURCE,
      storage: purdexStorage,
      version: 1,
      partialize: (state) => ({ hostId: state.hostId }),
      // A corrupted payload (hand edit, another version) means "default", never a broken id.
      merge: (persisted, current) => {
        const hostId = (persisted as { hostId?: unknown } | null)?.hostId
        return { ...current, hostId: typeof hostId === 'string' && hostId !== '' ? hostId : null }
      },
    },
  ),
)

/**
 * The host whose copy is the source: the chosen one while this device still has it, else the first host of the App's
 * list that exists (the App's main host by default), else `null`.
 */
export function resolveSourceHostId(
  chosen: string | null,
  hostOrder: readonly string[],
  hosts: Readonly<Record<string, unknown>>,
): string | null {
  if (chosen !== null && hosts[chosen]) return chosen
  return hostOrder.find((id) => hosts[id]) ?? null
}
