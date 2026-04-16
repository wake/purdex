// =============================================================================
// Sync Architecture — Integration Tests: Full Sync Flow
// =============================================================================
//
// These tests exercise the complete sync cycle using only in-memory mocks —
// no real stores or providers.  They verify that the SyncEngine correctly
// orchestrates: serialize → push → pull → conflict detection → resolve.
// =============================================================================

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { createSyncEngine } from './engine'
import type { SyncBundle, SyncContributor, FullPayload, MergeStrategy } from './types'

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

/**
 * Simulates a Zustand store as a simple in-memory object.
 */
function createTestContributor(id: string, initialData: Record<string, unknown>) {
  let data = { ...initialData }
  const contrib: SyncContributor = {
    id,
    strategy: 'full',
    serialize: () => ({ version: 1, data: { ...data } }),
    deserialize: (payload: unknown, merge: MergeStrategy) => {
      const p = (payload as FullPayload).data
      if (merge.type === 'full-replace') {
        data = { ...p }
        return
      }
      for (const [field, choice] of Object.entries(merge.resolved)) {
        if (choice === 'remote' && field in p) data[field] = p[field]
      }
    },
    getVersion: () => 1,
  }
  return {
    contrib,
    getData: () => ({ ...data }),
    setData: (d: Record<string, unknown>) => {
      data = { ...d }
    },
  }
}

/**
 * In-memory provider simulating daemon canonical bundle.
 */
function createInMemoryProvider() {
  let stored: SyncBundle | null = null
  return {
    id: 'memory',
    push: vi.fn(async (bundle: SyncBundle) => {
      stored = bundle
    }),
    pull: vi.fn(async () => stored),
    pushChunks: vi.fn(async () => {}),
    pullChunks: vi.fn(async () => ({})),
    listHistory: vi.fn(async () => []),
  }
}

// ---------------------------------------------------------------------------
// Integration tests
// ---------------------------------------------------------------------------

