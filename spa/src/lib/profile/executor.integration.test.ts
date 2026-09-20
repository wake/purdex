// spa/src/lib/profile/executor.integration.test.ts — the executor wired to the
// REAL collector, the REAL section store, the REAL apply-to-stores and the real
// zustand stores. Only the network (`./api`), the digest (`./hash`, a
// synchronous stand-in that still yields 64 hex — the section store validates
// its hashes) and `shapeTable` (crypto.subtle) are replaced.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { useHostStore } from '../../stores/useHostStore'
import type { HostConfig } from '../../stores/useHostStore'
import { useTabStore } from '../../stores/useTabStore'
import { useWorkspaceStore } from '../../features/workspace/store'
import { useRebuildStore } from '../../stores/useRebuildStore'
import type { ProfileIndexEntry, PutOutcome, Result, SectionMeta } from './api'
import { startCollector, type Collector } from './collector'
import { createExecutor, type Executor } from './executor'
import { hashSection } from './hash'
import { clearSectionStore, loadSectionStore } from './section-store'
import { buildHostsSection } from './sections'
import type { HostsPayload } from './types'

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

function renameH2(name: string): void {
  const { hosts } = useHostStore.getState()
  useHostStore.setState({ hosts: { ...hosts, [H2]: { ...hosts[H2], name } } })
}

const h2Name = (): string => useHostStore.getState().hosts[H2].name

