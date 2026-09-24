// spa/src/lib/profile/executor.integration.test.ts — the executor wired to the
// REAL collector, the REAL section store, the REAL apply-to-stores and the real
// zustand stores. Only the network (`./api`), the digest (`./hash`, a
// synchronous stand-in that still yields 64 hex — the section store validates
// its hashes) and `shapeTable` (crypto.subtle) are replaced.
//
// The sample section is `settings` (one field of it: `purdex-ui-settings.keepAliveCount`). It was `hosts` until host
// ownership H3a-2 retired that one from the sync loop.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { useHostStore } from '../../stores/useHostStore'
import type { HostConfig } from '../../stores/useHostStore'
import { useTabStore } from '../../stores/useTabStore'
import { useWorkspaceStore } from '../../features/workspace/store'
import { useRebuildStore } from '../../stores/useRebuildStore'
import { useUISettingsStore } from '../../stores/useUISettingsStore'
import type { ProfileIndexEntry, PutOutcome, Result, SectionMeta } from './api'
import { buildSectionPayload, startCollector, type Collector } from './collector'
import { createExecutor, type Executor } from './executor'
import { hashSection } from './hash'
import { clearSectionStore, loadSectionStore } from './section-store'
import type { ProfileSectionKey, SettingsPayload } from './types'

vi.mock('./hash', async (importOriginal) => {
  const actual = await importOriginal<typeof import('./hash')>()
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

vi.mock('./api', () => ({ listProfiles: vi.fn(), getSection: vi.fn(), putSection: vi.fn(), deleteSection: vi.fn() }))

vi.mock('./projections', async (importOriginal) => {
  const actual = await importOriginal<typeof import('./projections')>()
  return { ...actual, shapeTable: vi.fn(async () => ({ hosts: ['fp-hosts', 1], settings: ['fp-settings', 3], workspaces: ['fp-workspaces', 1], tabs: ['fp-tabs', 1] })) }
})

const api = vi.mocked(await import('./api'))

const M = 'host-master'
const H2 = 'host-two'
const PROFILE = 'p_0123456789ab'
const OTHER_CLIENT = 'c_bbbbbbbbbbbb'

function host(id: string, over: Partial<HostConfig> = {}): HostConfig {
  return { id, name: id, ip: '10.0.0.1', port: 7860, token: 'tok', order: 0, ...over }
}

/** A local edit of `settings`. */
const edit = (keepAliveCount: number): void => {
  useUISettingsStore.setState({ keepAliveCount })
}
const keepAlive = (): number => useUISettingsStore.getState().keepAliveCount

/** `settings` as the collector builds it now. */
const mySettings = (): SettingsPayload => buildSectionPayload('settings')!.payload as SettingsPayload

/** The settings payload as ANOTHER client would have written it, with `keepAliveCount` at `n`. */
function theirSettings(n: number): SettingsPayload {
  const mine = JSON.parse(JSON.stringify(mySettings())) as SettingsPayload
  ;(mine['purdex-ui-settings'] as Record<string, unknown>).keepAliveCount = n
  return mine
}

function meta(section: string, rev: number, hash: string): SectionMeta {
  const shape: Record<string, [string, number]> = { hosts: ['fp-hosts', 1], settings: ['fp-settings', 3], workspaces: ['fp-workspaces', 1] }
  return { section, rev, hash, fingerprint: shape[section][0], ordinal: shape[section][1], writer: OTHER_CLIENT, updatedAt: 0 }
}

function index(sections: SectionMeta[]): Result<ProfileIndexEntry[]> {
  return { kind: 'ok', value: [{ id: PROFILE, name: 'p', createdAt: 0, updatedAt: 0, sections, attachments: [] }] }
}

const flush = (): Promise<unknown> => vi.advanceTimersByTimeAsync(0)
const debounce = (): Promise<unknown> => vi.advanceTimersByTimeAsync(600)

let reachable = true
let executor: Executor
let collector: Collector
const problems: Array<{ kind: string; section?: string; detail: string }> = []

function start(): void {
  executor = createExecutor({
    hostId: M,
    profileId: PROFILE,
    isLeader: () => true,
    isReachable: () => reachable,
    autoSync: () => true,
    onProblem: (p) => problems.push(p),
    buildNow: (key) => buildSectionPayload(key as ProfileSectionKey), // what start.ts wires
  })
  collector = startCollector({ onSection: (r) => executor.onSection(r) })
}

function stop(): void {
  collector.stop()
  executor.dispose()
}

/** First attach: nothing on the SOT, every section is created at rev 1. */
async function attach(): Promise<void> {
  api.listProfiles.mockResolvedValue(index([]))
  api.putSection.mockResolvedValue({ kind: 'applied', rev: 1 })
  start()
  await collector.primeAll()
  executor.onReconnected()
  await flush()
  expect(executor.status()).toEqual({
    profile: 'synced', schemaLock: null, sections: { settings: 'synced', workspaces: 'synced' }, locks: {},
    profileGone: false, detail: { settings: { failures: 0, retryAt: null, rev: 1, invalidReason: null }, workspaces: { failures: 0, retryAt: null, rev: 1, invalidReason: null } }, indexFailures: 0, lastSuccessAt: expect.any(Number),
  })
  api.putSection.mockClear()
}

beforeEach(() => {
  vi.useFakeTimers()
  localStorage.clear()
  problems.length = 0
  reachable = true
  vi.clearAllMocks()
  useHostStore.setState({ hosts: { [M]: host(M), [H2]: host(H2, { ip: '10.0.0.2', order: 1 }) }, hostOrder: [M, H2], activeHostId: M, runtime: {} })
  useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null, visitHistory: [] })
  useWorkspaceStore.setState({ workspaces: [], activeWorkspaceId: null })
  useRebuildStore.setState({ operations: {}, lockedBy: null, lockGrant: null })
  useUISettingsStore.setState({ keepAliveCount: 0 })
  api.getSection.mockResolvedValue({ kind: 'failed', reason: 'network', status: 0, message: 'unset' })
  api.deleteSection.mockResolvedValue({ kind: 'failed', reason: 'network', status: 0, message: 'unset' })
})

