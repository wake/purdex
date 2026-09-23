// spa/src/lib/profile/executor.pull-guard.integration.test.ts — THE PULL GUARD (#1366; executor.ts header). A pull
// attached with the `hosts` row the user confirmed holds EVERY action of the executor until the SOT's `hosts` has been
// compared with it: equal hash → everything proceeds as before; anything else → halted, nothing applied, nothing
// written, `onPullUnconfirmed` once. The harness is executor.direction.integration.test.ts's: the real collector,
// section store and stores on this side, the in-memory daemon on the other, client A the same executor run first.
// `applySectionToStores` is the real one, wrapped in a spy so that "nothing was applied" can be said directly.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { useHostStore } from '../../stores/useHostStore'
import type { HostConfig } from '../../stores/useHostStore'
import { useTabStore } from '../../stores/useTabStore'
import { useWorkspaceStore } from '../../features/workspace/store'
import { useRebuildStore } from '../../stores/useRebuildStore'
import type { PaneLayout, Tab, Workspace } from '../../types/tab'
import type { Result, Section } from './api'
import { startCollector, type Collector } from './collector'
import { createExecutor, type Executor } from './executor'
import { hashSection } from './hash'
import { clearSectionStore, saveConflict } from './section-store'
import { FakeDaemon } from './test-fake-daemon'

const h = vi.hoisted(() => ({ clientId: 'c_aaaaaaaaaaaa' }))

