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

/** The host holding the collection with the highest stamp, and that collection; `null` when none has loaded one. */
export function currentSource(byHost: Record<string, PersonaEntry>): { hostId: string; collection: PersonaCollection } | null {
  let best: { hostId: string; collection: PersonaCollection } | null = null
  for (const [hostId, entry] of Object.entries(byHost)) {
    const c = entry.collection
    if (c && (best === null || isNewerStamp(c.stamp, best.collection.stamp))) best = { hostId, collection: c }
  }
  return best
}

/** The whole collection with the highest stamp among the loaded hosts, or `null` when none has loaded one. */
export function currentCollection(byHost: Record<string, PersonaEntry>): PersonaCollection | null {
  return currentSource(byHost)?.collection ?? null
}

interface PersonaState {
  byHost: Record<string, PersonaEntry>
  load: (hostId: string) => Promise<void>
  forget: (hostId: string) => void
  /** The collection the App shows and edits (highest stamp, whole). */
  current: () => PersonaCollection | null
}

/**
 * A load's answer is usable only while no newer load started and the host was not forgotten since: each `load` and
 * `forget` takes the next number, and a response whose number is no longer the latest is dropped. Otherwise an older
 * response landing late would overwrite a newer collection, and a removed or re-pointed host's old daemon would
 * bring back what `forget` just dropped.
 */
const latest = new Map<string, number>()

function bump(hostId: string): number {
  const n = (latest.get(hostId) ?? 0) + 1
  latest.set(hostId, n)
  return n
}

export const usePersonaStore = create<PersonaState>()((set, get) => ({
  byHost: {},
  async load(hostId) {
    const mine = bump(hostId)
    const prev = get().byHost[hostId]
    set((s) => ({ byHost: { ...s.byHost, [hostId]: { status: 'loading', collection: prev?.collection ?? null } } }))
    try {
      const collection = await fetchPersonas(hostId)
      if (latest.get(hostId) !== mine) return
      set((s) => ({ byHost: { ...s.byHost, [hostId]: { status: 'ready', collection } } }))
    } catch {
      if (latest.get(hostId) !== mine) return
      set((s) => ({ byHost: { ...s.byHost, [hostId]: { status: 'error', collection: s.byHost[hostId]?.collection ?? null } } }))
    }
  },
  forget(hostId) {
    bump(hostId)
    set((s) => {
      const { [hostId]: _dropped, ...rest } = s.byHost
      return { byHost: rest }
    })
  },
  current: () => currentCollection(get().byHost),
}))
