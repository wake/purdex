import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import { getContribution, listContributions, registerSettingsContribution } from '../settings-contribution-registry'
import { useSyncStore } from '../sync/use-sync-store'
import { getModule, getModules } from '../module-registry'
import { isModuleOwnedContribution } from '../settings-contribution-types'
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

  // Profile Sync P3d-3 (plan review #10; review F4): Settings › Profile REPLACES Settings › Sync — for everybody
  // who is not in the middle of using the old one. The section is still DECLARED, with a `visible()`: listed
  // only while the old Sync holds something only that page can deal with (pending conflicts, a pending remote
  // bundle) or is switched on (a provider other than off: that user is using it, and the page is where it is
  // switched off). Nothing of the old Sync runs by itself, so for everyone else there is nothing to reach.
  const conflict = { contributor: 'prefs', field: 'theme', lastSynced: 'x', local: 'y', remote: { value: 'z', device: 'A' } }
  const bundle = { version: 1, timestamp: 5000, device: 'A', collections: {} }
  const syncEntry = () => listContributions('purdex').find((c) => c.localId === 'sync')

  it('2.3.c: a user with no old-Sync state (nearly everyone) has NO Sync entry', () => {
    useSyncStore.getState().reset()
    expect(syncEntry()).toBeUndefined()
    expect(listContributions('purdex').find((c) => c.moduleId === 'sync')).toBeUndefined()
    expect(listContributions('purdex').find((c) => c.localId === 'profile')).toBeDefined()
  })

  it('2.3.d: PENDING CONFLICTS → the entry is there (module-owned, in its old slot): it is the only place they can be resolved or dismissed', () => {
    useSyncStore.getState().reset()
    useSyncStore.getState().setPendingConflicts([conflict], bundle)
    const entry = syncEntry()
    expect(entry).toBeDefined()
    expect(entry!.moduleId).toBe('sync')
    expect(isModuleOwnedContribution(entry!)).toBe(true)
    expect(entry!.order).toBe(SETTINGS_ORDER.MODULE_SYNC)
    useSyncStore.getState().clearPendingConflicts()
    expect(syncEntry()).toBeUndefined() // dealt with: gone again, without a new dispatch
  })

  it('2.3.e: a pending remote bundle alone, or a provider that is on, keeps it too', () => {
    useSyncStore.getState().reset()
    useSyncStore.setState({ pendingRemoteBundle: bundle as never })
    expect(syncEntry()).toBeDefined()
    useSyncStore.getState().reset()
    useSyncStore.getState().setActiveProvider('daemon')
    expect(syncEntry()).toBeDefined()
    useSyncStore.getState().setActiveProvider(null)
    expect(syncEntry()).toBeUndefined()
  })

  it('2.3.f: wherever the title bar\'s conflict icon shows, the page it leads to is listed', () => {
    useSyncStore.getState().reset()
    useSyncStore.getState().setActiveProvider('file')
    useSyncStore.getState().setPendingConflicts([conflict], bundle)
    const s = useSyncStore.getState()
    expect(s.activeProviderId !== null && s.pendingConflicts.length > 0 && s.pendingRemoteBundle !== null && s.pendingConflictsAt !== null).toBe(true)
    expect(syncEntry()).toBeDefined()
  })

  it('2.3.i: the section says WHEN to look again (round 2): its three conditions, and nothing else of the store', () => {
    useSyncStore.getState().reset()
    const entry = getContribution('sync.sync')!
    let asked = 0
    const stop = entry.subscribeVisibility!(() => { asked += 1 })
    useSyncStore.getState().setSyncHostId('h1') // not a condition
    expect(asked).toBe(0)
    useSyncStore.getState().setPendingConflicts([conflict], bundle) // what another window's write looks like here after a rehydrate
    expect(asked).toBeGreaterThan(0)
    const afterConflicts = asked
    useSyncStore.getState().setActiveProvider('daemon')
    expect(asked).toBeGreaterThan(afterConflicts)
    stop()
    const afterStop = asked
    useSyncStore.getState().reset()
    expect(asked).toBe(afterStop)
  })

  it('2.3.h: `visible()` is the registry\'s, for any contribution: false or throwing → not listed; absent → listed; `getContribution` still finds it', () => {
    const base = { scope: 'purdex' as const, order: 900, labelKey: 'x', component: () => null, moduleId: 'm' }
    registerSettingsContribution({ ...base, localId: 'shown', id: 'm.shown' })
    registerSettingsContribution({ ...base, localId: 'hidden', id: 'm.hidden', visible: () => false })
    registerSettingsContribution({ ...base, localId: 'broken', id: 'm.broken', visible: () => { throw new Error('x') } })
    const ids = listContributions('purdex').map((c) => c.localId)
    expect(ids).toContain('shown')
    expect(ids).not.toContain('hidden')
    expect(ids).not.toContain('broken')
    expect(getContribution('m.hidden')).toBeDefined()
  })

  it('2.3.g: registerSyncContributors() still wires all 6 contributors at boot', () => {
    // resetAndRegisterBuiltinModules() calls registerBuiltinModules(), which
    // calls registerSyncContributors() before any registerModule(...) calls.
    // 6, not 7: the quick-commands contributor went away with the
    // quick-command system (spec §4.4).
    expect(syncEngine.getContributors().length).toBe(6)
  })

})
