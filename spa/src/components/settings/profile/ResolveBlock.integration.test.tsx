// spa/src/components/settings/profile/ResolveBlock.integration.test.tsx — the Resolve rows END TO END (P3d-4 plan,
// R7): the Current block's buttons → `requestResolve` → the status channel → the executor and its REAL reducer, the
// collector, the section store, apply-to-stores and the stores. Only the network (`./api`, served by the in-memory
// daemon), the digest and `shapeTable` are replaced.
//
// The reducer's edges, each pinned through the UI:
//   - reset → both directions;
//   - invalid → no "Take the host's", and a forged `sot` command changes nothing;
//   - a pairless lock whose host rev moved on → the open confirmation closes itself, and its stale command is dropped;
//   - a conflict edited here since → "Keep this device's" pushes the SENT snapshot, and the confirmation said so.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { act, cleanup, fireEvent, render, screen } from '@testing-library/react'
import en from '../../../locales/en.json'
import { useHostStore } from '../../../stores/useHostStore'
import type { HostConfig } from '../../../stores/useHostStore'
import { MASTER_PROFILE_ID, useLocalProfilesStore } from '../../../stores/useLocalProfilesStore'
import { useProfileStore } from '../../../stores/useProfileStore'
import { useRebuildStore } from '../../../stores/useRebuildStore'
import { useTabStore } from '../../../stores/useTabStore'
import { useWorkspaceStore } from '../../../features/workspace/store'
import type { SectionLock } from '../../../lib/profile/executor'
import { hashSection } from '../../../lib/profile/hash'
import { __resetMasterWorldForTest } from '../../../lib/profile/master-world'
import { clearSectionStore } from '../../../lib/profile/section-store'
import { __resetProfileSyncForTest, attachMaster, profileSyncState, requestResolve, startProfileSync } from '../../../lib/profile/start'
import { masterTagOf } from '../../../lib/profile/sync-status'
import { FakeDaemon } from '../../../lib/profile/test-fake-daemon'
import type { HostsPayload } from '../../../lib/profile/types'
import { CurrentBlock } from './CurrentBlock'

vi.mock('../../../lib/profile/hash', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../../../lib/profile/hash')>()
  // FNV-1a under eight seeds: deterministic, synchronous, 64 lower-case hex (as start.integration.test.ts).
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

vi.mock('../../../lib/profile/api', () => ({
  listProfiles: vi.fn(),
  getSection: vi.fn(),
  putSection: vi.fn(),
  deleteSection: vi.fn(),
  putAttachment: vi.fn(),
  deleteAttachment: vi.fn(),
}))

vi.mock('../../../lib/profile/projections', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../../../lib/profile/projections')>()
  return { ...actual, shapeTable: vi.fn(async () => ({ hosts: ['fp-hosts', 1], settings: ['fp-settings', 3], workspaces: ['fp-workspaces', 1], tabs: ['fp-tabs', 1] })) }
})

vi.mock('../../../lib/client-identity', () => ({ getClientId: () => 'c_aaaaaaaaaaaa', isClientIdPersisted: () => true }))

const api = vi.mocked(await import('../../../lib/profile/api'))

const M = 'host-master'
const H2 = 'host-two'
const PROFILE = 'p_0123456789ab'
const OTHER = 'c_bbbbbbbbbbbb'

function host(id: string, over: Partial<HostConfig> = {}): HostConfig {
  return { id, name: id, ip: '10.0.0.1', port: 7860, token: 'tok', order: 0, ...over }
}

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

function renameH2(name: string): void {
  const { hosts } = useHostStore.getState()
  useHostStore.setState({ hosts: { ...hosts, [H2]: { ...hosts[H2], name } } })
}
const h2Name = (): string => useHostStore.getState().hosts[H2].name
const hostsLock = (): SectionLock | undefined => profileSyncState().status?.locks.hosts
const tagNow = (): string => masterTagOf({ hostId: M, profileId: PROFILE }, useProfileStore.getState().attachGeneration)

/** The master host goes away and comes back: the executor reindexes (start.ts: `onReconnected`). */
async function reconnect(): Promise<void> {
  act(() => useHostStore.getState().setRuntime(M, { status: 'disconnected' }))
  await settle()
  act(() => useHostStore.getState().setRuntime(M, { status: 'connected' }))
  await settle()
}

