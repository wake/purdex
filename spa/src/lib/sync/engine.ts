// =============================================================================
// Sync Architecture — SyncEngine
// =============================================================================

import type {
  SyncBundle,
  SyncContributor,
  SyncProvider,
  ConflictItem,
  ResolvedFields,
  FullPayload,
} from './types'
import { mergeCollection } from './three-way-merge'

// ---------------------------------------------------------------------------
// Public types
// ---------------------------------------------------------------------------

export interface PullResult {
  appliedBundle: SyncBundle | null
  conflicts: ConflictItem[]
}

// ---------------------------------------------------------------------------
// Implementation
// ---------------------------------------------------------------------------

export function createSyncEngine() {
  const contributors = new Map<string, SyncContributor>()

  // -------------------------------------------------------------------------
  // register
  // -------------------------------------------------------------------------

  function register(contributor: SyncContributor): void {
    contributors.set(contributor.id, contributor)
  }

  // -------------------------------------------------------------------------
  // getContributors
  // -------------------------------------------------------------------------

  function getContributors(): SyncContributor[] {
    return Array.from(contributors.values())
  }

  // -------------------------------------------------------------------------
  // serialize
  // -------------------------------------------------------------------------

  function serialize(device: string, enabledModules: string[]): SyncBundle {
    const collections: SyncBundle['collections'] = {}

    for (const id of enabledModules) {
      const contributor = contributors.get(id)
      if (!contributor) continue
      collections[id] = contributor.serialize()
    }

    return {
      version: 1,
      timestamp: Date.now(),
      device,
      collections,
    }
  }

  // -------------------------------------------------------------------------
  // push
  // -------------------------------------------------------------------------

  async function push(
    provider: SyncProvider,
    device: string,
    enabledModules: string[],
  ): Promise<SyncBundle> {
    const bundle = serialize(device, enabledModules)
    await provider.push(bundle)
    return bundle
  }

  // -------------------------------------------------------------------------
  // pull
  // -------------------------------------------------------------------------

  async function pull(
    provider: SyncProvider,
    lastSynced: SyncBundle | null,
    enabledModules: string[],
  ): Promise<PullResult> {
    const remoteBundle = await provider.pull()

    // No data on remote yet
    if (remoteBundle === null) {
      return { appliedBundle: null, conflicts: [] }
    }

    // First sync: full-replace all enabled contributors
    if (lastSynced === null) {
      for (const id of enabledModules) {
        const contributor = contributors.get(id)
        if (!contributor) continue
        const payload = remoteBundle.collections[id]
        if (!payload) continue
        contributor.deserialize(payload, { type: 'full-replace' })
      }
      return { appliedBundle: remoteBundle, conflicts: [] }
    }

    // Subsequent sync: three-way merge per contributor
    const allConflicts: ConflictItem[] = []

    for (const id of enabledModules) {
      const contributor = contributors.get(id)
      if (!contributor) continue
      if (contributor.strategy !== 'full') continue

      const remotePayload = remoteBundle.collections[id] as FullPayload | undefined
      if (!remotePayload) continue

      const lastPayload = lastSynced.collections[id] as FullPayload | undefined
      const localPayload = contributor.serialize() as FullPayload

      const lastData = lastPayload ? (lastPayload.data as Record<string, unknown>) : null
      const localData = localPayload.data as Record<string, unknown>
      const remoteData = remotePayload.data as Record<string, unknown>

      const { merged, conflicts } = mergeCollection(
        lastData,
        localData,
        remoteData,
        remoteBundle.device,
      )

      // Tag each conflict with the contributor id
      const taggedConflicts = conflicts.map((c) => ({ ...c, contributor: id }))
      allConflicts.push(...taggedConflicts)

      // If no conflicts for this contributor, apply the merged result immediately
      if (taggedConflicts.length === 0) {
        const mergedPayload: FullPayload = { version: remotePayload.version, data: merged }
        contributor.deserialize(mergedPayload, { type: 'full-replace' })
      }
    }

    return { appliedBundle: remoteBundle, conflicts: allConflicts }
  }

  // -------------------------------------------------------------------------
  // resolveConflicts
  // -------------------------------------------------------------------------

  function resolveConflicts(
    remoteBundle: SyncBundle,
    conflicts: ConflictItem[],
    resolved: ResolvedFields,
  ): void {
    // Collect the unique set of contributors involved in the conflicts
    const contributorIds = new Set(conflicts.map((c) => c.contributor))

    for (const id of contributorIds) {
      const contributor = contributors.get(id)
      if (!contributor) continue

      const remotePayload = remoteBundle.collections[id] as FullPayload | undefined
      if (!remotePayload) continue

      // Build merged data: start from local state, overlay resolved choices
      const localPayload = contributor.serialize() as FullPayload
      const localData = { ...localPayload.data } as Record<string, unknown>
      const remoteData = remotePayload.data as Record<string, unknown>

      const merged: Record<string, unknown> = { ...localData }

      for (const [field, choice] of Object.entries(resolved)) {
        if (choice === 'remote') {
          merged[field] = remoteData[field]
        }
        // 'local' keeps the local value already in merged
      }

      const mergedPayload: FullPayload = { version: remotePayload.version, data: merged }
      contributor.deserialize(mergedPayload, { type: 'field-merge', resolved })
    }
  }

  // -------------------------------------------------------------------------
  // Public interface
  // -------------------------------------------------------------------------

  return {
    register,
    getContributors,
    serialize,
    push,
    pull,
    resolveConflicts,
  }
}
