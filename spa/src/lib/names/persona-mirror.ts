// spa/src/lib/names/persona-mirror.ts — copies the source host's personas to the other daemons (name pool v2 spec §4.3).
// The daemons do not sync roles among themselves; the App writes the source, then the same items to every other host
// that is connected and declares `personas.v1`. A daemon refuses a write that would remove a code it holds (400,
// index -1): that host has a role the source lacks, so the role is taken over into the source unchanged and the copy
// is made again, at most two rounds.
import { fetchPersonas, putPersonas, PersonaApiError } from './persona-api'
import { personasSupported } from './persona-loader'
import { useHostStore } from '../../stores/useHostStore'
import type { Persona } from './persona-types'

export interface MirrorReport {
  /** Hosts that now hold the saved items. */
  mirrored: string[]
  /** Hosts that could not be written, with the reason. */
  failed: { hostId: string; message: string }[]
  /** Codes that were on another host but not on the source, and were taken into the source. */
  pulled: string[]
}

type Push = { kind: 'ok' } | { kind: 'extra'; extras: Persona[] } | { kind: 'fail'; message: string }

const MAX_ROUNDS = 2

function mirrorTargets(sourceId: string): string[] {
  const { runtime } = useHostStore.getState()
  return Object.keys(runtime).filter((id) => id !== sourceId && runtime[id]?.status === 'connected' && personasSupported(id))
}

async function pushOne(hostId: string, items: Persona[], abort?: () => boolean): Promise<Push> {
  try {
    const theirs = await fetchPersonas(hostId)
    // The revision just read would let a stale copy through the compare-and-set, so a caller whose items may have been
    // replaced since it started (the backfill, against a save) gets to stop here, between the read and the write.
    if (abort?.()) return { kind: 'fail', message: 'aborted: the source changed' }
    await putPersonas(hostId, items, theirs.revision)
    return { kind: 'ok' }
  } catch (e) {
    if (e instanceof PersonaApiError && e.status === 400 && e.index === -1) {
      try {
        const theirs = await fetchPersonas(hostId)
        const have = new Set(items.map((p) => p.code))
        return { kind: 'extra', extras: theirs.items.filter((p) => !have.has(p.code)) }
      } catch (e2) {
        return { kind: 'fail', message: e2 instanceof Error ? e2.message : String(e2) }
      }
    }
    return { kind: 'fail', message: e instanceof Error ? e.message : String(e) }
  }
}

/**
 * `items` were just written to the source at `revision`. Returns the items and revision the source holds afterwards
 * (they change when a role was taken in) and what happened to each other host. `only` limits the copy to one host
 * (the backfill of a host that just connected). `abort` is asked between each host's read and write.
 */
export async function mirrorPersonas(
  sourceId: string, items: Persona[], revision: number, only?: string, abort?: () => boolean,
): Promise<{ report: MirrorReport; items: Persona[]; revision: number }> {
  let current = items
  let rev = revision
  const pulled: string[] = []
  const failed = new Map<string, string>()
  let mirrored: string[] = []
  const targets = mirrorTargets(sourceId).filter((id) => only === undefined || id === only)

  for (let round = 1; round <= MAX_ROUNDS; round++) {
    const live = targets.filter((id) => !failed.has(id))
    const results = await Promise.all(live.map((id) => pushOne(id, current, abort)))
    mirrored = []
    const extras = new Map<string, Persona>()
    const rejected: string[] = []
    results.forEach((r, i) => {
      if (r.kind === 'ok') mirrored.push(live[i])
      else if (r.kind === 'fail') failed.set(live[i], r.message)
      else {
        rejected.push(live[i])
        for (const p of r.extras) if (!extras.has(p.code)) extras.set(p.code, p)
      }
    })
    if (rejected.length === 0) break
    const noMore = (reason: string) => { for (const id of rejected) failed.set(id, reason) }
    if (round === MAX_ROUNDS) { noMore('still refused after taking its extra roles into the source'); break }
    const merged = [...current, ...extras.values()]
    try {
      rev = (await putPersonas(sourceId, merged, rev)).revision
    } catch (e) {
      noMore(e instanceof Error ? e.message : String(e))
      break
    }
    current = merged
    pulled.push(...extras.keys())
  }

  return {
    report: { mirrored, failed: [...failed].map(([hostId, message]) => ({ hostId, message })), pulled },
    items: current,
    revision: rev,
  }
}
