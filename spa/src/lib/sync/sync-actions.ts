// =============================================================================
// Sync Architecture — Actions
//
// High-level orchestration helpers that wrap SyncEngine for UI consumption.
// Kept pure (no store access) so they are easy to test and reuse from any
// code path that needs to trigger a sync.
// =============================================================================

import type { createSyncEngine } from './engine'
import type { ConflictItem, SyncBundle, SyncProvider } from './types'

// ---------------------------------------------------------------------------
// Result shape
// ---------------------------------------------------------------------------

export type SyncActionResult =
  | { kind: 'ok'; appliedBundle: SyncBundle }
  | {
      kind: 'conflicts'
      conflicts: ConflictItem[]
      remoteBundle: SyncBundle
      /**
       * The baseline to persist as the next `lastSyncedBundle`.
       *
       * Because `SyncEngine.pull` applies non-conflicting contributors
       * immediately (engine.ts), the local state of those contributors has
       * already advanced to the remote payload — so their baseline must
       * advance too. Conflicting contributors were left untouched and keep
       * their previous baseline until the user resolves the conflict.
       *
       * Persisting this bundle prevents the next sync from rebasing
       * already-applied contributors against a stale ancestor (issue #388).
       */
      partialBaseline: SyncBundle
    }
  | { kind: 'error'; error: string }

// ---------------------------------------------------------------------------
// syncNow — pull then push through a SyncProvider
// ---------------------------------------------------------------------------

export interface SyncNowDeps {
  provider: SyncProvider
  clientId: string
  lastSyncedBundle: SyncBundle | null
  enabledModules: string[]
  engine: ReturnType<typeof createSyncEngine>
}

export async function syncNow(deps: SyncNowDeps): Promise<SyncActionResult> {
  const { provider, clientId, lastSyncedBundle, enabledModules, engine } = deps

  try {
    const pullResult = await engine.pull(provider, lastSyncedBundle, enabledModules)

    if (pullResult.conflicts.length > 0 && pullResult.appliedBundle) {
      // Surface conflicts; caller decides how to resolve before the next push.
      return {
        kind: 'conflicts',
        conflicts: pullResult.conflicts,
        remoteBundle: pullResult.appliedBundle,
        partialBaseline: buildPartialBaseline(
          lastSyncedBundle,
          pullResult.appliedBundle,
          pullResult.conflicts,
        ),
      }
    }

    const pushedBundle = await engine.push(provider, clientId, enabledModules)
    return { kind: 'ok', appliedBundle: pushedBundle }
  } catch (err) {
    return { kind: 'error', error: errorMessage(err) }
  }
}

// ---------------------------------------------------------------------------
// applyImport — apply a bundle that came from a manual import
// ---------------------------------------------------------------------------

export interface ApplyImportDeps {
  bundle: SyncBundle
  lastSyncedBundle: SyncBundle | null
  enabledModules: string[]
  engine: ReturnType<typeof createSyncEngine>
}

export async function applyImport(deps: ApplyImportDeps): Promise<SyncActionResult> {
  const { bundle, lastSyncedBundle, enabledModules, engine } = deps

  try {
    const oneShotProvider: SyncProvider = {
      id: 'import',
      async push() {},
      async pull() {
        return bundle
      },
      async pushChunks() {},
      async pullChunks() {
        return {}
      },
      async listHistory() {
        return []
      },
    }

    const pullResult = await engine.pull(oneShotProvider, lastSyncedBundle, enabledModules)

    if (pullResult.conflicts.length > 0 && pullResult.appliedBundle) {
      return {
        kind: 'conflicts',
        conflicts: pullResult.conflicts,
        remoteBundle: pullResult.appliedBundle,
        partialBaseline: buildPartialBaseline(
          lastSyncedBundle,
          pullResult.appliedBundle,
          pullResult.conflicts,
        ),
      }
    }

    return { kind: 'ok', appliedBundle: pullResult.appliedBundle ?? bundle }
  } catch (err) {
    return { kind: 'error', error: errorMessage(err) }
  }
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

function errorMessage(err: unknown): string {
  if (err instanceof Error) return err.message
  return String(err)
}

/**
 * Merge the previous `lastSyncedBundle` with the remote bundle, taking remote
 * payloads only for contributors that had no conflicts. Conflicting
 * contributors keep their previous baseline until the user resolves them.
 */
function buildPartialBaseline(
  previous: SyncBundle | null,
  remoteBundle: SyncBundle,
  conflicts: ConflictItem[],
): SyncBundle {
  const conflictingIds = new Set(conflicts.map((c) => c.contributor))
  const collections: SyncBundle['collections'] = { ...(previous?.collections ?? {}) }
  for (const [id, payload] of Object.entries(remoteBundle.collections)) {
    if (!conflictingIds.has(id)) {
      collections[id] = payload
    }
  }
  return {
    version: remoteBundle.version,
    timestamp: remoteBundle.timestamp,
    device: remoteBundle.device,
    collections,
  }
}