describe('Sync Flow Integration', () => {
  // -------------------------------------------------------------------------
  // Scenario 1: Device A pushes, Device B pulls (first sync — no conflict)
  // -------------------------------------------------------------------------

  describe('Scenario 1: first sync — full-replace', () => {
    it('Device B inherits Device A state on first pull (lastSynced=null)', async () => {
      const provider = createInMemoryProvider()

      // Device A: push its state
      const engineA = createSyncEngine()
      const contribA = createTestContributor('prefs', { theme: 'dark', locale: 'en' })
      engineA.register(contribA.contrib)
      await engineA.push(provider, 'device-a', ['prefs'])

      expect(provider.push).toHaveBeenCalledOnce()

      // Device B: different local state, but lastSynced is null (first sync)
      const engineB = createSyncEngine()
      const contribB = createTestContributor('prefs', { theme: 'light', locale: 'en' })
      engineB.register(contribB.contrib)

      const result = await engineB.pull(provider, null, ['prefs'])

      expect(result.conflicts).toHaveLength(0)
      expect(result.appliedBundle).not.toBeNull()

      // B should now have A's state via full-replace
      expect(contribB.getData()).toEqual({ theme: 'dark', locale: 'en' })
    })
  })

  // -------------------------------------------------------------------------
  // Scenario 2: Both devices change different fields — auto-merge
  // -------------------------------------------------------------------------

  describe('Scenario 2: diverged changes on different fields — auto-merge', () => {
    it('merges cleanly when A changes theme and B changes locale', async () => {
      const provider = createInMemoryProvider()

      // Establish common baseline: push lastSynced state
      const lastSynced: SyncBundle = {
        version: 1,
        timestamp: Date.now() - 60_000,
        device: 'device-a',
        collections: {
          prefs: { version: 1, data: { theme: 'light', locale: 'en' } },
        },
      }
      // Seed the provider with the baseline bundle
      await provider.push(lastSynced)

      // Device A changes theme → 'dark', pushes new state
      const engineA = createSyncEngine()
      const contribA = createTestContributor('prefs', { theme: 'dark', locale: 'en' })
      engineA.register(contribA.contrib)
      const bundleA = await engineA.push(provider, 'device-a', ['prefs'])

      // Device B has changed locale → 'zh-TW' since lastSynced
      const engineB = createSyncEngine()
      const contribB = createTestContributor('prefs', { theme: 'light', locale: 'zh-TW' })
      engineB.register(contribB.contrib)

      // B pulls using the shared lastSynced bundle as ancestor
      const result = await engineB.pull(provider, lastSynced, ['prefs'])

      expect(result.conflicts).toHaveLength(0)
      expect(result.appliedBundle).toBe(bundleA)

      // Both changes preserved: A's theme + B's locale
      expect(contribB.getData()).toEqual({ theme: 'dark', locale: 'zh-TW' })
    })
  })

  // -------------------------------------------------------------------------
  // Scenario 3: Both devices change same field — conflict detected + resolved
  // -------------------------------------------------------------------------

  describe('Scenario 3: conflicting change on same field — manual resolution', () => {
    it('detects conflict and applies remote after user resolves with remote', async () => {
      const provider = createInMemoryProvider()

      const lastSynced: SyncBundle = {
        version: 1,
        timestamp: Date.now() - 60_000,
        device: 'device-a',
        collections: {
          prefs: { version: 1, data: { theme: 'light' } },
        },
      }

      // Device A changes theme → 'dark', pushes
      const engineA = createSyncEngine()
      const contribA = createTestContributor('prefs', { theme: 'dark' })
      engineA.register(contribA.contrib)
      const bundleA = await engineA.push(provider, 'device-a', ['prefs'])

      // Device B has independently changed theme → 'solarized'
      const engineB = createSyncEngine()
      const contribB = createTestContributor('prefs', { theme: 'solarized' })
      engineB.register(contribB.contrib)

      const result = await engineB.pull(provider, lastSynced, ['prefs'])

      // Exactly one conflict on 'theme'
      expect(result.conflicts).toHaveLength(1)
      const conflict = result.conflicts[0]
      expect(conflict.contributor).toBe('prefs')
      expect(conflict.field).toBe('theme')
      expect(conflict.local).toBe('solarized')
      expect(conflict.remote.value).toBe('dark')
      expect(conflict.lastSynced).toBe('light')

      // B resolves by choosing 'remote'
      engineB.resolveConflicts(bundleA, result.conflicts, { theme: 'remote' })

      // After resolution B should have the remote value
      expect(contribB.getData()).toEqual({ theme: 'dark' })
    })
  })

  // -------------------------------------------------------------------------
  // Scenario 4: Multiple contributors — independent merge
  // -------------------------------------------------------------------------

  describe('Scenario 4: multiple contributors — independent auto-merge', () => {
    it('auto-merges prefs and layout contributors independently without conflicts', async () => {
      const provider = createInMemoryProvider()

      const lastSynced: SyncBundle = {
        version: 1,
        timestamp: Date.now() - 60_000,
        device: 'device-a',
        collections: {
          prefs: { version: 1, data: { theme: 'light' } },
          layout: { version: 1, data: { sidebar: false } },
        },
      }

      // Device A changes prefs.theme → 'dark', layout unchanged
      const engineA = createSyncEngine()
      const prefsA = createTestContributor('prefs', { theme: 'dark' })
      const layoutA = createTestContributor('layout', { sidebar: false })
      engineA.register(prefsA.contrib)
      engineA.register(layoutA.contrib)
      const bundleA = await engineA.push(provider, 'device-a', ['prefs', 'layout'])

      // Device B changes layout.sidebar → true, prefs unchanged
      const engineB = createSyncEngine()
      const prefsB = createTestContributor('prefs', { theme: 'light' })
      const layoutB = createTestContributor('layout', { sidebar: true })
      engineB.register(prefsB.contrib)
      engineB.register(layoutB.contrib)

      const result = await engineB.pull(provider, lastSynced, ['prefs', 'layout'])

      // No conflicts — different contributors and different fields
      expect(result.conflicts).toHaveLength(0)
      expect(result.appliedBundle).toBe(bundleA)

      // prefs: B gets A's theme change
      expect(prefsB.getData()).toEqual({ theme: 'dark' })
      // layout: B keeps its own sidebar change
      expect(layoutB.getData()).toEqual({ sidebar: true })
    })
  })
})
