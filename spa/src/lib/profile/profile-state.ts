// spa/src/lib/profile/profile-state.ts — the profile level of Profile Sync
// (spec 2026-09-20-profile-sync §4.4, §4.5, §4.6.3): the shape verdict, the
// whole-profile schema lock, the status roll-up over the sections, and which
// `tabs.*` sections should exist. `sync-state.ts` owns one section; this file
// owns what only makes sense across all of them.
//
// No fetching, no clocks, no stores — every input is a parameter, nothing is
// mutated, and every list that comes out is sorted, so the same inputs in any
// order give the same answer.
import { sectionKind, tabsSectionKey, workspaceIdOf } from './projections'
import type { SectionSyncState } from './sync-state'
import type { ProfileSectionKey, SectionKind, Shape, SotIndexEntry } from './types'

// === Shape (§4.5) ===

export type ShapeVerdict = 'ok' | 'i-am-newer' | 'sot-is-newer' | 'shape-changed-without-ordinal'

/**
 * §4.5's table. The fingerprint detects a change, the ordinal decides its
 * direction; a changed fingerprint with an equal ordinal means someone forgot
 * the bump, and no direction can be trusted — fail closed.
 */
export function compareShape(mine: Shape, sot: Shape): ShapeVerdict {
  if (mine.fingerprint === sot.fingerprint) return 'ok'
  if (mine.ordinal > sot.ordinal) return 'i-am-newer'
  if (mine.ordinal < sot.ordinal) return 'sot-is-newer'
  return 'shape-changed-without-ordinal'
}

// === Schema lock (§4.4) ===

/** Why the whole profile is locked: the section that offends, and both shapes, so the panel can name it. */
export interface SchemaLock {
  section: string
  kind: SectionKind
  verdict: 'sot-is-newer' | 'shape-changed-without-ordinal'
  mine: Shape
  sot: Shape
}

function byKey(a: string, b: string): number {
  return a < b ? -1 : a > b ? 1 : 0
}

/**
 * The first offending section of the SOT index — first by section key, not by
 * input order — or `null` when this client may write. Any offender locks the
 * WHOLE profile: the caller must not write any section while this is non-null.
 *
 * A section whose kind this client does not know is skipped: it is carried and
 * never rewritten (§4.6.3), so this client cannot damage it. All `tabs.*`
 * sections are compared against `mine.tabs`.
 */
export function profileLock(index: readonly SotIndexEntry[], mine: Record<SectionKind, Shape>): SchemaLock | null {
  const sorted = [...index].sort((a, b) => byKey(a.section, b.section))
  for (const entry of sorted) {
    const kind = sectionKind(entry.section)
    if (kind === null) continue
    const sot: Shape = { fingerprint: entry.fingerprint, ordinal: entry.ordinal }
    const verdict = compareShape(mine[kind], sot)
    if (verdict === 'ok' || verdict === 'i-am-newer') continue
    return { section: entry.section, kind, verdict, mine: mine[kind], sot }
  }
  return null
}

// === Profile status (§4.4) ===

export type ProfileStatus = 'idle' | 'locked:schema' | 'locked:conflict' | 'locked:reset' | 'locked:invalid' | 'pending' | 'synced'

/** Worst last. `locked:reset` outranks `locked:conflict`: the SOT the section was based on is gone.
 *  `locked:invalid` sits under both — nothing of the user's is at stake, the section merely cannot
 *  take what the SOT holds — but above `pending`, which resolves itself; this does not. */
const SEVERITY: readonly SectionSyncState['status'][] = ['synced', 'pending', 'locked:invalid', 'locked:conflict', 'locked:reset']

/** `idle` without a master, `locked:schema` under a lock, otherwise the worst of the sections (`synced` when there are none). */
export function profileStatus(args: {
  hasMaster: boolean
  sections: Record<string, SectionSyncState>
  lock: SchemaLock | null
}): ProfileStatus {
  if (!args.hasMaster) return 'idle'
  if (args.lock !== null) return 'locked:schema'
  let worst = 0
  for (const key of Object.keys(args.sections)) {
    worst = Math.max(worst, SEVERITY.indexOf(args.sections[key].status))
  }
  return SEVERITY[worst]
}

// === Section lifecycle (§4.6.3) ===

function sortedUnique<T extends string>(items: Iterable<T>): T[] {
  return [...new Set(items)].sort(byKey)
}

/**
 * Which `tabs.*` sections should come and go, given a `workspaces` section that
 * has just been APPLIED — that section is the authority.
 *
 * §4.6.3 says both "a client that has applied `workspaces` deletes the `tabs.*`
 * sections for workspaces that are gone" and "a `tabs.<wsId>` whose workspace is
 * not in `workspaces` is kept, not deleted". The two are reconciled by what this
 * client knew before the apply (`previousWorkspaceIds`):
 *
 * - `remove` — `tabs.<id>` exists on either side, the client knew workspace `id`,
 *   and the applied `workspaces` no longer has it: the workspace is gone.
 * - `keepUnrendered` — `tabs.<id>` exists, `id` is in neither set: an arrival
 *   that overtook its workspace, or a deletion this client never witnessed. Kept
 *   and not rendered; a later `workspaces` apply settles which.
 * - `create` — a workspace with no `tabs.<id>` on EITHER side. Missing on one
 *   side only is an ordinary push or pull, the section state machine's business.
 *   Throws (via `tabsSectionKey`) for an id the daemon would reject.
 * - `unknown` — keys of a kind this client does not know. Reported, carried,
 *   never removed (forward compatibility).
 *
 * `hosts` / `settings` / `workspaces` never appear in any list.
 */
export function reconcileSectionSet(args: {
  workspaceIds: readonly string[]
  previousWorkspaceIds: readonly string[]
  localKeys: readonly string[]
  sotKeys: readonly string[]
}): { create: ProfileSectionKey[]; remove: ProfileSectionKey[]; keepUnrendered: string[]; unknown: string[] } {
  const wanted = new Set(args.workspaceIds)
  const known = new Set(args.previousWorkspaceIds)
  const present = new Set([...args.localKeys, ...args.sotKeys])

  const create = [...wanted].map(tabsSectionKey).filter((key) => !present.has(key))
  const remove: ProfileSectionKey[] = []
  const keepUnrendered: string[] = []
  const unknown: string[] = []
  for (const key of present) {
    if (sectionKind(key) === null) {
      unknown.push(key)
      continue
    }
    const id = workspaceIdOf(key)
    if (id === null || wanted.has(id)) continue
    if (known.has(id)) remove.push(tabsSectionKey(id))
    else keepUnrendered.push(key)
  }
  return {
    create: sortedUnique(create),
    remove: sortedUnique(remove),
    keepUnrendered: sortedUnique(keepUnrendered),
    unknown: sortedUnique(unknown),
  }
}
