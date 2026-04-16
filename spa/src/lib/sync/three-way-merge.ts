// =============================================================================
// Sync Architecture — Three-Way Merge
// =============================================================================

import type { ConflictItem } from './types'

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

/**
 * Result of a three-way conflict detection on a single field.
 *
 * - `no-change`  — all three values are equal; nothing to do.
 * - `use-local`  — only the local side changed; keep local.
 * - `use-remote` — only the remote side changed; adopt remote.
 * - `both-same`  — both sides changed to the same value; no conflict.
 * - `conflict`   — both sides changed to different values; needs resolution.
 */
export type ConflictResult =
  | 'no-change'
  | 'use-local'
  | 'use-remote'
  | 'both-same'
  | 'conflict'

/** Output of mergeCollection. */
export interface MergeCollectionResult {
  /** The merged data object. Conflict fields retain the local value as a placeholder. */
  merged: Record<string, unknown>
  /** Fields that could not be auto-resolved. Caller must fill in `contributor`. */
  conflicts: ConflictItem[]
}

// ---------------------------------------------------------------------------
// Private helpers
// ---------------------------------------------------------------------------

/**
 * Deep equality for JSON-serializable values (primitives, null, plain objects,
 * arrays). Does not handle Date, Set, Map, or functions.
 */
function deepEqual(a: unknown, b: unknown): boolean {
  if (a === b) return true

  // Handle null explicitly (typeof null === 'object')
  if (a === null || b === null) return false
  if (a === undefined || b === undefined) return false

  if (typeof a !== typeof b) return false
  if (typeof a !== 'object') return false

  // Both are non-null objects at this point
  if (Array.isArray(a) !== Array.isArray(b)) return false

  if (Array.isArray(a) && Array.isArray(b)) {
    if (a.length !== b.length) return false
    for (let i = 0; i < a.length; i++) {
      if (!deepEqual(a[i], b[i])) return false
    }
    return true
  }

  // Plain objects
  const aObj = a as Record<string, unknown>
  const bObj = b as Record<string, unknown>
  const aKeys = Object.keys(aObj)
  const bKeys = Object.keys(bObj)

  if (aKeys.length !== bKeys.length) return false

  for (const key of aKeys) {
    if (!Object.prototype.hasOwnProperty.call(bObj, key)) return false
    if (!deepEqual(aObj[key], bObj[key])) return false
  }

  return true
}

// ---------------------------------------------------------------------------
// Public API
// ---------------------------------------------------------------------------

/**
 * Three-way conflict detection for a single field.
 *
 * @param last   The value at the time of the last successful sync (ancestor).
 * @param local  The current local value.
 * @param remote The value from the remote peer.
 */
export function detectConflict(
  last: unknown,
  local: unknown,
  remote: unknown,
): ConflictResult {
  const localChanged = !deepEqual(last, local)
  const remoteChanged = !deepEqual(last, remote)

  if (!localChanged && !remoteChanged) return 'no-change'
  if (localChanged && !remoteChanged) return 'use-local'
  if (!localChanged && remoteChanged) return 'use-remote'

  // Both changed — check if they converged on the same value
  if (deepEqual(local, remote)) return 'both-same'

  return 'conflict'
}

/**
 * Merge two flat data objects using three-way comparison against their common
 * ancestor (`last`).
 *
 * When `last` is `null` this is the first sync: fully replace with the remote
 * state and produce no conflicts.
 *
 * @param last         Common ancestor record, or `null` for a first-time sync.
 * @param local        Current local record.
 * @param remote       Incoming remote record.
 * @param remoteDevice Device identifier of the remote peer (stored in conflicts).
 */
export function mergeCollection(
  last: Record<string, unknown> | null,
  local: Record<string, unknown>,
  remote: Record<string, unknown>,
  remoteDevice: string,
): MergeCollectionResult {
  // First sync: full-replace from remote, no conflicts
  if (last === null) {
    return { merged: { ...remote }, conflicts: [] }
  }

  const merged: Record<string, unknown> = {}
  const conflicts: ConflictItem[] = []

  // Collect the union of all keys across all three objects
  const allKeys = new Set([
    ...Object.keys(last),
    ...Object.keys(local),
    ...Object.keys(remote),
  ])

  for (const key of allKeys) {
    const lastVal = Object.prototype.hasOwnProperty.call(last, key) ? last[key] : undefined
    const localVal = Object.prototype.hasOwnProperty.call(local, key) ? local[key] : undefined
    const remoteVal = Object.prototype.hasOwnProperty.call(remote, key) ? remote[key] : undefined

    const resolution = detectConflict(lastVal, localVal, remoteVal)

    switch (resolution) {
      case 'no-change':
      case 'both-same':
        // Use local (identical to remote for both-same)
        if (localVal !== undefined) {
          merged[key] = localVal
        }
        // If both are undefined (key removed from both), omit from merged
        break

      case 'use-local':
        // Local changed; if local deleted the key, omit it
        if (localVal !== undefined) {
          merged[key] = localVal
        }
        break

      case 'use-remote':
        // Remote changed; if remote deleted the key, omit it
        if (remoteVal !== undefined) {
          merged[key] = remoteVal
        }
        break

      case 'conflict':
        // Keep local as placeholder; record conflict for UI resolution
        if (localVal !== undefined) {
          merged[key] = localVal
        }
        conflicts.push({
          contributor: '', // caller fills this in
          field: key,
          lastSynced: lastVal,
          local: localVal,
          remote: {
            value: remoteVal,
            device: remoteDevice,
          },
        })
        break
    }
  }

  return { merged, conflicts }
}