/** Another client writes `hosts` on the daemon: this payload, at this rev (a rev BELOW ours = the section was recreated). */
async function hostWrites(rev: number, mutate: (p: HostsPayload) => void): Promise<string> {
  const row = daemon.rows.get('hosts')!
  const payload = JSON.parse(JSON.stringify(row.payload)) as HostsPayload
  mutate(payload)
  const hash = await hashSection(payload)
  daemon.rows.set('hosts', { ...row, rev, hash, payload: payload as unknown as Record<string, unknown>, writer: OTHER })
  return hash
}

let daemon: FakeDaemon
let stop: () => void = () => {}

beforeEach(() => {
  vi.useFakeTimers()
  localStorage.clear()
  __resetProfileSyncForTest()
  __resetMasterWorldForTest()
  daemon = new FakeDaemon(PROFILE)
  vi.clearAllMocks()
  api.listProfiles.mockImplementation(async () => daemon.list())
  api.getSection.mockImplementation(async (_h, _p, key) => daemon.get(key))
  api.putSection.mockImplementation(async (_h, _p, key, body) => daemon.put(key, body))
  api.deleteSection.mockImplementation(async (_h, _p, key, params) => daemon.delete(key, params))
  api.putAttachment.mockResolvedValue({ kind: 'ok', value: { attached: true } })
  api.deleteAttachment.mockResolvedValue({ kind: 'ok', value: { detached: true } })
  useProfileStore.setState({ masterHostId: null, masterProfileId: null, autoSync: true, pendingDirection: null, attachGeneration: 0, masterEndpoint: null, suspension: null, pendingDetaches: [] })
  useHostStore.setState({ hosts: { [M]: host(M), [H2]: host(H2, { ip: '10.0.0.2', order: 1 }) }, hostOrder: [M, H2], activeHostId: M, runtime: { [M]: { status: 'connected' } } })
  useLocalProfilesStore.setState({ slaves: {}, slaveOrder: [], activeProfileId: MASTER_PROFILE_ID, parkedMaster: null, worldEpoch: 0, relabelCount: 0, master: { name: null } })
  useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null, visitHistory: [], worldId: MASTER_PROFILE_ID, worldEpoch: 0 })
  useWorkspaceStore.setState({ workspaces: [], activeWorkspaceId: null, worldId: MASTER_PROFILE_ID, worldEpoch: 0 })
  useRebuildStore.setState({ operations: {}, lockedBy: null, lockGrant: null })
  vi.spyOn(console, 'warn').mockImplementation(() => {})
})

afterEach(() => {
  cleanup()
  stop()
  stop = () => {}
  clearSectionStore()
  __resetMasterWorldForTest()
  vi.restoreAllMocks()
  vi.useRealTimers()
})

/** Attached with `push`, the first reconciliation over, `hosts` agreed at `hostsRev` (each extra rev is an edit pushed). */
async function attachedAt(hostsRev: number): Promise<void> {
  stop = startProfileSync()
  expect(await attachMaster(M, PROFILE, 'push')).toEqual({ ok: true })
  await settle()
  for (let i = 1; i < hostsRev; i += 1) {
    act(() => renameH2(`edit-${i}`))
    await settle()
  }
  expect(daemon.rows.get('hosts')!.rev).toBe(hostsRev)
  expect(profileSyncState().status?.profile).toBe('synced')
  render(<CurrentBlock masterName="default" />)
}

