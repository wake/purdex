import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import { listContributions } from '../settings-contribution-registry'
import { dispatchSettingsContributions } from '../dispatch-settings-contributions'
import { getModule, getModules } from '../module-registry'
import { isModuleOwnedContribution } from '../settings-contribution-types'
import { useModuleEnabledStore } from '../../stores/useModuleEnabledStore'
import { syncEngine } from '../sync/register-sync'
import { SETTINGS_ORDER_PR2_FUTURE } from '../settings-order'
import {
  clearAllBuiltinModuleRegistries,
  resetAndRegisterBuiltinModules,
  resetModuleEnabledStore,
} from './test-bootstrap-harness'

// Spec §4.3 — PR-2 promotes Sync from a built-in `registerSettingsSection`
// to a real (structural) module. The module is **not** marked
// `disableable`: turning Sync off is a future spec item that involves
// engine + contributor wiring.

beforeEach(() => {
  resetAndRegisterBuiltinModules()
})

afterEach(() => {
  clearAllBuiltinModuleRegistries()
  resetModuleEnabledStore()
})

describe('Sync modularize (spec §4.3)', () => {
  it('2.3.a: registerBuiltinModules() registers a `sync` module', () => {
    const sync = getModule('sync')
    expect(sync).toBeDefined()
    expect(sync?.name).toBe('Sync')
  })

  it('2.3.b: sync is NOT in the disableable subset (does not appear in Modules switchboard)', () => {
    const switchboardModules = getModules().filter((m) => m.disableable === true)
    const ids = switchboardModules.map((m) => m.id)
    expect(ids).not.toContain('sync')
  })

  it('2.3.c: sync contribution is module-owned (moduleId === "sync")', () => {
    const purdex = listContributions('purdex')
    const syncContrib = purdex.find((c) => c.localId === 'sync')
    expect(syncContrib).toBeDefined()
    expect(syncContrib!.moduleId).toBe('sync')
    // Module-owned predicate flips on (puzzle icon shows in sidebar).
    expect(isModuleOwnedContribution(syncContrib!)).toBe(true)
  })

  it('2.3.d: sync contribution carries the SETTINGS_ORDER MODULE_SYNC value (=14)', () => {
    const purdex = listContributions('purdex')
    const syncContrib = purdex.find((c) => c.localId === 'sync')
    expect(syncContrib).toBeDefined()
    // Until commit 5 swaps Performance Monitor / Editor / Quick Commands /
    // Sync to the final SETTINGS_ORDER constants, the sync entry should at
    // minimum match the canonical PR-2 final value defined in
    // SETTINGS_ORDER_PR2_FUTURE.MODULE_SYNC.
    expect(syncContrib!.order).toBe(SETTINGS_ORDER_PR2_FUTURE.MODULE_SYNC)
  })

  it('2.3.e: stale `useModuleEnabledStore` entry { sync: false } does NOT drop the contribution', () => {
    // Clear any prior state then write a stale persisted-style entry as if a
    // future PR turned `disableable` on, the user disabled sync, then PR-2
    // reverted disableable. The dispatch filter only consults override when
    // the module IS disableable; a `disableable: undefined` module always
    // resolves to enabled.
    useModuleEnabledStore.setState({ enabled: { sync: false }, baseline: null })
    dispatchSettingsContributions()
    const purdex = listContributions('purdex')
    expect(purdex.find((c) => c.localId === 'sync')).toBeDefined()
  })

  it('2.3.f: repeated dispatch (HMR-like) keeps sync module-owned, no stale legacy moduleId', () => {
    dispatchSettingsContributions()
    dispatchSettingsContributions()
    const purdex = listContributions('purdex')
    const syncContrib = purdex.find((c) => c.localId === 'sync')
    expect(syncContrib).toBeDefined()
    expect(syncContrib!.moduleId).toBe('sync')
    // Negative: the previous built-in registration moduleId must NOT come
    // back through any code path.
    expect(
      purdex.find(
        (c) =>
          c.localId === 'sync' &&
          c.moduleId === '_builtin.legacy-section',
      ),
    ).toBeUndefined()
  })

  it('2.3.g: registerSyncContributors() still wires all 7 contributors at boot', () => {
    // resetAndRegisterBuiltinModules() calls registerBuiltinModules(), which
    // calls registerSyncContributors() before any registerModule(...) calls.
    expect(syncEngine.getContributors().length).toBe(7)
  })

})