afterEach(() => {
  stop()
  clearSectionStore()
  vi.useRealTimers()
})

describe('executor — integration (real collector, section store, apply, stores)', () => {
  it('first attach creates every section with baseRev 0 and persists the agreed bases — never `hosts` (host ownership H3a-2)', async () => {
    api.listProfiles.mockResolvedValue(index([]))
    api.putSection.mockResolvedValue({ kind: 'applied', rev: 1 })
    start()
    await collector.primeAll()
    executor.onReconnected()
    await flush()
    // `settings` goes out only once `workspaces` is on the SOT (its scoped entries are written against it)
    expect(api.putSection.mock.calls.map((c) => [c[2], c[3].baseRev])).toEqual([['workspaces', 0], ['settings', 0]])
    const settingsHash = await hashSection(mySettings())
    expect(api.putSection.mock.calls[1][3]).toMatchObject({ hash: settingsHash, payload: mySettings() })
    expect(loadSectionStore(PROFILE).sections.settings).toEqual({ base: { rev: 1, hash: settingsHash }, currentHash: settingsHash })
    expect(loadSectionStore(PROFILE).sections).not.toHaveProperty('hosts')
    expect(problems).toEqual([])
  })

  it('a pulled section lands in the stores, and the collector seeing it afterwards echoes NOTHING back', async () => {
    await attach()
    const theirs = theirSettings(2)
    const theirHash = await hashSection(theirs)
    api.getSection.mockResolvedValue({ kind: 'ok', value: { ...meta('settings', 2, theirHash), payload: theirs as unknown as Record<string, unknown> } })
    executor.onRemoteEvent({ hostId: M, profileId: PROFILE, section: 'settings', rev: 2, hash: theirHash, writerClientId: OTHER_CLIENT })
    await flush()
    expect(keepAlive()).toBe(2)
    expect(loadSectionStore(PROFILE).sections.settings).toEqual({ base: { rev: 2, hash: theirHash }, currentHash: theirHash })
    await debounce() // the collector rebuilds `settings` from the stores and reports it
    await vi.advanceTimersByTimeAsync(60_000)
    expect(api.putSection).not.toHaveBeenCalled()
    expect(api.getSection).toHaveBeenCalledTimes(1)
    expect(executor.status().profile).toBe('synced')
    expect(problems).toEqual([])
  })

  it('RESTART: a 409 lock survives with the snapshot that was SENT, and keep-local puts THAT back and pushes it', async () => {
    await attach()
    const theirs = theirSettings(3)
    const theirHash = await hashSection(theirs)

    // the edit that gets sent — and conflicts
    edit(4)
    const sent = mySettings()
    const sentHash = await hashSection(sent)
    api.putSection.mockResolvedValueOnce({ kind: 'conflict', rev: 5, hash: theirHash, payload: theirs as unknown as Record<string, unknown> })
    await debounce()
    expect(api.putSection.mock.calls[0][3]).toMatchObject({ baseRev: 1, hash: sentHash })
    expect(executor.status().sections.settings).toBe('locked:conflict')

    // the stores move on under the lock
    edit(5)
    await debounce()
    const laterHash = await hashSection(mySettings())
    expect(loadSectionStore(PROFILE).sections.settings).toEqual({
      base: { rev: 1, hash: expect.any(String) },
      currentHash: laterHash,
      conflict: { localHash: sentHash, sot: { rev: 5, hash: theirHash } },
    })

    // restart
    stop()
    api.putSection.mockClear()
    const stored = loadSectionStore(PROFILE).sections
    // the SOT also holds a legacy `hosts` row an older client wrote: never looked at (H3a-2)
    api.listProfiles.mockResolvedValue(
      index([meta('hosts', 9, 'f'.repeat(64)), meta('settings', 5, theirHash), meta('workspaces', 1, stored.workspaces.currentHash!)]),
    )
    const put = { resolve: (_: PutOutcome) => {} }
    api.putSection.mockReturnValue(new Promise<PutOutcome>((r) => (put.resolve = r)))
    start()
    await collector.primeAll()
    executor.onReconnected()
    await flush()
    expect(executor.status().sections.settings).toBe('locked:conflict')
    expect(api.putSection).not.toHaveBeenCalled()

    executor.resolve('settings', 'local')
    await flush()
    expect(keepAlive()).toBe(4) // not 5: the user chose between two known snapshots
    expect(api.putSection).toHaveBeenCalledTimes(1)
    expect(api.putSection.mock.calls[0][2]).toBe('settings')
    expect(api.putSection.mock.calls[0][3]).toMatchObject({ baseRev: 5, hash: sentHash, payload: sent })
    put.resolve({ kind: 'applied', rev: 6 })
    await flush()
    await debounce()
    expect(executor.status().sections.settings).toBe('synced')
    expect(loadSectionStore(PROFILE).sections.settings).toEqual({ base: { rev: 6, hash: sentHash }, currentHash: sentHash })
    expect(executor.status().sections).not.toHaveProperty('hosts')
    expect(api.getSection).not.toHaveBeenCalled()
    expect(problems).toEqual([])
  })

  it('DECIDE-TIME conflict (the SOT side is a hash only): persisted, restored after a restart, and keep-local restores what was local THEN', async () => {
    await attach()
    const theirHash = await hashSection(theirSettings(3))
    const stored = loadSectionStore(PROFILE).sections

    // offline: a local edit; meanwhile the SOT moves. Back online the index shows both → row 8, no 409 involved.
    edit(6)
    const mine = mySettings()
    const mineHash = await hashSection(mine)
    reachable = false
    await debounce()
    reachable = true
    api.listProfiles.mockResolvedValue(
      index([meta('settings', 5, theirHash), meta('workspaces', 1, stored.workspaces.currentHash!)]),
    )
    executor.onReconnected()
    await flush()
    expect(executor.status().sections.settings).toBe('locked:conflict')
    expect(loadSectionStore(PROFILE).sections.settings.conflict).toEqual({ localHash: mineHash, sot: { rev: 5, hash: theirHash } })

    // locked, the SOT moves again — announced by an event, which carries no payload: persisted again
    const newerHash = await hashSection(theirSettings(7))
    executor.onRemoteEvent({ hostId: M, profileId: PROFILE, section: 'settings', rev: 6, hash: newerHash, writerClientId: OTHER_CLIENT })
    expect(loadSectionStore(PROFILE).sections.settings.conflict).toEqual({ localHash: mineHash, sot: { rev: 6, hash: newerHash } })
    expect(problems).toEqual([])

    // the stores move on, then a restart
    edit(8)
    await debounce()
    stop()
    api.listProfiles.mockResolvedValue(
      index([meta('settings', 6, newerHash), meta('workspaces', 1, stored.workspaces.currentHash!)]),
    )
    api.putSection.mockClear()
    api.putSection.mockResolvedValue({ kind: 'applied', rev: 7 })
    start()
    await collector.primeAll()
    executor.onReconnected()
    await flush()
    expect(executor.status().sections.settings).toBe('locked:conflict')

    executor.resolve('settings', 'local')
    await flush()
    expect(keepAlive()).toBe(6)
    expect(api.putSection.mock.calls[0][3]).toMatchObject({ baseRev: 6, hash: mineHash, payload: mine })
    expect(problems).toEqual([])
  })
})