vi.mock('./hash', async (importOriginal) => {
  const actual = await importOriginal<typeof import('./hash')>()
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

vi.mock('./apply-to-stores', async (importOriginal) => {
  const actual = await importOriginal<typeof import('./apply-to-stores')>()
  return { ...actual, applySectionToStores: vi.fn(actual.applySectionToStores) }
})

vi.mock('./projections', async (importOriginal) => {
  const actual = await importOriginal<typeof import('./projections')>()
  return { ...actual, shapeTable: vi.fn(async () => ({ hosts: ['fp-hosts', 1], settings: ['fp-settings', 3], workspaces: ['fp-workspaces', 1], tabs: ['fp-tabs', 1] })) }
})

vi.mock('../client-identity', () => ({ getClientId: () => h.clientId, isClientIdPersisted: () => true }))

const api = vi.mocked(await import('./api'))
const applied = vi.mocked((await import('./apply-to-stores')).applySectionToStores)

const M = 'host-master'
const H2 = 'host-two'
const PROFILE = 'p_0123456789ab'
const A = 'c_aaaaaaaaaaaa'
const B = 'c_bbbbbbbbbbbb'

type Guard = { rev: number; hash: string } | 'absent' | null

function host(id: string, over: Partial<HostConfig> = {}): HostConfig {
  return { id, name: id, ip: '10.0.0.1', port: 7860, token: 'tok', order: 0, ...over }
}

function leaf(paneId: string): PaneLayout {
  return { type: 'leaf', pane: { id: paneId, content: { kind: 'tmux-session', hostId: M, sessionCode: `c-${paneId}`, mode: 'terminal', cachedName: paneId, tmuxInstance: 'inst' } } }
}

function tab(id: string): Tab {
  return { id, pinned: false, locked: false, createdAt: 1, layout: leaf(`p-${id}`) }
}

function ws(id: string, tabs: string[]): Workspace {
  return { id, name: id.toUpperCase(), tabs, activeTabId: tabs[0] ?? null }
}

function world(h2Name: string, workspaces: Workspace[], tabs: Tab[]): void {
  useHostStore.setState({ hosts: { [M]: host(M), [H2]: host(H2, { ip: '10.0.0.2', order: 1, name: h2Name }) }, hostOrder: [M, H2], activeHostId: M, runtime: {} })
  useTabStore.setState({ tabs: Object.fromEntries(tabs.map((t) => [t.id, t])), tabOrder: tabs.map((t) => t.id), activeTabId: null, visitHistory: [] })
  useWorkspaceStore.setState({ workspaces, activeWorkspaceId: workspaces[0]?.id ?? null })
  useRebuildStore.setState({ operations: {}, lockedBy: null, lockGrant: null })
}

let daemon: FakeDaemon
let executor: Executor | null = null
let collector: Collector | null = null
const problems: Array<{ kind: string; section?: string; detail: string }> = []

interface Run {
  unconfirmed: ReturnType<typeof vi.fn<() => void>>
  settled: ReturnType<typeof vi.fn<() => void>>
  direction: { value: 'push' | 'pull' | null }
  guard: { value: Guard }
}

/** Build B's executor (attached `direction` with `guard`) and its collector; nothing is connected yet. */
async function start(clientId: string, direction: 'push' | 'pull', guard: Guard): Promise<Run> {
  h.clientId = clientId
  const run: Run = { unconfirmed: vi.fn<() => void>(), settled: vi.fn<() => void>(), direction: { value: direction }, guard: { value: guard } }
  run.settled.mockImplementation(() => {
    run.direction.value = null
    run.guard.value = null
  })
  executor = createExecutor({
    hostId: M,
    profileId: PROFILE,
    isLeader: () => true,
    isReachable: () => true,
    autoSync: () => true,
    onProblem: (p) => problems.push(p),
    initialDirection: () => run.direction.value,
    confirmedPullHosts: () => run.guard.value,
    onInitialSettled: run.settled,
    onPullUnconfirmed: run.unconfirmed,
  })
  const ex = executor
  collector = startCollector({ onSection: (r) => ex.onSection(r) })
  await collector.primeAll()
  return run
}

async function play(seconds = 6): Promise<void> {
  for (let i = 0; i < seconds; i += 1) await vi.advanceTimersByTimeAsync(1_000)
}

/** Attach, connect, and let everything play out. */
async function attach(clientId: string, direction: 'push' | 'pull', guard: Guard): Promise<Run> {
  const run = await start(clientId, direction, guard)
  executor!.onReconnected()
  await play()
  return run
}

function leave(): void {
  collector?.stop()
  executor?.dispose()
  collector = null
  executor = null
  clearSectionStore()
  localStorage.clear()
}

/** What B's stores hold: compared before and after a halted attach. */
function snapshot(): string {
  return JSON.stringify([useHostStore.getState().hosts, useWorkspaceStore.getState().workspaces, useTabStore.getState().tabs])
}

const hostsRow = (): { rev: number; hash: string } => {
  const r = daemon.rows.get('hosts')!
  return { rev: r.rev, hash: r.hash! }
}

const writesBy = (clientId: string) => daemon.writes.filter((w) => w.clientId === clientId)

beforeEach(() => {
  vi.useFakeTimers()
  localStorage.clear()
  problems.length = 0
  daemon = new FakeDaemon(PROFILE)
  vi.clearAllMocks()
  api.listProfiles.mockImplementation(async () => daemon.list())
  api.getSection.mockImplementation(async (_h, _p, key) => daemon.get(key))
  api.putSection.mockImplementation(async (_h, _p, key, body) => daemon.put(key, body))
  api.deleteSection.mockImplementation(async (_h, _p, key, params) => daemon.delete(key, params))
})

afterEach(() => {
  leave()
  vi.useRealTimers()
})

/** A's world on the daemon (host-two renamed, two workspaces); `hosts` is rev 1. */
async function clientAHasPushed(): Promise<{ rev: number; hash: string }> {
  world('named-by-A', [ws('wa1', ['ta1']), ws('wa2', ['ta2'])], [tab('ta1'), tab('ta2')])
  const run = await attach(A, 'push', null)
  expect(run.settled).toHaveBeenCalledTimes(1)
  leave()
  problems.length = 0
  return hostsRow()
}

/** Another device writes `hosts` again with other content (another name for host-two): a new rev AND a new hash. */
async function hostsMovedOn(): Promise<void> {
  const cur = daemon.rows.get('hosts')!
  const payload = JSON.parse(JSON.stringify(cur.payload).split('named-by-A').join('renamed-again')) as Record<string, unknown>
  const outcome = daemon.put('hosts', { clientId: A, baseRev: cur.rev, hash: await hashSection(payload), fingerprint: cur.fingerprint, ordinal: cur.ordinal, payload })
  expect(outcome.kind).toBe('applied')
}

/** B: a world of its own — the pull would replace it. */
function worldOfB(): void {
  world(H2, [ws('wb1', ['tb1'])], [tab('tb1')])
}

function expectHalted(run: Run, before: string, writesFrom = 0): void {
  expect(applied).not.toHaveBeenCalled()
  expect(daemon.writes.slice(writesFrom).filter((w) => w.clientId === B)).toEqual([])
  expect(api.putSection.mock.calls.filter((c) => (c[3] as { clientId: string }).clientId === B)).toEqual([])
  expect(api.deleteSection).not.toHaveBeenCalled()
  expect(snapshot()).toBe(before)
  expect(problems.filter((p) => p.kind === 'pull-hosts-unconfirmed')).toHaveLength(1)
  expect(run.unconfirmed).toHaveBeenCalledTimes(1)
  expect(run.settled).not.toHaveBeenCalled()
}

describe('THE PULL GUARD — a match', () => {
  it('the SOT still holds the confirmed row: released, and the pull proceeds as before', async () => {
    const confirmed = await clientAHasPushed()
    worldOfB()
    const run = await attach(B, 'pull', confirmed)
    expect(useWorkspaceStore.getState().workspaces.map((w) => w.id)).toEqual(['wa1', 'wa2'])
    expect(useHostStore.getState().hosts[H2].name).toBe('named-by-A')
    expect(writesBy(B)).toEqual([])
    expect(run.unconfirmed).not.toHaveBeenCalled()
    expect(run.settled).toHaveBeenCalledTimes(1)
    expect(executor!.status().profile).toBe('synced')
  })

  it('a HIGHER rev with the SAME hash is still what the user saw: released', async () => {
    const confirmed = await clientAHasPushed()
    // the same payload written again (delete + re-create): the rev moves, the hash does not
    const cur = daemon.rows.get('hosts')!
    daemon.rows.set('hosts', { ...cur, rev: cur.rev + 2 })
    worldOfB()
    const run = await attach(B, 'pull', confirmed)
    expect(hostsRow()).toEqual({ rev: confirmed.rev + 2, hash: confirmed.hash })
    expect(useHostStore.getState().hosts[H2].name).toBe('named-by-A')
    expect(run.unconfirmed).not.toHaveBeenCalled()
    expect(run.settled).toHaveBeenCalledTimes(1)
  })

  it("guard 'absent' and the index lists no `hosts`: released — the ordinary rules push B's hosts", async () => {
    await clientAHasPushed()
    daemon.delete('hosts', { baseRev: daemon.rows.get('hosts')!.rev, clientId: A })
    worldOfB()
    const run = await attach(B, 'pull', 'absent')
    expect(run.unconfirmed).not.toHaveBeenCalled()
    expect(useWorkspaceStore.getState().workspaces.map((w) => w.id)).toEqual(['wa1', 'wa2'])
    expect(writesBy(B).map((w) => [w.key, w.outcome])).toEqual([['hosts', 'applied']])
    expect(run.settled).toHaveBeenCalledTimes(1)
  })

  it('the index lists `hosts` but its GET answers 404: the index is asked again — never read as "absent"', async () => {
    const confirmed = await clientAHasPushed()
    worldOfB()
    let misses = 1
    api.getSection.mockImplementation(async (_h, _p, key) => (key === 'hosts' && misses-- > 0 ? { kind: 'ok', value: null } : daemon.get(key)))
    const run = await attach(B, 'pull', confirmed)
    await play(10)
    expect(problems.map((p) => p.kind)).toContain('pull-absent-but-listed')
    expect(api.listProfiles.mock.calls.length).toBeGreaterThanOrEqual(2)
    expect(run.unconfirmed).not.toHaveBeenCalled()
    expect(useHostStore.getState().hosts[H2].name).toBe('named-by-A')
  })

  it('the index lists `hosts` and its GET keeps answering 404: no verdict at all (a 404 is not "absent") — nothing runs, nothing halts', async () => {
    await clientAHasPushed()
    worldOfB()
    const before = snapshot()
    api.getSection.mockImplementation(async (_h, _p, key) => (key === 'hosts' ? { kind: 'ok', value: null } : daemon.get(key)))
    // a row that is not the SOT's: read as "absent", the 404 would halt; read as a mismatch too — it is neither
    const run = await attach(B, 'pull', { rev: 99, hash: 'f'.repeat(64) })
    await play(20)
    expect(run.unconfirmed).not.toHaveBeenCalled()
    expect(applied).not.toHaveBeenCalled()
    expect(writesBy(B)).toEqual([])
    expect(snapshot()).toBe(before)
  })

  it('a failed read is retried with the backoff, the barrier up meanwhile; then released', async () => {
    const confirmed = await clientAHasPushed()
    worldOfB()
    const before = snapshot()
    let failures = 2
    api.getSection.mockImplementation(async (_h, _p, key) =>
      key === 'hosts' && failures-- > 0 ? { kind: 'failed', reason: 'network', status: 0, message: 'down' } : daemon.get(key),
    )
    const run = await start(B, 'pull', confirmed)
    executor!.onReconnected()
    await vi.advanceTimersByTimeAsync(1_000)
    expect(applied).not.toHaveBeenCalled()
    expect(writesBy(B)).toEqual([])
    expect(snapshot()).toBe(before)
    expect(problems.filter((p) => p.kind === 'pull-hosts-check-failed').length).toBeGreaterThanOrEqual(1)
    await play(12)
    expect(run.unconfirmed).not.toHaveBeenCalled()
    expect(useWorkspaceStore.getState().workspaces.map((w) => w.id)).toEqual(['wa1', 'wa2'])
    expect(run.settled).toHaveBeenCalledTimes(1)
  })
})

describe('THE PULL GUARD — a mismatch halts everything', () => {
  it('another device moved `hosts` (#1366): nothing applied, nothing written, onPullUnconfirmed once', async () => {
    const confirmed = await clientAHasPushed()
    await hostsMovedOn()
    worldOfB()
    const before = snapshot()
    const run = await attach(B, 'pull', confirmed)
    await play(10)
    expectHalted(run, before)
  })

  it('sections absent on the SOT — which the decision table would PUSH at once — are not pushed either', async () => {
    const confirmed = await clientAHasPushed()
    // only `hosts` is left: B's `workspaces` (no gate) would be pushed by the decision table the moment the index lands
    for (const key of ['settings', 'tabs.wa1', 'tabs.wa2', 'workspaces']) daemon.delete(key, { baseRev: daemon.rows.get(key)!.rev, clientId: A })
    await hostsMovedOn()
    worldOfB()
    const before = snapshot()
    const writesFrom = daemon.writes.length
    // the verdict takes a while: whatever the barrier let through would go out meanwhile
    api.getSection.mockImplementation(async (_h, _p, key) => {
      if (key === 'hosts') await new Promise((r) => setTimeout(r, 3_000))
      return daemon.get(key)
    })
    const run = await attach(B, 'pull', confirmed)
    await play(10)
    expectHalted(run, before, writesFrom)
  })

  it("guard 'absent' but the index lists `hosts`: a mismatch", async () => {
    await clientAHasPushed()
    worldOfB()
    const before = snapshot()
    const run = await attach(B, 'pull', 'absent')
    await play(5)
    expectHalted(run, before)
    expect(api.getSection).not.toHaveBeenCalled() // the index alone decides it
  })

  it('what was asked while the verdict was out never starts after it: a restore from the section store, a remote event, a manual sync', async () => {
    const confirmed = await clientAHasPushed()
    // B restarts over a persisted conflict on `workspaces` whose LOCAL side is a snapshot it could restore
    const sot = daemon.rows.get('workspaces')!
    const localPayload = JSON.parse(JSON.stringify(sot.payload)) as Record<string, unknown>
    ;(localPayload as { order?: unknown }).order = [...((localPayload as { order: string[] }).order ?? [])].reverse()
    const localHash = await hashSection(localPayload)
    expect(localHash).not.toBe(sot.hash)
    expect(saveConflict(PROFILE, 'workspaces', { base: { rev: 0, hash: null }, currentHash: localHash, conflict: { localHash, sot: { rev: sot.rev, hash: sot.hash } } }, { [localHash]: localPayload })).toBe('ok')
    await hostsMovedOn()
    worldOfB()
    const before = snapshot()
    let answer: ((r: Result<Section | null>) => void) | null = null
    api.getSection.mockImplementation((_h, _p, key) => (key === 'hosts' ? new Promise((r) => (answer = r)) : Promise.resolve(daemon.get(key))))

    const run = await start(B, 'pull', confirmed)
    executor!.onReconnected()
    await vi.advanceTimersByTimeAsync(500)
    expect(answer).not.toBeNull() // the verdict is out
    executor!.resolve('workspaces', 'local') // a restore-local, the one action the table does not gate on the network
    executor!.onRemoteEvent({ hostId: M, profileId: PROFILE, section: 'settings', rev: 50, hash: 'e'.repeat(64), writerClientId: A })
    executor!.syncNow()
    await vi.advanceTimersByTimeAsync(500)
    expect(applied).not.toHaveBeenCalled()

    answer!(daemon.get('hosts'))
    await play(10)
    expectHalted(run, before)
  })

  it('a write that lands between the verdict and the `hosts` pull: that pull is not applied either — halted', async () => {
    const confirmed = await clientAHasPushed()
    const seen = daemon.get('hosts')
    await hostsMovedOn()
    worldOfB()
    let first = true
    // the verdict reads what the user saw; the pull right after it reads what is there now
    api.getSection.mockImplementation(async (_h, _p, key) => {
      if (key === 'hosts' && first) {
        first = false
        return seen
      }
      return daemon.get(key)
    })
    const run = await attach(B, 'pull', confirmed)
    await play(5)
    expect(applied.mock.calls.filter((c) => c[0] === 'hosts')).toEqual([])
    expect(useHostStore.getState().hosts[H2].name).toBe(H2)
    expect(writesBy(B)).toEqual([])
    expect(run.unconfirmed).toHaveBeenCalledTimes(1)
    expect(problems.filter((p) => p.kind === 'pull-hosts-unconfirmed')).toHaveLength(1)
  })

  it('the profile is not on the host: the profile-gone path, not a match and not a mismatch', async () => {
    await clientAHasPushed()
    worldOfB()
    api.listProfiles.mockImplementation(async () => ({ kind: 'ok', value: [] }))
    const run = await attach(B, 'pull', 'absent')
    expect(executor!.status().profileGone).toBe(true)
    expect(problems.map((p) => p.kind)).toContain('profile-gone')
    expect(run.unconfirmed).not.toHaveBeenCalled()
    expect(applied).not.toHaveBeenCalled()
  })
})

describe('THE PULL GUARD — who has none', () => {
  it('no guard: today\'s pull, whatever `hosts` holds', async () => {
    await clientAHasPushed()
    await hostsMovedOn()
    worldOfB()
    const run = await attach(B, 'pull', null)
    expect(useHostStore.getState().hosts[H2].name).toBe('renamed-again')
    expect(run.unconfirmed).not.toHaveBeenCalled()
    expect(run.settled).toHaveBeenCalledTimes(1)
  })

  it('push: a guard means nothing', async () => {
    const confirmed = await clientAHasPushed()
    await hostsMovedOn()
    worldOfB()
    const run = await attach(B, 'push', confirmed)
    expect(run.unconfirmed).not.toHaveBeenCalled()
    expect(writesBy(B).length).toBeGreaterThan(0)
    expect(run.settled).toHaveBeenCalledTimes(1)
  })

  it('an executor born without a direction ignores a guard it is handed', async () => {
    const confirmed = await clientAHasPushed()
    await hostsMovedOn()
    worldOfB()
    h.clientId = B
    const unconfirmed = vi.fn<() => void>()
    // a stale guard beside no direction (a reload after the period): no period, so no barrier
    const ex = createExecutor({
      hostId: M, profileId: PROFILE, isLeader: () => true, isReachable: () => true, autoSync: () => true,
      onProblem: (p) => problems.push(p), initialDirection: () => null, confirmedPullHosts: () => confirmed, onPullUnconfirmed: unconfirmed,
    })
    executor = ex
    collector = startCollector({ onSection: (r) => ex.onSection(r) })
    await collector.primeAll()
    ex.onReconnected()
    await play()
    expect(unconfirmed).not.toHaveBeenCalled()
    expect(problems.map((p) => p.kind)).not.toContain('pull-hosts-unconfirmed')
    expect(api.getSection.mock.calls.filter((c) => c[2] === 'hosts')).toEqual([]) // no direction: `hosts` is a conflict for the user; the guard is never read
  })
})
