import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import { listContributions } from '../settings-contribution-registry'
import { dispatchSettingsContributions } from '../dispatch-settings-contributions'
import { getModule, getModules } from '../module-registry'
import { isModuleOwnedContribution } from '../settings-contribution-types'
import { useModuleEnabledStore } from '../../stores/useModuleEnabledStore'
import { syncEngine } from '../sync/register-sync'
import { SETTINGS_ORDER } from '../settings-order'
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

  // Profile Sync P3d-3 (plan review #10): Settings › Profile REPLACES Settings › Sync, so the module's `settings`
  // contribution is gone. The module itself, its engine and its contributors stay until P4a deletes them —
  // nothing of the old Sync runs by itself (every entry point was a button on that page), so nothing is left
  // running that a user could no longer stop.
  it('2.3.c: the sync module contributes NO settings section — it is not in the sidebar', () => {
    expect(getModule('sync')?.settings ?? []).toEqual([])
    const purdex = listContributions('purdex')
    expect(purdex.find((c) => c.localId === 'sync')).toBeUndefined()
    expect(purdex.find((c) => c.moduleId === 'sync')).toBeUndefined()
    expect(purdex.some(isModuleOwnedContribution)).toBe(true) // the registry itself still works: other modules are there
  })

  it('2.3.d: the core band still has Profile, where sync lives now; MODULE_SYNC keeps its slot number for P4a to retire', () => {
    expect(listContributions('purdex').find((c) => c.localId === 'profile')).toBeDefined()
    expect(SETTINGS_ORDER.MODULE_SYNC).toBeGreaterThan(SETTINGS_ORDER.PROFILE)
  })

  it('2.3.e: a stale `useModuleEnabledStore` entry and a repeated dispatch (HMR-like) do not bring the section back', () => {
    useModuleEnabledStore.setState({ enabled: { sync: true }, baseline: null })
    dispatchSettingsContributions()
    dispatchSettingsContributions()
    expect(listContributions('purdex').find((c) => c.localId === 'sync')).toBeUndefined()
    expect(getModule('sync')).toBeDefined()
  })

  it('2.3.g: registerSyncContributors() still wires all 6 contributors at boot', () => {
    // resetAndRegisterBuiltinModules() calls registerBuiltinModules(), which
    // calls registerSyncContributors() before any registerModule(...) calls.
    // 6, not 7: the quick-commands contributor went away with the
    // quick-command system (spec §4.4).
    expect(syncEngine.getContributors().length).toBe(6)
  })

})