describe('Resolve, end to end (R7)', () => {
  it('locked:reset → "Keep this device\'s": this device\'s copy goes back to the host, over the recreated one', async () => {
    await attachedAt(2)
    const mine = h2Name()
    await hostWrites(1, (p) => void (p.hosts[H2].name = 'recreated'))
    await reconnect()
    expect(screen.getByTestId('profile-resolve-row-hosts')).toHaveAttribute('data-lock', 'locked:reset')
    expect(screen.getByTestId('profile-resolve-why-hosts')).toHaveTextContent(en['settings.profile.resolve.why.reset'])

    await click('profile-resolve-keep-local-hosts')
    // this device: what is built now (2 hosts); the host: one read-only GET of its copy (2 hosts), at the lock's rev
    expect(screen.getByTestId('profile-resolve-count-local')).toHaveAttribute('data-state', 'read')
    expect(screen.getByTestId('profile-resolve-count-local')).toHaveTextContent('2')
    expect(screen.getByTestId('profile-resolve-count-sot')).toHaveAttribute('data-state', 'read')
    expect(screen.queryByTestId('profile-resolve-sot-moved')).toBeNull()
    await click('profile-resolve-confirm')
    await settle()

    expect(screen.queryByTestId('profile-resolve-row-hosts')).toBeNull()
    expect(profileSyncState().status?.sections.hosts).toBe('synced')
    expect(h2Name()).toBe(mine)
    const row = daemon.rows.get('hosts')!
    expect((row.payload as unknown as HostsPayload).hosts[H2].name).toBe(mine)
    expect(row.writer).toBe('c_aaaaaaaaaaaa')
  })

  it('locked:reset → "Take the host\'s": the recreated copy lands here', async () => {
    await attachedAt(2)
    await hostWrites(1, (p) => void (p.hosts[H2].name = 'recreated'))
    await reconnect()
    await click('profile-resolve-take-sot-hosts')
    expect(screen.getByTestId('profile-resolve-dialog')).toHaveTextContent(en['settings.profile.resolve.take_sot_body'])
    await click('profile-resolve-confirm')
    await settle()

    expect(screen.queryByTestId('profile-resolve-row-hosts')).toBeNull()
    expect(h2Name()).toBe('recreated')
    expect(profileSyncState().status?.sections.hosts).toBe('synced')
    expect(daemon.rows.get('hosts')!.writer).toBe(OTHER) // nothing was written over it
  })

  it('locked:invalid → no "Take the host\'s"; a forged `sot` command changes nothing; "Keep this device\'s" puts this device\'s copy back', async () => {
    await attachedAt(1)
    const mine = h2Name()
    await hostWrites(2, (p) => {
      delete p.hosts[M]
      p.hostOrder = p.hostOrder.filter((id) => id !== M)
    })
    await reconnect()
    expect(screen.getByTestId('profile-resolve-row-hosts')).toHaveAttribute('data-lock', 'locked:invalid')
    expect(screen.getByTestId('profile-resolve-why-hosts')).toHaveAttribute('data-reason', 'removes-master-host')
    expect(screen.getByTestId('profile-resolve-why-hosts')).toHaveTextContent(en['settings.profile.resolve.why.invalid.removes_master_host'])
    expect(screen.queryByTestId('profile-resolve-take-sot-hosts')).toBeNull()

    // the command no button sends, with the very lock on screen: the reducer refuses it
    const lock = hostsLock()!
    const reads = api.getSection.mock.calls.length
    const problems = profileSyncState().problems.length
    expect(requestResolve('hosts', 'sot', lock, tagNow())).toBe(true)
    await settle()
    expect(profileSyncState().status?.sections.hosts).toBe('locked:invalid')
    // not even asked for again: no pull of the refused copy, no new verdict
    expect(api.getSection.mock.calls.length).toBe(reads)
    expect(profileSyncState().problems.length).toBe(problems)
    expect(Object.keys(useHostStore.getState().hosts)).toContain(M)
    expect(daemon.rows.get('hosts')!.writer).toBe(OTHER)

    await click('profile-resolve-keep-local-hosts')
    await click('profile-resolve-confirm')
    await settle()
    expect(screen.queryByTestId('profile-resolve-row-hosts')).toBeNull()
    const row = daemon.rows.get('hosts')!
    expect(row.writer).toBe('c_aaaaaaaaaaaa')
    expect(Object.keys((row.payload as unknown as HostsPayload).hosts)).toContain(M)
    expect(h2Name()).toBe(mine)
  })

  it('a pairless lock whose host rev moves on while the confirmation is open: it closes itself, and the stale command is dropped', async () => {
    await attachedAt(3)
    await hostWrites(1, (p) => void (p.hosts[H2].name = 'recreated'))
    await reconnect()
    await click('profile-resolve-keep-local-hosts')
    const frozen = hostsLock()!
    expect(screen.getByTestId('profile-resolve-dialog')).toBeInTheDocument()

    // another client writes again — still below this device's rev: still `locked:reset`, another lock
    await hostWrites(2, (p) => void (p.hosts[H2].name = 'recreated-again'))
    await reconnect()
    expect(hostsLock()?.status).toBe('locked:reset')
    expect(hostsLock()?.sot.rev).toBe(2)
    expect(screen.queryByTestId('profile-resolve-dialog')).toBeNull()
    expect(screen.getByTestId('profile-resolve-changed-hosts')).toHaveTextContent(en['settings.profile.resolve.changed'])

    // what a late click would have sent: dropped by the channel's binding, nothing written
    const writes = daemon.writes.length
    expect(requestResolve('hosts', 'local', frozen, tagNow())).toBe(false) // dropped by the lock binding (review A2)
    await settle()
    expect(hostsLock()?.sot.rev).toBe(2)
    expect(daemon.writes.length).toBe(writes)
    expect(daemon.rows.get('hosts')!.writer).toBe(OTHER)
  })

  it('a conflict edited here since it arose: "Keep this device\'s" pushes the SENT snapshot — and the confirmation said so', async () => {
    await attachedAt(1)
    // another client moves `hosts` while this one edits it: the push is refused (409) and the sent snapshot kept
    await hostWrites(2, (p) => void (p.hosts[H2].name = 'theirs'))
    act(() => renameH2('sent-here'))
    await settle()
    expect(hostsLock()?.status).toBe('locked:conflict')
    // edited again, under the lock: a third host
    act(() => {
      const { hosts, hostOrder } = useHostStore.getState()
      useHostStore.setState({ hosts: { ...hosts, h3: host('h3', { ip: '10.0.0.3', order: 2 }) }, hostOrder: [...hostOrder, 'h3'] })
    })
    await settle()
    const lock = hostsLock()!
    expect(lock.currentHash).not.toBe(lock.conflict!.localHash)

    expect(screen.getByTestId('profile-resolve-undoes-hosts')).toHaveTextContent(en['settings.profile.resolve.undoes'])
    await click('profile-resolve-keep-local-hosts')
    expect(screen.getByTestId('profile-resolve-dialog-undoes')).toHaveTextContent(en['settings.profile.resolve.dialog_undoes'])
    // this device's side is the SENT snapshot, read from the stash: 2 hosts — not the 3 here now
    expect(screen.getByTestId('profile-resolve-count-local')).toHaveAttribute('data-state', 'read')
    expect(screen.getByTestId('profile-resolve-count-local')).toHaveTextContent(en['settings.profile.resolve.unit.hosts'].replace('{{count}}', '2'))
    await click('profile-resolve-confirm')
    await settle()

    expect(screen.queryByTestId('profile-resolve-row-hosts')).toBeNull()
    expect(h2Name()).toBe('sent-here')
    expect(Object.keys(useHostStore.getState().hosts)).toEqual([M, H2]) // the edit made since is undone, as said
    const row = daemon.rows.get('hosts')!
    expect((row.payload as unknown as HostsPayload).hosts[H2].name).toBe('sent-here')
    expect(Object.keys((row.payload as unknown as HostsPayload).hosts)).toEqual([M, H2])
    expect(row.writer).toBe('c_aaaaaaaaaaaa')
  })

  it('the same master is attached AGAIN while the confirmation is open (review A1): it closes itself; the old tag is refused, nothing written', async () => {
    await attachedAt(2)
    await hostWrites(1, (p) => void (p.hosts[H2].name = 'recreated'))
    await reconnect()
    await click('profile-resolve-keep-local-hosts')
    const frozen = hostsLock()!
    const shownUnder = tagNow()
    expect(await attachMaster(M, PROFILE, 'pull')).toEqual({ ok: true })
    expect(tagNow()).not.toBe(shownUnder)
    await act(async () => {})
    expect(screen.queryByTestId('profile-resolve-dialog')).toBeNull()
    const writes = daemon.writes.length
    expect(requestResolve('hosts', 'local', frozen, shownUnder)).toBe(false)
    await settle()
    expect(daemon.writes.length).toBe(writes)
  })
})
