// spa/src/components/settings/profile/wizard/ProfileWizard.integration.test.tsx — the wizard end to end, driven
// through its controls: the real start layer, lease, collector, executor, switch-active.ts and zustand stores;
// only the network (`lib/profile/api`, served by the in-memory daemon), the digest and `shapeTable` are
// replaced (as lib/profile/start.integration.test.ts). Two runs: new → push, and existing → pull with the copy.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { act, cleanup, fireEvent, render, screen } from '@testing-library/react'
import { ProfileWizard } from './ProfileWizard'
import { useHostStore } from '../../../../stores/useHostStore'
import { useDeviceNameStore } from '../../../../stores/useDeviceNameStore'
import { MASTER_PROFILE_ID, useLocalProfilesStore } from '../../../../stores/useLocalProfilesStore'
import { selectMaster, useProfileStore } from '../../../../stores/useProfileStore'
import { useRebuildStore } from '../../../../stores/useRebuildStore'
import { useTabStore } from '../../../../stores/useTabStore'
import { useWorkspaceStore } from '../../../../features/workspace/store'
import { __resetMasterWorldForTest } from '../../../../lib/profile/master-world'
import { clearSectionStore } from '../../../../lib/profile/section-store'
import { __resetProfileSyncForTest, attachMaster, detachMaster, profileSyncState, startProfileSync } from '../../../../lib/profile/start'
import { FakeDaemon } from '../../../../lib/profile/test-fake-daemon'

vi.mock('../../../../lib/profile/hash', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../../../../lib/profile/hash')>()
  // FNV-1a under eight seeds: deterministic, synchronous, 64 lower-case hex.
  const hex64 = (text: string): string => {
    let out = ''
    for (let seed = 0; seed < 8; seed += 1) {
      let x = (0x811c9dc5 ^ Math.imul(seed + 1, 0x9e3779b1)) >>> 0
      for (let i = 0; i < text.length; i += 1) x = Math.imul(x ^ text.charCodeAt(i), 0x01000193) >>> 0
      out += x.toString(16).padStart(8, '0')
    }
    return out
  }
  return { ...actual, hashSection: vi.fn(async (payload: unknown) => hex64(actual.structuralKey(payload))) }
})

vi.mock('../../../../lib/profile/api', () => ({
  listProfiles: vi.fn(),
  createProfile: vi.fn(),
  getSection: vi.fn(),
  putSection: vi.fn(),
  deleteSection: vi.fn(),
  putAttachment: vi.fn(),
  deleteAttachment: vi.fn(),
}))

vi.mock('../../../../lib/profile/projections', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../../../../lib/profile/projections')>()
  return { ...actual, shapeTable: vi.fn(async () => ({ hosts: ['fp-hosts', 1], settings: ['fp-settings', 3], workspaces: ['fp-workspaces', 1], tabs: ['fp-tabs', 1] })) }
})

vi.mock('../../../../lib/client-identity', () => ({ getClientId: () => 'c_aaaaaaaaaaaa', isClientIdPersisted: () => true }))

const api = vi.mocked(await import('../../../../lib/profile/api'))

const M = 'host-master'
const PROFILE = 'p_0123456789ab'

let daemon: FakeDaemon
/** Whether the daemon has the profile yet: a create makes it. */
let exists = false
let stop: () => void = () => {}

const settle = async (): Promise<void> => {
  await act(async () => {
    for (let i = 0; i < 6; i += 1) await vi.advanceTimersByTimeAsync(1_000)
  })
}
const click = async (id: string): Promise<void> => {
  fireEvent.click(screen.getByTestId(id))
  await act(async () => {
    await vi.advanceTimersByTimeAsync(10)
  })
}
const step = (): string | null => screen.getByTestId('profile-wizard').getAttribute('data-step')
const workspace = (id: string, name: string) => ({ id, name, tabs: [], activeTabId: null })
const onScreen = (): string[] => useWorkspaceStore.getState().workspaces.map((w) => w.name)

beforeEach(() => {
  vi.useFakeTimers()
  localStorage.clear()
  __resetProfileSyncForTest()
  __resetMasterWorldForTest()
  daemon = new FakeDaemon(PROFILE)
  exists = false
  vi.clearAllMocks()
  api.listProfiles.mockImplementation(async () => (exists ? daemon.list() : { kind: 'ok', value: [] }))
  api.createProfile.mockImplementation(async (_h, name) => {
    exists = true
    return { kind: 'ok', value: { id: PROFILE, name, createdAt: 1, updatedAt: 1 } }
  })
  api.getSection.mockImplementation(async (_h, _p, key) => daemon.get(key))
  api.putSection.mockImplementation(async (_h, _p, key, body) => daemon.put(key, body))
  api.deleteSection.mockImplementation(async (_h, _p, key, params) => daemon.delete(key, params))
  api.putAttachment.mockResolvedValue({ kind: 'ok', value: { attached: true } })
  api.deleteAttachment.mockResolvedValue({ kind: 'ok', value: { detached: true } })
  useProfileStore.setState({ masterHostId: null, masterProfileId: null, autoSync: true, pendingDirection: null, attachGeneration: 0, masterEndpoint: null, suspension: null, pendingDetaches: [] })
  useHostStore.setState({ hosts: { [M]: { id: M, name: 'mlab', ip: '10.0.0.1', port: 7860, token: 'tok', order: 0 } }, hostOrder: [M], activeHostId: M, devHostId: M, runtime: { [M]: { status: 'connected' } } })
  useDeviceNameStore.setState({ deviceName: 'Laptop' })
  useLocalProfilesStore.setState({ slaves: {}, slaveOrder: [], activeProfileId: MASTER_PROFILE_ID, parkedMaster: null, worldEpoch: 0, relabelCount: 0, master: { name: null } })
  useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null, visitHistory: [], worldId: MASTER_PROFILE_ID, worldEpoch: 0 })
  useWorkspaceStore.setState({ workspaces: [workspace('wa', 'SENTINEL-A')], activeWorkspaceId: 'wa', worldId: MASTER_PROFILE_ID, worldEpoch: 0 })
  useRebuildStore.setState({ operations: {}, lockedBy: null, lockGrant: null })
  vi.spyOn(console, 'warn').mockImplementation(() => {})
  stop = startProfileSync()
})