/** The hosts payload as ANOTHER client would have written it, with `host-two` renamed. */
function theirHosts(name: string): HostsPayload {
  const mine = buildHostsSection(useHostStore.getState())
  return JSON.parse(JSON.stringify({ ...mine, hosts: { ...mine.hosts, [H2]: { ...mine.hosts[H2], name } } })) as HostsPayload
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

let executor: Executor
let collector: Collector
const problems: Array<{ kind: string; section?: string; detail: string }> = []

function start(): void {
  executor = createExecutor({
    hostId: M,
    profileId: PROFILE,
    isLeader: () => true,
    isReachable: () => true,
    autoSync: () => true,
    onProblem: (p) => problems.push(p),
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
  expect(executor.status()).toEqual({ profile: 'synced', schemaLock: null, sections: { hosts: 'synced', settings: 'synced', workspaces: 'synced' } })
  api.putSection.mockClear()
}

beforeEach(() => {
  vi.useFakeTimers()
  localStorage.clear()
  problems.length = 0
  vi.clearAllMocks()
  useHostStore.setState({ hosts: { [M]: host(M), [H2]: host(H2, { ip: '10.0.0.2', order: 1 }) }, hostOrder: [M, H2], activeHostId: M, runtime: {} })
  useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null, visitHistory: [] })
  useWorkspaceStore.setState({ workspaces: [], activeWorkspaceId: null })
  useRebuildStore.setState({ operations: {}, lockedBy: null, lockGrant: null })
  api.getSection.mockResolvedValue({ kind: 'failed', reason: 'network', status: 0, message: 'unset' })
  api.deleteSection.mockResolvedValue({ kind: 'failed', reason: 'network', status: 0, message: 'unset' })
})

afterEach(() => {
  stop()
  clearSectionStore()
  vi.useRealTimers()
})

describe('executor — integration (real collector, section store, apply, stores)', () => {
  it('first attach creates every section with baseRev 0 and persists the agreed bases', async () => {
    api.listProfiles.mockResolvedValue(index([]))
    api.putSection.mockResolvedValue({ kind: 'applied', rev: 1 })
    start()
    await collector.primeAll()
    executor.onReconnected()
    await flush()
    expect(api.putSection.mock.calls.map((c) => [c[2], c[3].baseRev])).toEqual([['hosts', 0], ['settings', 0], ['workspaces', 0]])
    const hostsHash = await hashSection(buildHostsSection(useHostStore.getState()))
    expect(api.putSection.mock.calls[0][3]).toMatchObject({ hash: hostsHash, payload: buildHostsSection(useHostStore.getState()) })
    expect(loadSectionStore(PROFILE).sections.hosts).toEqual({ base: { rev: 1, hash: hostsHash }, currentHash: hostsHash })
    expect(problems).toEqual([])
  })

  it('a pulled section lands in the stores, and the collector seeing it afterwards echoes NOTHING back', async () => {
    await attach()
    const theirs = theirHosts('renamed-elsewhere')
    const theirHash = await hashSection(theirs)
    api.getSection.mockResolvedValue({ kind: 'ok', value: { ...meta('hosts', 2, theirHash), payload: theirs as unknown as Record<string, unknown> } })
    executor.onRemoteEvent({ hostId: M, profileId: PROFILE, section: 'hosts', rev: 2, hash: theirHash, writerClientId: OTHER_CLIENT })
    await flush()
    expect(h2Name()).toBe('renamed-elsewhere')
    expect(loadSectionStore(PROFILE).sections.hosts).toEqual({ base: { rev: 2, hash: theirHash }, currentHash: theirHash })
    await debounce() // the collector rebuilds `hosts` from the stores and reports it
    await vi.advanceTimersByTimeAsync(60_000)
    expect(api.putSection).not.toHaveBeenCalled()
    expect(api.getSection).toHaveBeenCalledTimes(1)
    expect(executor.status().profile).toBe('synced')
    expect(problems).toEqual([])
  })

  it('RESTART: a 409 lock survives with the snapshot that was SENT, and keep-local puts THAT back and pushes it', async () => {
    await attach()
    const theirs = theirHosts('theirs')
    const theirHash = await hashSection(theirs)

    // the edit that gets sent — and conflicts
    renameH2('mine-sent')
    const sent = buildHostsSection(useHostStore.getState())
    const sentHash = await hashSection(sent)
    api.putSection.mockResolvedValueOnce({ kind: 'conflict', rev: 5, hash: theirHash, payload: theirs as unknown as Record<string, unknown> })
    await debounce()
    expect(api.putSection.mock.calls[0][3]).toMatchObject({ baseRev: 1, hash: sentHash })
    expect(executor.status().sections.hosts).toBe('locked:conflict')

    // the stores move on under the lock
    renameH2('mine-later')
    await debounce()
    const laterHash = await hashSection(buildHostsSection(useHostStore.getState()))
    expect(loadSectionStore(PROFILE).sections.hosts).toEqual({
      base: { rev: 1, hash: expect.any(String) },
      currentHash: laterHash,
      conflict: { localHash: sentHash, sot: { rev: 5, hash: theirHash } },
    })

    // restart
    stop()
    api.putSection.mockClear()
    const stored = loadSectionStore(PROFILE).sections
    api.listProfiles.mockResolvedValue(
      index([meta('hosts', 5, theirHash), meta('settings', 1, stored.settings.currentHash!), meta('workspaces', 1, stored.workspaces.currentHash!)]),
    )
    const put = { resolve: (_: PutOutcome) => {} }
    api.putSection.mockReturnValue(new Promise<PutOutcome>((r) => (put.resolve = r)))
    start()
    await collector.primeAll()
    executor.onReconnected()
    await flush()
    expect(executor.status().sections.hosts).toBe('locked:conflict')
    expect(api.putSection).not.toHaveBeenCalled()

    executor.resolve('hosts', 'local')
    await flush()
    expect(h2Name()).toBe('mine-sent') // not 'mine-later': the user chose between two known snapshots
    expect(api.putSection).toHaveBeenCalledTimes(1)
    expect(api.putSection.mock.calls[0][2]).toBe('hosts')
    expect(api.putSection.mock.calls[0][3]).toMatchObject({ baseRev: 5, hash: sentHash, payload: sent })
    put.resolve({ kind: 'applied', rev: 6 })
    await flush()
    await debounce()
    expect(executor.status().sections.hosts).toBe('synced')
    expect(loadSectionStore(PROFILE).sections.hosts).toEqual({ base: { rev: 6, hash: sentHash }, currentHash: sentHash })
    expect(problems).toEqual([])
  })
})
