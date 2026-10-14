// spa/src/lib/fleet/probe-hosts-save.ts — the one write path of the probe-only host list (fleet spec §6, plan T-2a.4):
// `PUT /api/hostconfig/probe-hosts` with the whole list and the collection's revision.
//
// Two rules, the same as the other hostconfig lists (components/hosts/useHostConfigCollection.ts): (1) an action is a
// transformation of whatever list the store holds when it RUNS, never a list built at render time; (2) one PUT at a time
// per host (`queueHostConfigSave`), so a second click does not race the first into a 409 that discards its intent.
import { hostConfigQueueKey, queueHostConfigSave } from '../host-config-queue'
import { putProbeHosts } from './api'
import { useFleetStore } from './store'
import type { ProbeHostDef } from './types'

/** The most rows the daemon keeps (`normalizeProbeHosts`, plan T-1b.1). */
export const MAX_PROBE_HOSTS = 16

export type ProbeSaveOutcome =
  | { kind: 'ok' }
  /** 409: the list was reloaded from the daemon's current copy; the caller says it changed elsewhere. */
  | { kind: 'conflict' }
  /** 400: the daemon's plain-text reason, to show as it is. */
  | { kind: 'invalid'; text: string }
  | { kind: 'limit' }
  /** The request failed or the list is not loaded. */
  | { kind: 'failed'; reason: string }

export class ProbeLimitError extends Error {}

export function saveProbeHosts(
  hostId: string,
  mutate: (current: readonly ProbeHostDef[]) => readonly ProbeHostDef[] | null,
): Promise<ProbeSaveOutcome> {
  const run = async (): Promise<ProbeSaveOutcome> => {
    const store = useFleetStore.getState()
    const gen = store.genOf(hostId)
    const cur = store.defs[hostId]
    if (!cur) return { kind: 'failed', reason: 'probe host list not loaded' }
    let next: readonly ProbeHostDef[] | null
    try { next = mutate(cur.items) } catch (e) {
      if (e instanceof ProbeLimitError) return { kind: 'limit' }
      throw e
    }
    if (next === null) return { kind: 'ok' } // nothing to do: no revision is spent
    try {
      const res = await putProbeHosts(hostId, next, cur.revision)
      if (res.kind === 'invalid') return res
      if (res.current) useFleetStore.getState().applyDefs(hostId, gen, res.current)
      else await useFleetStore.getState().loadDefs(hostId)
      return { kind: res.kind }
    } catch (e) {
      return { kind: 'failed', reason: e instanceof Error ? e.message : String(e) }
    }
  }
  return queueHostConfigSave(hostConfigQueueKey(hostId, 'probe-hosts'), run)
}

/** Replace the row with the same id, or append (a new draft has id ''). */
export const upsertProbe = (row: ProbeHostDef) => (current: readonly ProbeHostDef[]): readonly ProbeHostDef[] => {
  const exists = row.id !== '' && current.some((i) => i.id === row.id)
  if (!exists && current.length >= MAX_PROBE_HOSTS) throw new ProbeLimitError()
  return exists ? current.map((i) => (i.id === row.id ? row : i)) : [...current, row]
}

/** Drop the row with `id`; null (nothing to do) when it is already gone. */
export const removeProbe = (id: string) => (current: readonly ProbeHostDef[]): readonly ProbeHostDef[] | null => {
  const next = current.filter((i) => i.id !== id)
  return next.length === current.length ? null : next
}
