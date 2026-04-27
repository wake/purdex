// =============================================================================
// Quick Commands — pure data module (Phase 1a Q2 extraction, issue #679)
// =============================================================================
// Schema, sanitizer, own-property guard, and persist merge function. Imported
// by both `useQuickCommandStore` and the sync contributor so neither has to
// drag the whole zustand store to reach the data primitives.
// =============================================================================

import type { QuickCommandSlotId } from './quick-command-slots'

export interface QuickCommand {
  id: string
  name: string
  command: string
  icon?: string
  category?: string
  hostOnly?: boolean
}

export type Bindings = Record<string /* commandId */, QuickCommandSlotId[]>

/**
 * Pure data shape persisted to storage and exchanged via sync. The store
 * extends this with action methods; the sync contributor reuses it directly.
 */
export interface QuickCommandData {
  global: QuickCommand[]
  byHost: Record<string, QuickCommand[]>
  bindings: Bindings
}

export const UNSAFE_KEYS = new Set(['__proto__', 'constructor', 'prototype'])

/**
 * AR-1 (mirrors `useModuleEnabledStore` codex review #617):
 * Sanitize bindings on rehydrate / sync deserialize. A corrupted payload —
 * hand-edited localStorage, failed migration, hostile sync source — must
 * NEVER let arbitrary string keys silently mount commands into slots.
 *
 * Rules (spec §2.3 — forward-compat with future slot ids):
 *  - Top-level value must be a plain object (rejects arrays, null, primitives).
 *  - Reject `__proto__` / `constructor` / `prototype` command id keys
 *    (prototype pollution).
 *  - Drop entries whose value is not an array.
 *  - Within each array, keep only non-empty strings. NO slot id whitelist —
 *    spec §2.3 explicitly requires accepting unknown slot ids in Phase 1
 *    so cross-version sync (older client pulls newer client's future slot
 *    binding) doesn't lose data; SlotHost simply ignores unknown slot ids
 *    at render time.
 *  - Drop entries whose cleaned array is empty (matches setBinding(id, [])).
 */
export function sanitizeBindings(raw: unknown): Bindings {
  if (raw === null || typeof raw !== 'object' || Array.isArray(raw)) return {}
  const out: Bindings = {}
  for (const [cmdId, targets] of Object.entries(raw as Record<string, unknown>)) {
    if (typeof cmdId !== 'string' || cmdId.length === 0) continue
    if (UNSAFE_KEYS.has(cmdId)) continue
    if (!Array.isArray(targets)) continue
    const cleaned: QuickCommandSlotId[] = []
    for (const t of targets) {
      if (typeof t === 'string' && t.length > 0) cleaned.push(t as QuickCommandSlotId)
    }
    if (cleaned.length > 0) out[cmdId] = cleaned
  }
  return out
}

/**
 * Read bindings[cmdId] safely. Prevents inherited-prop attacks where cmdId
 * happens to match Object.prototype methods (toString / valueOf / hasOwnProperty
 * / isPrototypeOf / etc.) — naive `bindings[cmdId]` would resolve to a
 * non-array function and `.includes(slot)` would throw, crashing
 * getBoundCommands for every caller (DoS).
 *
 * Returns undefined unless `bindings` has its OWN property `cmdId` AND that
 * value is an array.
 */
export function getBindingTargets(
  bindings: Bindings,
  cmdId: string,
): QuickCommandSlotId[] | undefined {
  if (!Object.prototype.hasOwnProperty.call(bindings, cmdId)) return undefined
  const targets = bindings[cmdId]
  return Array.isArray(targets) ? targets : undefined
}

/**
 * Pure merge function — used by zustand persist `merge` hook AND directly by
 * tests to drive the real hydrate trust boundary with malformed payloads.
 * Generic over `T extends QuickCommandData` so the store can pass its full
 * `QuickCommandState` (data + actions) and recover the same shape after merge.
 */
export function mergePersistedQuickCommandState<T extends QuickCommandData>(
  persisted: unknown,
  current: T,
): T {
  const p = persisted as
    | { global?: unknown; byHost?: unknown; bindings?: unknown }
    | null
    | undefined
  return {
    ...current,
    global: Array.isArray(p?.global) ? (p?.global as QuickCommand[]) : current.global,
    byHost:
      typeof p?.byHost === 'object' && p?.byHost !== null && !Array.isArray(p?.byHost)
        ? (p?.byHost as Record<string, QuickCommand[]>)
        : current.byHost,
    bindings: sanitizeBindings(p?.bindings),
  }
}
