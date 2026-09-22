// spa/src/components/settings/profile/resolve-counts.ts — what the Resolve confirmation counts (P3d-4 plan, "Every
// action goes through a ConfirmDialog", R1, R5). COUNTS, NOT A DIFF — the precedent is the wizard's `countWorld`.
//
// EACH SIDE IS COUNTED FROM WHERE THE CHOICE TAKES IT (R1):
//   - this device, `locked:conflict` → the payload that was SENT (`conflict.localHash`, from the section store's
//     stash — any window can read it): "Keep this device's" restores THAT snapshot, not what is here now;
//   - this device, `locked:reset` / `locked:invalid` → the payload the collector would build NOW
//     (`buildSectionPayload`, over the master world): no snapshot is restored, the current state is pushed. When it
//     no longer hashes like the lock's `currentHash`, that is said;
//   - the host → ONE read-only `getSection`, made when the confirmation opens. Its rev ≠ the frozen lock's → said.
// Whatever cannot be read (no stash entry, an unsettled world, a failed request, a shape this build does not know) is
// `unreadable` — "could not be read", never a guess. The counts inform; they never gate the action.
import { getSection } from '../../../lib/profile/api'
import { buildSectionPayload } from '../../../lib/profile/collector'
import type { SectionLock } from '../../../lib/profile/executor'
import { hashSection } from '../../../lib/profile/hash'
import { sectionKind } from '../../../lib/profile/projections'
import { getStash } from '../../../lib/profile/section-store'
import type { ProfileSectionKey } from '../../../lib/profile/types'

export type SideCount = { state: 'read'; count: number } | { state: 'unreadable' }

export interface LocalSide {
  count: SideCount
  /** Reset / invalid only: what is built now no longer hashes like the lock's `currentHash`. */
  changedSince: boolean
}

export interface HostSide {
  count: SideCount
  /** The host's rev (or its having the section at all) is not the frozen lock's any more. */
  movedOn: boolean
}

const UNREADABLE: SideCount = { state: 'unreadable' }

function isRecord(v: unknown): v is Record<string, unknown> {
  return typeof v === 'object' && v !== null && !Array.isArray(v)
}

const keysOf = (v: unknown): number | null => (isRecord(v) ? Object.keys(v).length : null)

/** hosts → hosts · workspaces → workspaces · tabs → tabs · settings → every entry of every store. `null` payload (the
 *  section is absent on that side) → 0. Not the section's shape → null. */
export function countPayload(key: string, payload: unknown): number | null {
  if (payload === null) return 0
  if (!isRecord(payload)) return null
  switch (sectionKind(key)) {
    case 'hosts':
      return keysOf(payload.hosts)
    case 'workspaces':
      return keysOf(payload.workspaces)
    case 'tabs':
      return keysOf(payload.tabs)
    case 'settings': {
      let total = 0
      for (const fields of Object.values(payload)) {
        const n = keysOf(fields)
        if (n === null) return null
        total += n
      }
      return total
    }
    default:
      return null
  }
}

function sideCount(key: string, payload: unknown): SideCount {
  const count = countPayload(key, payload)
  return count === null ? UNREADABLE : { state: 'read', count }
}

/** What "Keep this device's" keeps. */
export async function readLocalSide(profileId: string, key: string, lock: SectionLock): Promise<LocalSide> {
  if (lock.conflict !== null) {
    const { localHash } = lock.conflict
    if (localHash === null) return { count: { state: 'read', count: 0 }, changedSince: false }
    const sent = getStash(profileId, localHash)
    return { count: sent === undefined ? UNREADABLE : sideCount(key, sent), changedSince: false }
  }
  let built: { payload: unknown | null } | null
  try {
    built = buildSectionPayload(key as ProfileSectionKey)
  } catch {
    built = null
  }
  if (built === null) return { count: UNREADABLE, changedSince: false }
  const count = sideCount(key, built.payload)
  let hash: string | null = null
  try {
    hash = built.payload === null ? null : await hashSection(built.payload)
  } catch {
    return { count, changedSince: false } // unhashable: nothing is claimed
  }
  return { count, changedSince: hash !== lock.currentHash }
}

/** What the host holds now — what "Take the host's" takes, and what "Keep this device's" replaces. `expectEndpoint`:
 *  the daemon the attachment is on — the request is sent there or not at all (api.ts, review A3). */
export async function readHostSide(hostId: string, profileId: string, key: string, lock: SectionLock, opts: { expectEndpoint: string; signal?: AbortSignal }): Promise<HostSide> {
  try {
    const result = await getSection(hostId, profileId, key, { signal: opts.signal, expectEndpoint: opts.expectEndpoint })
    if (result.kind !== 'ok') return { count: UNREADABLE, movedOn: false }
    if (result.value === null) return { count: { state: 'read', count: 0 }, movedOn: lock.sot.hash !== null }
    return { count: sideCount(key, result.value.payload), movedOn: result.value.rev !== lock.sot.rev }
  } catch {
    return { count: UNREADABLE, movedOn: false }
  }
}
