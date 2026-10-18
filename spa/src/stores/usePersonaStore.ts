// spa/src/stores/usePersonaStore.ts — per-host cache of the daemon's persona collection (name pool v2 spec §9.2).
// Not persisted: the daemon is the source of truth. Each daemon holds its own copy and they converge by last-writer-wins
// on the stamp, so the App reads every host and uses ONE collection, the one with the highest stamp. It never unions
// collections by code: a union would show — and on the next save revive — what last-writer-wins dropped.
import { create } from 'zustand'
import { fetchPersonas } from '../lib/names/persona-api'
import type { PersonaCollection, PersonaStamp } from '../lib/names/persona-types'

export type PersonaStatus = 'idle' | 'loading' | 'ready' | 'error'

export interface PersonaEntry {
  status: PersonaStatus
  collection: PersonaCollection | null
}

/** Newer when `at` is greater, or `at` is equal and `host` sorts higher (the daemons' rule, spec §4.3). */
export function isNewerStamp(a: PersonaStamp, b: PersonaStamp): boolean {
  if (a.at !== b.at) return a.at > b.at
  return a.host > b.host
}

/** The whole collection with the highest stamp among the loaded hosts, or `null` when none has loaded one. */
export function currentCollection(byHost: Record<string, PersonaEntry>): PersonaCollection | null {
  let best: PersonaCollection | null = null
  for (const entry of Object.values(byHost)) {
    const c = entry.collection
    if (c && (best === null || isNewerStamp(c.stamp, best.stamp))) best = c
  }
  return best
}

interface PersonaState {
  byHost: Record<string, PersonaEntry>
  load: (hostId: string) => Promise<void>
  forget: (hostId: string) => void
  /** The collection the App shows and edits (highest stamp, whole). */
  current: () => PersonaCollection | null
}

export const usePersonaStore = create<PersonaState>()((set, get) => ({
  byHost: {},
  async load(hostId) {
    const prev = get().byHost[hostId]
    set((s) => ({ byHost: { ...s.byHost, [hostId]: { status: 'loading', collection: prev?.collection ?? null } } }))
    try {
      const collection = await fetchPersonas(hostId)
      set((s) => ({ byHost: { ...s.byHost, [hostId]: { status: 'ready', collection } } }))
    } catch {
      set((s) => ({ byHost: { ...s.byHost, [hostId]: { status: 'error', collection: s.byHost[hostId]?.collection ?? null } } }))
    }
  },
  forget(hostId) {
    set((s) => {
      const { [hostId]: _dropped, ...rest } = s.byHost
      return { byHost: rest }
    })
  },
  current: () => currentCollection(get().byHost),
}))