afterEach(() => {
  cleanup()
  stop()
  stop = () => {}
  clearSectionStore()
  __resetMasterWorldForTest()
  useLocalProfilesStore.setState({ slaves: {}, slaveOrder: [], activeProfileId: MASTER_PROFILE_ID, parkedMaster: null, worldEpoch: 0, relabelCount: 0, master: { name: null } })
  useWorkspaceStore.setState({ workspaces: [], activeWorkspaceId: null, worldId: MASTER_PROFILE_ID, worldEpoch: 0 })
  vi.restoreAllMocks()
  vi.useRealTimers()
})

describe('the wizard, through its controls, against a daemon', () => {
  it('NEW → PUSH: the profile is created, pull is never offered, and this device\'s state reaches the host', async () => {
    render(<ProfileWizard onClose={() => {}} />)
    await settle()
    expect(step()).toBe('sot')
    await click('profile-wizard-profile-new')
    fireEvent.change(screen.getByTestId('profile-wizard-new-name'), { target: { value: 'default' } })
    await click('profile-wizard-next')
    expect(api.createProfile).toHaveBeenCalledWith(M, 'default')
    expect(step()).toBe('local')
    await click('profile-wizard-next')
    expect(screen.getByTestId('profile-wizard-direction-pull')).toBeDisabled()
    await click('profile-wizard-next')
    expect(daemon.writes).toEqual([]) // nothing has left this device before Start
    await click('profile-wizard-start')
    await settle()

    expect(screen.getByTestId('profile-wizard-done')).toBeInTheDocument()
    expect(selectMaster(useProfileStore.getState())).toEqual({ hostId: M, profileId: PROFILE })
    expect(daemon.live()).toEqual(['hosts', 'settings', 'tabs.wa', 'workspaces'])
    expect(JSON.stringify(daemon.rows.get('workspaces')!.payload)).toContain('SENTINEL-A')
    expect(profileSyncState().status?.profile).toBe('synced')
    expect(onScreen()).toEqual(['SENTINEL-A']) // a push replaces nothing here
    expect(Object.keys(useLocalProfilesStore.getState().slaves)).toEqual([]) // and keeps no copy
  })

  it('EXISTING → PULL with the copy: this device takes the host\'s state, and what it held is a local profile named after it', async () => {
    // another day: this device pushed SENTINEL-A, stopped, and has worked on as SENTINEL-B since
    exists = true
    expect(await attachMaster(M, PROFILE, 'push')).toEqual({ ok: true })
    await settle()
    expect(await detachMaster()).toEqual({ ok: true })
    useWorkspaceStore.setState({ workspaces: [workspace('wb', 'SENTINEL-B')], activeWorkspaceId: 'wb' })
    await settle()
    const writes = daemon.writes.length

    render(<ProfileWizard onClose={() => {}} />)
    await settle()
    await click(`profile-wizard-profile-${PROFILE}`)
    await click('profile-wizard-next')
    await click('profile-wizard-next')
    await click('profile-wizard-direction-pull')
    expect((screen.getByTestId('profile-wizard-save-first') as HTMLInputElement).checked).toBe(true)
    expect((screen.getByTestId('profile-wizard-save-name') as HTMLInputElement).value).toBe('Laptop')
    await click('profile-wizard-next')
    expect(onScreen()).toEqual(['SENTINEL-B']) // nothing yet
    await click('profile-wizard-start')
    await settle()

    expect(screen.getByTestId('profile-wizard-done')).toBeInTheDocument()
    expect(onScreen()).toEqual(['SENTINEL-A'])
    const kept = Object.values(useLocalProfilesStore.getState().slaves)
    expect(kept.map((s) => s.name)).toEqual(['Laptop'])
    expect(kept[0].world?.workspaces.map((w) => w.name)).toEqual(['SENTINEL-B'])
    // the host's copy was read, not written: SENTINEL-B never reached it
    expect(daemon.writes.slice(writes).filter((w) => w.outcome === 'applied')).toEqual([])
    expect(JSON.stringify([...daemon.rows.values()].map((r) => r.payload))).not.toContain('SENTINEL-B')
  })
})
