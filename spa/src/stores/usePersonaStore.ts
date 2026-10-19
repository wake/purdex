// spa/src/stores/usePersonaStore.ts — per-host cache of the daemon's persona collection (name pool v2 spec §9.2).
// Not persisted: the daemon is the source of truth. Each daemon holds its own copy and they converge by last-writer-wins
// on the stamp, so the App reads every host and uses ONE collection, the one with the highest stamp. It never unions
// collections by code: a union would show — and on the next save revive — what last-writer-wins dropped.
import { create } from 'zustand'
import { fetchPersonas } from '../lib/names/persona-api'
import { resolvedSourceId } from './usePersonaSourceStore'
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

export interface PersonaSource { hostId: string; collection: PersonaCollection; readOnly: boolean }

/**
 * The collection the App shows. The source host's copy wins while it has one that is not in error. When the source is
 * offline (no collection, or its last read failed) the copy with the highest stamp among the others stands in, and is
 * marked `readOnly`: an edit made on it could not be written to the source, so the page does not offer one. With no
 * `sourceId` (no host chosen yet) the highest stamp is used the same way.
 */
export function currentSource(byHost: Record<string, PersonaEntry>, sourceId: string | null = null): PersonaSource | null {
  const own = sourceId === null ? undefined : byHost[sourceId]
  if (sourceId !== null && own?.collection && own.status !== 'error') {
    return { hostId: sourceId, collection: own.collection, readOnly: false }
  }
  let best: { hostId: string; collection: PersonaCollection } | null = null
  for (const [hostId, entry] of Object.entries(byHost)) {
    if (hostId === sourceId) continue
    const c = entry.collection
    if (c && (best === null || isNewerStamp(c.stamp, best.collection.stamp))) best = { hostId, collection: c }
  }
  return best && { ...best, readOnly: sourceId !== null }
}

/** The whole collection the App shows, or `null` when no host has loaded one. */
export function currentCollection(byHost: Record<string, PersonaEntry>, sourceId: string | null = null): PersonaCollection | null {
  return currentSource(byHost, sourceId)?.collection ?? null
}

interface PersonaState {
  byHost: Record<string, PersonaEntry>
  load: (hostId: string) => Promise<void>
  forget: (hostId: string) => void
  /** The host dropped its connection: its copy stays (a stand-in can still be shown) but is no longer a live source. */
  markOffline: (hostId: string) => void
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
  markOffline(hostId) {
    set((s) => {
      const entry = s.byHost[hostId]
      if (!entry || entry.status === 'error') return s
      return { byHost: { ...s.byHost, [hostId]: { ...entry, status: 'error' } } }
    })
  },
  current: () => currentCollection(get().byHost, resolvedSourceId()),
}))
