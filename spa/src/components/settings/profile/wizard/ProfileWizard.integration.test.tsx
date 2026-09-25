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
import { useUndoToast } from '../../../../stores/useUndoToast'
import en from '../../../../locales/en.json'
import { useWorkspaceStore } from '../../../../features/workspace/store'
import { __resetMasterWorldForTest } from '../../../../lib/profile/master-world'
import { clearSectionStore } from '../../../../lib/profile/section-store'
import { __resetProfileSyncForTest, attachMaster, detachMaster, profileSyncState, startProfileSync } from '../../../../lib/profile/start'
import { FakeDaemon } from '../../../../lib/profile/test-fake-daemon'
import { hashSection } from '../../../../lib/profile/hash'
import { buildHostsSection } from '../../../../lib/profile/sections'

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
  useUndoToast.getState().dismiss()
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
    expect(daemon.live()).toEqual(['settings', 'tabs.wa', 'workspaces']) // never `hosts` (host ownership H3a-2)
    expect(JSON.stringify(daemon.rows.get('workspaces')!.payload)).toContain('SENTINEL-A')
    expect(profileSyncState().status?.profile).toBe('synced')
    expect(onScreen()).toEqual(['SENTINEL-A']) // a push replaces nothing here
    expect(Object.keys(useLocalProfilesStore.getState().slaves)).toEqual([]) // and keeps no copy
    // a push is said as a toast too, not only by the done line (P3d-3 "the result is a toast, too"; P3d-4c F9).
    // The name is the one the host LISTS (the fake daemon lists every profile as "p"), as the run re-lists it.
    expect(useUndoToast.getState().toast?.message).toBe(en['settings.profile.wizard.toast.done'].replace('{{profile}}', 'p').replace('{{host}}', 'mlab'))
  })

  it('EXISTING → PULL with the copy: this device takes the host\'s state, and what it held is a local profile named after it', async () => {
    // another day: this device pushed SENTINEL-A, stopped, and has worked on as SENTINEL-B since
    exists = true
    // a pull needs the host verified this session (host-sync-identity §8)
    useHostStore.setState({ hosts: { [M]: { ...useHostStore.getState().hosts[M], daemonId: 'mlab:278cbm' } }, runtime: { [M]: { status: 'connected', daemonIdVerified: { endpoint: '10.0.0.1:7860', daemonId: 'mlab:278cbm' } } } })
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
    // a pull lists no host (host ownership H3b): nothing is removed here
    expect(screen.queryByTestId('profile-wizard-pull-removes')).toBeNull()
    expect(screen.queryByTestId('profile-wizard-pull-refused')).toBeNull()
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
    // said where a replaced world cannot take it away (acceptance F6)
    expect(useUndoToast.getState().toast?.message).toContain('“Laptop”')
    // the host's copy was read, not written: SENTINEL-B never reached it
    expect(daemon.writes.slice(writes).filter((w) => w.outcome === 'applied')).toEqual([])
    expect(JSON.stringify([...daemon.rows.values()].map((r) => r.payload))).not.toContain('SENTINEL-B')
  })

  // Host ownership H3b + H3a-2 (spec §5.3, "the wizard's attach"): a whole wizard run — its door, its ask before the
  // attach, AND the sync the attach starts, through its first reconciliation — never names `hosts`: no GET, PUT or
  // DELETE of it. The legacy row an older client left on the SOT stays byte for byte.
  it.each(['push', 'pull'] as const)('THE WHOLE RUN NEVER NAMES `hosts` — %s: the wizard and the sync it starts; the legacy row untouched', async (direction) => {
    exists = true
    useHostStore.setState({ hosts: { [M]: { ...useHostStore.getState().hosts[M], daemonId: 'mlab:278cbm' } }, runtime: { [M]: { status: 'connected', daemonIdVerified: { endpoint: '10.0.0.1:7860', daemonId: 'mlab:278cbm' } } } })
    expect(await attachMaster(M, PROFILE, 'push')).toEqual({ ok: true })
    await settle()
    expect(await detachMaster()).toEqual({ ok: true })
    // the legacy `hosts` row an older client wrote (an H3 client never writes one: host ownership H3a-2)
    const legacy = { hosts: { [M]: { id: M, name: 'mlab', ip: '10.0.0.1', port: 7860, token: 'legacy-tok', order: 0 } }, hostOrder: [M] }
    daemon.rows.set('hosts', { rev: 1, hash: 'a'.repeat(64), payload: legacy, fingerprint: 'fp-hosts', ordinal: 1, writer: 'c_oooooooooooo' })
    const rowBefore = JSON.stringify(daemon.rows.get('hosts'))
    // An unchanged world pushed again needs no request at all (#1450: its empty `tabs.wa` is no longer taken for a
    // placeholder and pulled) — so the push has something to send, or "the sync did talk to the host" proves nothing.
    if (direction === 'push') useWorkspaceStore.setState({ workspaces: [workspace('wa', 'SENTINEL-B')], activeWorkspaceId: 'wa' })
    vi.clearAllMocks()
    api.listProfiles.mockImplementation(async () => daemon.list())
    api.getSection.mockImplementation(async (_h, _p, key) => daemon.get(key))
    api.putSection.mockImplementation(async (_h, _p, key, body) => daemon.put(key, body))
    api.deleteSection.mockImplementation(async (_h, _p, key, params) => daemon.delete(key, params))
    api.deleteAttachment.mockResolvedValue({ kind: 'ok', value: { detached: true } })
    let beforeAttach: string[] | null = null
    const named = (): string[] => [...api.getSection.mock.calls.map((c) => `GET ${c[2]}`), ...api.putSection.mock.calls.map((c) => `PUT ${c[2]}`), ...api.deleteSection.mock.calls.map((c) => `DELETE ${c[2]}`)]
    api.putAttachment.mockImplementation(async () => {
      beforeAttach = named()
      return { kind: 'ok', value: { attached: true } }
    })

    render(<ProfileWizard onClose={() => {}} />)
    await settle()
    await click(`profile-wizard-profile-${PROFILE}`)
    await click('profile-wizard-next')
    await click('profile-wizard-next')
    await click(`profile-wizard-direction-${direction}`)
    await click('profile-wizard-next')
    await click('profile-wizard-start')
    await settle()
    await settle()

    expect(screen.getByTestId('profile-wizard-done')).toBeInTheDocument()
    expect(selectMaster(useProfileStore.getState())).toEqual({ hostId: M, profileId: PROFILE })
    expect(useProfileStore.getState().pendingDirection).toBeNull() // the first reconciliation has run to its end
    expect(beforeAttach).not.toBeNull()
    expect(beforeAttach!.filter((c) => c.endsWith(' hosts'))).toEqual([]) // the wizard's own requests
    expect(named().filter((c) => c.endsWith(' hosts'))).toEqual([]) // and the sync's, after the attach
    expect(api.getSection.mock.calls.length + api.putSection.mock.calls.length).toBeGreaterThan(0) // the sync did talk to the host
    expect(JSON.stringify(daemon.rows.get('hosts'))).toBe(rowBefore)
  })

  // Host ownership H3 (spec decision 1, §5.1): the host list is this device's. A pull through the wizard — the sync
  // it starts included — removes no local host, although the SOT's legacy `hosts` row does not list one of them.
  it('A PULL KEEPS EVERY LOCAL HOST: the SOT\'s legacy `hosts` row lists mlab only, this device also has air — after the run, both, unchanged', async () => {
    exists = true
    useHostStore.setState({ hosts: { [M]: { ...useHostStore.getState().hosts[M], daemonId: 'mlab:278cbm' } }, runtime: { [M]: { status: 'connected', daemonIdVerified: { endpoint: '10.0.0.1:7860', daemonId: 'mlab:278cbm' } } } })
    expect(await attachMaster(M, PROFILE, 'push')).toEqual({ ok: true })
    await settle()
    expect(await detachMaster()).toEqual({ ok: true })
    // what a pre-H3 client pushed: a well-formed `hosts` payload under its real hash — one an older build WOULD apply
    const legacy = buildHostsSection({ hosts: { [M]: { id: M, name: 'mlab', ip: '10.0.0.1', port: 7860, token: 'tok', order: 0, daemonId: 'mlab:278cbm' } }, hostOrder: [M] }) as unknown as Record<string, unknown>
    daemon.rows.set('hosts', { rev: 1, hash: await hashSection(legacy), payload: legacy, fingerprint: 'fp-hosts', ordinal: 1, writer: 'c_oooooooooooo' })
    useHostStore.setState({ hosts: { ...useHostStore.getState().hosts, 'host-air': { id: 'host-air', name: 'air', ip: '10.0.0.4', port: 7860, token: 'tok-air', order: 1 } }, hostOrder: [M, 'host-air'] })
    const hostsBefore = JSON.stringify([useHostStore.getState().hosts, useHostStore.getState().hostOrder])

    render(<ProfileWizard onClose={() => {}} />)
    await settle()
    await click(`profile-wizard-profile-${PROFILE}`)
    await click('profile-wizard-next')
    await click('profile-wizard-next')
    await click('profile-wizard-direction-pull')
    expect(screen.queryByTestId('profile-wizard-pull-removes')).toBeNull() // nothing is announced as removed…
    await click('profile-wizard-next')
    await click('profile-wizard-start')
    await settle()
    await settle()

    // …and nothing is: both hosts, as they were
    expect(JSON.stringify([useHostStore.getState().hosts, useHostStore.getState().hostOrder])).toBe(hostsBefore)
    expect(screen.getByTestId('profile-wizard-done')).toBeInTheDocument()
    expect(useProfileStore.getState().pendingDirection).toBeNull()
    // and air reached no section of the SOT: its address and token are this device's alone
    expect(JSON.stringify([...daemon.rows.values()].map((r) => r.payload))).not.toContain('10.0.0.4')
    expect(JSON.stringify([...daemon.rows.values()].map((r) => r.payload))).not.toContain('tok-air')
  })

  it('REVIEW F1 — seen EMPTY, and another device pushes its world into it before Start: NOTHING of this device reaches the host; the user is sent back to choose', async () => {
    exists = true // an existing profile that holds nothing: push only, and no "replaces what is there" warning
    render(<ProfileWizard onClose={() => {}} />)
    await settle()
    await click(`profile-wizard-profile-${PROFILE}`)
    await click('profile-wizard-next')
    await click('profile-wizard-next')
    expect(screen.getByTestId('profile-wizard-direction-pull')).toBeDisabled()
    expect(screen.queryByTestId('profile-wizard-push-warning')).toBeNull()
    await click('profile-wizard-next')

    // the other device, meanwhile
    const theirs = { clientId: 'c_bbbbbbbbbbbb', baseRev: 0, hash: 'e'.repeat(64), fingerprint: 'fp-workspaces', ordinal: 1, payload: { workspaces: [{ id: 'wz', name: 'SENTINEL-THEIRS' }] } }
    expect(daemon.put('workspaces', theirs)).toMatchObject({ kind: 'applied' })
    const writes = daemon.writes.length

    await click('profile-wizard-start')
    await settle()
    expect(api.putAttachment).not.toHaveBeenCalled()
    expect(selectMaster(useProfileStore.getState())).toBeNull()
    expect(daemon.writes.length).toBe(writes)
    expect(JSON.stringify(daemon.rows.get('workspaces')!.payload)).toContain('SENTINEL-THEIRS')
    expect(step()).toBe('direction')
    expect(screen.getByTestId('profile-wizard-notice')).toHaveAttribute('data-reason', 'profile-changed')
    expect(screen.getByTestId('profile-wizard-direction-pull')).not.toBeDisabled()
    await click('profile-wizard-direction-push')
    expect(screen.getByTestId('profile-wizard-push-warning')).toBeInTheDocument()
  })
})
