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
