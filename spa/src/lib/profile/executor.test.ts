import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { useWorkspaceStore } from '../../features/workspace/store'
import type { Workspace } from '../../types/tab'
import type { Failure, ProfileIndexEntry, PutOutcome, Result, Section, SectionMeta } from './api'
import type { ApplyOutcome } from './apply-to-stores'
import type { PersistedSection } from './section-store'
import type { SectionEvent } from './sync-state'
import { createExecutor, type Executor, type ExecutorDeps, type ExecutorStatus } from './executor'

// Unit tests: every collaborator that touches the network, storage or the app's
// stores is mocked; the reducer is REAL (wrapped, so the events it is fed can
// be counted). executor.integration.test.ts runs the real section store and the
// real apply against the real stores.

const h = vi.hoisted(() => ({
  stored: {} as Record<string, unknown>,
  persistedStash: new Map<string, unknown>(),
  clientId: 'c_aaaaaaaaaaaa',
  events: [] as Array<{ event: { type: string } & Record<string, unknown>; changed: boolean }>,
}))

vi.mock('./hash', async (importOriginal) => {
  const actual = await importOriginal<typeof import('./hash')>()
  return { ...actual, hashSection: vi.fn(async (payload: unknown) => actual.structuralKey(payload)) }
})

vi.mock('./api', () => ({
  listProfiles: vi.fn(),
  getSection: vi.fn(),
  putSection: vi.fn(),
  deleteSection: vi.fn(),
}))

vi.mock('./apply-to-stores', () => ({ applySectionToStores: vi.fn() }))

vi.mock('./section-store', () => ({
  loadSectionStore: vi.fn((profileId: string) => ({ profileId, sections: h.stored })),
  saveSection: vi.fn(() => 'ok'),
  saveConflict: vi.fn(() => 'ok'),
  dropSection: vi.fn(() => 'ok'),
  getStash: vi.fn((_p: string, hash: string) => h.persistedStash.get(hash)),
  pruneStash: vi.fn(() => 'ok'),
}))

// `shapeTable` is a crypto.subtle digest: fake timers cannot flush it.
vi.mock('./projections', async (importOriginal) => {
  const actual = await importOriginal<typeof import('./projections')>()
  return {
    ...actual,
    shapeTable: vi.fn(async () => ({ hosts: ['fp-hosts', 1], settings: ['fp-settings', 3], workspaces: ['fp-workspaces', 1], tabs: ['fp-tabs', 1] })),
  }
})

vi.mock('../client-identity', () => ({ getClientId: () => h.clientId }))

vi.mock('./sync-state', async (importOriginal) => {
  const actual = await importOriginal<typeof import('./sync-state')>()
  return {
    ...actual,
    reduceSection: (s: Parameters<typeof actual.reduceSection>[0], e: SectionEvent) => {
      const next = actual.reduceSection(s, e)
      h.events.push({ event: e as never, changed: next !== s })
      return next
    },
  }
})

const api = vi.mocked(await import('./api'))
const store = vi.mocked(await import('./section-store'))
const { applySectionToStores } = vi.mocked(await import('./apply-to-stores'))

const HOST = 'host-1'
const PROFILE = 'p_0123456789ab'
const OTHER_CLIENT = 'c_bbbbbbbbbbbb'

/* ─── helpers ─── */

interface Deferred<T> {
  promise: Promise<T>
  resolve: (v: T) => void
}
function deferred<T>(): Deferred<T> {
  let resolve!: (v: T) => void
  const promise = new Promise<T>((r) => (resolve = r))
  return { promise, resolve }
}

const flush = (): Promise<unknown> => vi.advanceTimersByTimeAsync(0)

function failure(reason: Failure['reason'], extra: Partial<Failure> = {}): Failure {
  return { kind: 'failed', reason, status: 0, message: reason, ...extra }
}

function meta(section: string, rev: number, hash: string, shape: [string, number] | null = null): SectionMeta {
  const kind = section.startsWith('tabs.') ? 'tabs' : section
  const mine: Record<string, [string, number]> = { hosts: ['fp-hosts', 1], settings: ['fp-settings', 3], workspaces: ['fp-workspaces', 1], tabs: ['fp-tabs', 1] }
  const [fingerprint, ordinal] = shape ?? mine[kind] ?? ['fp-unknown', 1]
  return { section, rev, hash, fingerprint, ordinal, writer: OTHER_CLIENT, updatedAt: 0 }
}

function index(sections: SectionMeta[], id = PROFILE): Result<ProfileIndexEntry[]> {
  return { kind: 'ok', value: [{ id, name: 'p', createdAt: 0, updatedAt: 0, sections, attachments: [] }] }
}

function sectionOf(m: SectionMeta, payload: Record<string, unknown>): Result<Section | null> {
  return { kind: 'ok', value: { ...m, payload } }
}

function ws(id: string): Workspace {
  return { id, name: id, tabs: [], activeTabId: null, moduleConfig: {} }
}

function eventsOf(...types: string[]): Array<{ type: string } & Record<string, unknown>> {
  return h.events.filter((e) => types.includes(e.event.type)).map((e) => e.event)
}

const TERMINAL = ['push-applied', 'push-converged', 'push-conflict', 'push-failed']

interface Harness {
  ex: Executor
  problems: Array<{ kind: string; section?: string; detail: string }>
  statuses: ExecutorStatus[]
  env: { leader: boolean; reachable: boolean; autoSync: boolean }
}

let live: Executor[] = []

function make(over: Partial<ExecutorDeps> = {}): Harness {
  const env = { leader: true, reachable: true, autoSync: true }
  const problems: Harness['problems'] = []
  const statuses: ExecutorStatus[] = []
  const ex = createExecutor({
    hostId: HOST,
    profileId: PROFILE,
    isLeader: () => env.leader,
    isReachable: () => env.reachable,
    autoSync: () => env.autoSync,
    onProblem: (p) => problems.push(p),
    onStatus: (s) => statuses.push(s),
    ...over,
  })
  live.push(ex)
  return { ex, problems, statuses, env }
}

/** A harness whose sections are clean and freshly indexed at `rev` 1: `{key: hash}`. */
async function synced(sections: Record<string, string>, over: Partial<ExecutorDeps> = {}): Promise<Harness> {
  h.stored = Object.fromEntries(
    Object.entries(sections).map(([key, hash]) => [key, { base: { rev: 1, hash }, currentHash: hash } satisfies PersistedSection]),
  )
  api.listProfiles.mockResolvedValueOnce(index(Object.entries(sections).map(([key, hash]) => meta(key, 1, hash))))
  const harness = make(over)
  harness.ex.onReconnected()
  await flush()
  h.events.length = 0
  harness.problems.length = 0
  harness.statuses.length = 0
  vi.clearAllMocks()
  return harness
}

beforeEach(() => {
  vi.useFakeTimers()
  h.stored = {}
  h.persistedStash.clear()
  h.events.length = 0
  h.clientId = 'c_aaaaaaaaaaaa'
  vi.clearAllMocks()
  api.listProfiles.mockResolvedValue(failure('network'))
  api.getSection.mockResolvedValue(failure('network'))
  api.putSection.mockResolvedValue(failure('network'))
  api.deleteSection.mockResolvedValue(failure('network'))
  applySectionToStores.mockResolvedValue({ ok: false, reason: 'busy' })
  store.saveSection.mockReturnValue('ok')
  store.saveConflict.mockReturnValue('ok')
  store.dropSection.mockReturnValue('ok')
  useWorkspaceStore.setState({ workspaces: [] })
})

afterEach(() => {
  for (const ex of live) ex.dispose()
  live = []
  vi.useRealTimers()
})

/* ─── stage 1: state, dispatch, persistence ─── */

describe('executor — state and persistence', () => {
  it('starts from the section store, restored and stale', () => {
    h.stored = { hosts: { base: { rev: 4, hash: 'H1' }, currentHash: 'H1' }, settings: { base: { rev: 2, hash: 'S1' }, currentHash: 'S2' } }
    const { ex } = make()
    expect(store.loadSectionStore).toHaveBeenCalledWith(PROFILE)
    expect(ex.status()).toEqual({ profile: 'pending', schemaLock: null, sections: { hosts: 'synced', settings: 'pending' } , locks: {} })
  })

  it('restores a persisted conflict as locked:conflict', () => {
    h.stored = { hosts: { base: { rev: 4, hash: 'H1' }, currentHash: 'H2', conflict: { localHash: 'H2', sot: { rev: 5, hash: 'H9' } } } }
    expect(make().ex.status().sections).toEqual({ hosts: 'locked:conflict' })
  })

  it('status() carries the fingerprint of every LOCKED section — what a `resolve` is bound to (P3 plan Task 2)', () => {
    h.stored = {
      hosts: { base: { rev: 4, hash: 'H1' }, currentHash: 'H2', conflict: { localHash: 'H2', sot: { rev: 5, hash: 'H9' } } },
      settings: { base: { rev: 2, hash: 'S1' }, currentHash: 'S1' },
    }
    expect(make().ex.status().locks).toEqual({
      hosts: { status: 'locked:conflict', currentHash: 'H2', sot: { rev: 5, hash: 'H9' }, conflict: { localHash: 'H2', sot: { rev: 5, hash: 'H9' } } },
    })
  })

  it('onStatus fires when an open conflict moves under an unchanged status: the SOT advanced while locked', async () => {
    const { ex, statuses } = await synced({ hosts: 'H1' })
    api.putSection.mockResolvedValue({ kind: 'conflict', rev: 5, hash: 'H9', payload: { theirs: true } })
    ex.onSection({ key: 'hosts', hash: 'H2', payload: { mine: true } })
    await flush()
    expect(statuses.at(-1)).toMatchObject({ sections: { hosts: 'locked:conflict' }, locks: { hosts: { conflict: { localHash: 'H2', sot: { rev: 5, hash: 'H9' } } } } })
    const before = statuses.length
    ex.onRemoteEvent({ hostId: HOST, profileId: PROFILE, section: 'hosts', rev: 6, hash: 'HA', writerClientId: OTHER_CLIENT })
    expect(statuses).toHaveLength(before + 1)
    expect(statuses.at(-1)).toMatchObject({ sections: { hosts: 'locked:conflict' }, locks: { hosts: { sot: { rev: 6, hash: 'HA' }, conflict: { localHash: 'H2', sot: { rev: 6, hash: 'HA' } } } } })
  })

  it('onStatus fires when the LOCAL side of a locked section moves under an unchanged status: `locks` carries currentHash', async () => {
    const { ex, statuses } = await synced({ hosts: 'H1' })
    api.putSection.mockResolvedValue({ kind: 'conflict', rev: 5, hash: 'H9', payload: { theirs: true } })
    ex.onSection({ key: 'hosts', hash: 'H2', payload: { mine: true } })
    await flush()
    expect(ex.status().locks.hosts).toMatchObject({ status: 'locked:conflict', currentHash: 'H2' })
    const before = statuses.length
    ex.onSection({ key: 'hosts', hash: 'H3', payload: { mine: 'again' } })
    expect(statuses).toHaveLength(before + 1)
    expect(statuses.at(-1)).toMatchObject({ sections: { hosts: 'locked:conflict' }, locks: { hosts: { status: 'locked:conflict', currentHash: 'H3' } } })
  })

  it('a collector report creates the section, persists it and asks for the index', async () => {
    api.listProfiles.mockReturnValue(deferred<Result<ProfileIndexEntry[]>>().promise)
    const { ex } = make()
    ex.onSection({ key: 'hosts', hash: 'H1', payload: { a: 1 } })
    expect(store.saveSection).toHaveBeenCalledWith(PROFILE, 'hosts', { base: { rev: 0, hash: null }, currentHash: 'H1' })
    await flush()
    expect(api.listProfiles).toHaveBeenCalledTimes(1)
    expect(ex.status().sections).toEqual({ hosts: 'pending' })
  })

  it('a report that changes nothing is neither persisted nor acted on', async () => {
    const { ex } = await synced({ hosts: 'H1' })
    ex.onSection({ key: 'hosts', hash: 'H1', payload: { a: 1 } })
    await flush()
    expect(store.saveSection).not.toHaveBeenCalled()
    expect(api.putSection).not.toHaveBeenCalled()
    expect(api.listProfiles).not.toHaveBeenCalled()
  })

  it('a vanished report for a section nobody knows creates nothing', () => {
    const { ex } = make()
    ex.onSection({ key: 'tabs.w1', hash: null, payload: null })
    expect(ex.status().sections).toEqual({})
    expect(store.saveSection).not.toHaveBeenCalled()
    expect(store.dropSection).not.toHaveBeenCalled()
  })

  it('does not persist a change that leaves {base, currentHash, conflict} alone', async () => {
    const { ex } = await synced({ hosts: 'H1' })
    ex.onRemoteEvent({ hostId: HOST, profileId: PROFILE, section: 'hosts', rev: 2, hash: 'H1', writerClientId: OTHER_CLIENT })
    expect(h.events.at(-1)).toMatchObject({ event: { type: 'remote-event' }, changed: true })
    expect(store.saveSection).not.toHaveBeenCalled()
  })

  it('onStatus fires on a change only', async () => {
    const { ex, statuses } = await synced({ hosts: 'H1' })
    ex.onSection({ key: 'hosts', hash: 'H1', payload: {} })
    expect(statuses).toHaveLength(0)
    api.putSection.mockReturnValue(deferred<PutOutcome>().promise)
    ex.onSection({ key: 'hosts', hash: 'H2', payload: {} })
    expect(statuses).toHaveLength(1)
    expect(statuses[0]).toEqual({ profile: 'pending', schemaLock: null, sections: { hosts: 'pending' } , locks: {} })
    ex.onSection({ key: 'hosts', hash: 'H3', payload: {} }) // still pending: no second call
    expect(statuses).toHaveLength(1)
  })
})

/* ─── reindex ─── */

describe('executor — reindex', () => {
  it('is single-flight: N stale sections share one listProfiles, each answered at the epoch it had when the request was sent', async () => {
    h.stored = {
      hosts: { base: { rev: 1, hash: 'H1' }, currentHash: 'H1' },
      settings: { base: { rev: 1, hash: 'S1' }, currentHash: 'S1' },
      workspaces: { base: { rev: 1, hash: 'W1' }, currentHash: 'W1' },
    }
    const list = deferred<Result<ProfileIndexEntry[]>>()
    api.listProfiles.mockReturnValue(list.promise)
    const { ex } = make()
    ex.onReconnected()
    await flush()
    expect(api.listProfiles).toHaveBeenCalledTimes(1)
    expect(api.listProfiles).toHaveBeenCalledWith(HOST, expect.objectContaining({ signal: expect.any(AbortSignal) }))
    list.resolve(index([meta('hosts', 1, 'H1'), meta('settings', 1, 'S1'), meta('workspaces', 1, 'W1')]))
    await flush()
    expect(api.listProfiles).toHaveBeenCalledTimes(1)
    const answers = eventsOf('sot-index')
    expect(answers).toHaveLength(3)
    // `reconnected` moved every indexEpoch from 0 to 1 before the request went out
    expect(answers.every((e) => e.epoch === 1)).toBe(true)
    expect(h.events.filter((e) => e.event.type === 'sot-index').every((e) => e.changed)).toBe(true)
    expect(ex.status().profile).toBe('synced')
  })

  it('a failed list dispatches NOTHING and retries 2 s → 4 → 8 → 16 → 30 → 30, then starts over after a success', async () => {
    h.stored = { hosts: { base: { rev: 1, hash: 'H1' }, currentHash: 'H1' } }
    api.listProfiles.mockResolvedValue(failure('server', { status: 500 }))
    const { ex, problems } = make()
    ex.onReconnected()
    await flush()
    h.events.length = 0
    let calls = 1
    for (const ms of [2000, 4000, 8000, 16000, 30000, 30000]) {
      await vi.advanceTimersByTimeAsync(ms - 1)
      expect(api.listProfiles).toHaveBeenCalledTimes(calls)
      await vi.advanceTimersByTimeAsync(1)
      calls += 1
      expect(api.listProfiles).toHaveBeenCalledTimes(calls)
    }
    expect(h.events).toEqual([]) // not one event: "the request failed" is not "the profile is gone"
    expect(ex.status().sections).toEqual({ hosts: 'synced' })
    expect(problems.every((p) => p.kind === 'reindex-failed')).toBe(true)

    // success → the backoff starts over
    api.listProfiles.mockResolvedValueOnce(index([meta('hosts', 1, 'H1')]))
    await vi.advanceTimersByTimeAsync(30000)
    expect(eventsOf('sot-index')).toHaveLength(1)
    api.listProfiles.mockResolvedValue(failure('network'))
    ex.onRemoteEvent({ hostId: HOST, profileId: PROFILE, section: 'hosts', rev: 0, hash: 'H1', writerClientId: OTHER_CLIENT }) // no-op
    ex.onReconnected()
    await flush()
    const before = api.listProfiles.mock.calls.length
    await vi.advanceTimersByTimeAsync(1999)
    expect(api.listProfiles).toHaveBeenCalledTimes(before)
    await vi.advanceTimersByTimeAsync(1)
    expect(api.listProfiles).toHaveBeenCalledTimes(before + 1)
  })

  it('a failing list is not re-requested by other pumps before its backoff has passed', async () => {
    api.listProfiles.mockResolvedValue(failure('network'))
    const { ex } = make()
    ex.onSection({ key: 'hosts', hash: 'H1', payload: {} })
    await flush()
    expect(api.listProfiles).toHaveBeenCalledTimes(1)
    for (let i = 0; i < 5; i += 1) ex.onSection({ key: 'hosts', hash: `H${i + 2}`, payload: {} })
    await vi.advanceTimersByTimeAsync(1000)
    expect(api.listProfiles).toHaveBeenCalledTimes(1)
  })

  it('PROFILE GONE: a well-formed list without the profile writes no store, drops no section, and reports locked:reset', async () => {
    useWorkspaceStore.setState({ workspaces: [ws('w1')] })
    h.stored = {
      hosts: { base: { rev: 3, hash: 'H1' }, currentHash: 'H1' },
      workspaces: { base: { rev: 3, hash: 'W1' }, currentHash: 'W1' },
      'tabs.w1': { base: { rev: 3, hash: 'T1' }, currentHash: 'T1' },
    }
    api.listProfiles.mockResolvedValue(index([meta('hosts', 1, 'HX')], 'p_ffffffffffff')) // a REBUILT profile has another id
    api.getSection.mockResolvedValue({ kind: 'ok', value: null })
    const { ex, problems, statuses } = make()
    ex.onReconnected()
    await flush()
    await vi.advanceTimersByTimeAsync(60_000)

    expect(eventsOf('sot-index')).toEqual([])
    expect(api.getSection).not.toHaveBeenCalled()
    expect(applySectionToStores).not.toHaveBeenCalled()
    expect(store.dropSection).not.toHaveBeenCalled()
    expect(store.saveSection).not.toHaveBeenCalled()
    expect(api.putSection).not.toHaveBeenCalled()
    expect(api.deleteSection).not.toHaveBeenCalled()
    expect(ex.status().profile).toBe('locked:reset')
    expect(Object.keys(ex.status().sections).sort()).toEqual(['hosts', 'tabs.w1', 'workspaces'])
    expect(problems.filter((p) => p.kind === 'profile-gone')).toHaveLength(1)
    expect(statuses.at(-1)?.profile).toBe('locked:reset')

    // and it stays stopped: no action ever runs again
    ex.onSection({ key: 'hosts', hash: 'H2', payload: {} })
    ex.onReconnected()
    ex.syncNow()
    await vi.advanceTimersByTimeAsync(60_000)
    expect(api.listProfiles).toHaveBeenCalledTimes(1)
    expect(api.putSection).not.toHaveBeenCalled()
  })

  it('a section the index lists and this client has never seen is created and pulled', async () => {
    api.listProfiles.mockResolvedValue(index([meta('hosts', 1, 'H1'), meta('settings', 7, 'S7')]))
    const m = meta('settings', 7, 'S7')
    api.getSection.mockResolvedValue(sectionOf(m, { x: 1 }))
    applySectionToStores.mockResolvedValue({ ok: true, hash: 'S7' })
    h.stored = { hosts: { base: { rev: 1, hash: 'H1' }, currentHash: 'H1' } }
    const { ex } = make()
    ex.onReconnected()
    await flush()
    expect(api.getSection).toHaveBeenCalledWith(HOST, PROFILE, 'settings', expect.anything())
    expect(applySectionToStores).toHaveBeenCalledWith('settings', { x: 1 }, { masterHostId: HOST })
    expect(store.saveSection).toHaveBeenCalledWith(PROFILE, 'settings', { base: { rev: 7, hash: 'S7' }, currentHash: 'S7' })
    expect(ex.status().sections).toEqual({ hosts: 'synced', settings: 'synced' })
  })

  it('a section of an unknown kind is carried: no state, no pull, reported once', async () => {
    api.listProfiles.mockResolvedValue(index([meta('hosts', 1, 'H1'), meta('plugins', 2, 'P2')]))
    h.stored = { hosts: { base: { rev: 1, hash: 'H1' }, currentHash: 'H1' } }
    const { ex, problems } = make()
    ex.onReconnected()
    await flush()
    ex.onReconnected()
    await flush()
    expect(ex.status().sections).toEqual({ hosts: 'synced' })
    expect(api.getSection).not.toHaveBeenCalled()
    expect(problems.filter((p) => p.kind === 'sections-unknown-kind')).toEqual([{ kind: 'sections-unknown-kind', detail: 'plugins' }])
  })

  it('a tabs section whose workspace is nowhere is reported as unrendered, and not pulled', async () => {
    api.listProfiles.mockResolvedValue(index([meta('hosts', 1, 'H1'), meta('workspaces', 1, 'W1'), meta('tabs.ghost', 4, 'T4')]))
    h.stored = { hosts: { base: { rev: 1, hash: 'H1' }, currentHash: 'H1' }, workspaces: { base: { rev: 1, hash: 'W1' }, currentHash: 'W1' } }
    const { ex, problems } = make()
    ex.onReconnected()
    await flush()
    expect(problems).toContainEqual({ kind: 'sections-unrendered', detail: 'tabs.ghost' })
    expect(api.getSection).not.toHaveBeenCalled()
  })

  it('a schema-locked index still teaches the sections the SOT, but nothing is pushed, deleted or pulled', async () => {
    useWorkspaceStore.setState({ workspaces: [ws('w1')] })
    h.stored = {
      hosts: { base: { rev: 1, hash: 'H1' }, currentHash: 'H2' }, // dirty → would push
      settings: { base: { rev: 1, hash: 'S1' }, currentHash: 'S1' }, // behind → would pull
      'tabs.w1': { base: { rev: 1, hash: 'T1' }, currentHash: null }, // gone locally → would delete
    }
    api.listProfiles.mockResolvedValue(index([meta('hosts', 1, 'H1'), meta('settings', 2, 'S2', ['fp-newer', 4]), meta('tabs.w1', 1, 'T1')]))
    const { ex, problems } = make()
    ex.onSection({ key: 'hosts', hash: 'H2', payload: { a: 1 } })
    ex.onReconnected()
    await flush()
    expect(eventsOf('sot-index')).toHaveLength(3)
    expect(ex.status().profile).toBe('locked:schema')
    expect(ex.status().schemaLock).toMatchObject({ section: 'settings', verdict: 'sot-is-newer' })
    expect(problems.filter((p) => p.kind === 'schema-lock')).toHaveLength(1)
    await vi.advanceTimersByTimeAsync(60_000)
    expect(api.putSection).not.toHaveBeenCalled()
    expect(api.deleteSection).not.toHaveBeenCalled()
    expect(api.getSection).not.toHaveBeenCalled()

    // a later index without the offender lifts the lock
    api.listProfiles.mockResolvedValue(index([meta('hosts', 1, 'H1'), meta('settings', 1, 'S1'), meta('tabs.w1', 1, 'T1')]))
    api.putSection.mockResolvedValue({ kind: 'applied', rev: 2 })
    ex.onReconnected()
    await flush()
    expect(ex.status().schemaLock).toBeNull()
    expect(api.putSection).toHaveBeenCalledTimes(1)
  })

  it('unauthorized stops every request until the host reconnects', async () => {
    h.stored = { hosts: { base: { rev: 1, hash: 'H1' }, currentHash: 'H1' } }
    api.listProfiles.mockResolvedValue(failure('unauthorized', { status: 401 }))
    const { ex, problems } = make()
    ex.onReconnected()
    await flush()
    await vi.advanceTimersByTimeAsync(120_000)
    expect(api.listProfiles).toHaveBeenCalledTimes(1)
    expect(problems.map((p) => p.kind)).toEqual(['unauthorized'])
    ex.onReconnected()
    await flush()
    expect(api.listProfiles).toHaveBeenCalledTimes(2)
  })
})

/* ─── push / delete ─── */

describe('executor — push and delete', () => {
  it('push: sends the stashed payload with the shape of its kind and this client id, then records the new base', async () => {
    const { ex } = await synced({ hosts: 'H1' })
    api.putSection.mockResolvedValue({ kind: 'applied', rev: 2 })
    ex.onSection({ key: 'hosts', hash: 'H2', payload: { hosts: { a: 1 } } })
    await flush()
    expect(api.putSection).toHaveBeenCalledTimes(1)
    expect(api.putSection).toHaveBeenCalledWith(
      HOST, PROFILE, 'hosts',
      { clientId: 'c_aaaaaaaaaaaa', baseRev: 1, hash: 'H2', fingerprint: 'fp-hosts', ordinal: 1, payload: { hosts: { a: 1 } } },
      expect.objectContaining({ signal: expect.any(AbortSignal) }),
    )
    expect(eventsOf('push-started', ...TERMINAL).map((e) => e.type)).toEqual(['push-started', 'push-applied'])
    expect(store.saveSection).toHaveBeenLastCalledWith(PROFILE, 'hosts', { base: { rev: 2, hash: 'H2' }, currentHash: 'H2' })
    expect(ex.status().profile).toBe('synced')
  })

  it('a tabs push carries the tabs shape', async () => {
    const { ex } = await synced({ hosts: 'H1', 'tabs.w1': 'T1' })
    api.putSection.mockResolvedValue({ kind: 'applied', rev: 2 })
    ex.onSection({ key: 'tabs.w1', hash: 'T2', payload: { order: [] } })
    await flush()
    expect(api.putSection.mock.calls[0][3]).toMatchObject({ fingerprint: 'fp-tabs', ordinal: 1 })
  })

  it('delete: a section that vanished locally is deleted with its base rev, then dropped from the store', async () => {
    const { ex } = await synced({ hosts: 'H1', 'tabs.w1': 'T1' })
    api.deleteSection.mockResolvedValue({ kind: 'applied', rev: 2 })
    ex.onSection({ key: 'tabs.w1', hash: null, payload: null })
    await flush()
    expect(api.deleteSection).toHaveBeenCalledWith(HOST, PROFILE, 'tabs.w1', { baseRev: 1, clientId: 'c_aaaaaaaaaaaa' }, expect.anything())
    expect(eventsOf(...TERMINAL).map((e) => e.type)).toEqual(['push-applied'])
    expect(store.dropSection).toHaveBeenCalledWith(PROFILE, 'tabs.w1')
    expect(ex.status().sections).toEqual({ hosts: 'synced' }) // forgotten: it exists on neither side
  })

  const FAILURES: Array<[string, PutOutcome]> = [
    ['network', failure('network')],
    ['timeout', failure('timeout')],
    ['aborted', failure('aborted')],
    ['contended', failure('contended', { status: 503, retryAfterMs: 1000 })],
    ['not-found', failure('not-found', { status: 404 })],
    ['too-large', failure('too-large', { status: 413 })],
    ['rejected', failure('rejected', { status: 400 })],
    ['unauthorized', failure('unauthorized', { status: 401 })],
    ['server', failure('server', { status: 500 })],
    ['malformed', failure('malformed', { status: 200 })],
    ['unknown-host', failure('unknown-host')],
  ]
  const OUTCOMES: Array<[string, PutOutcome, string]> = [
    ['applied', { kind: 'applied', rev: 2 }, 'push-applied'],
    ['converged', { kind: 'converged', rev: 2 }, 'push-converged'],
    ['conflict', { kind: 'conflict', rev: 5, hash: 'H9', payload: { theirs: true } }, 'push-conflict'],
    ['conflict with an absent SOT', { kind: 'conflict', rev: 0, hash: null, payload: null }, 'push-conflict'],
    ['schema', { kind: 'schema', fingerprint: 'fp-newer', ordinal: 9 }, 'push-failed'],
    ...FAILURES.map(([name, f]): [string, PutOutcome, string] => [`failed/${name}`, f, 'push-failed']),
  ]

  it.each(OUTCOMES)('PUT outcome %s → exactly one terminal event, and the flight is closed', async (_name, outcome, terminal) => {
    const { ex } = await synced({ hosts: 'H1' })
    const put = deferred<PutOutcome>()
    api.putSection.mockReturnValueOnce(put.promise)
    ex.onSection({ key: 'hosts', hash: 'H2', payload: { a: 1 } })
    await flush()
    expect(eventsOf('push-started')).toHaveLength(1)
    expect(eventsOf(...TERMINAL)).toHaveLength(0)
    put.resolve(outcome)
    await flush()
    const terminals = h.events.filter((e) => TERMINAL.includes(e.event.type))
    expect(terminals.map((e) => e.event.type)).toEqual([terminal])
    expect(terminals[0].changed).toBe(true) // it closed the flight it was sent for
    // no open flight is left: a fresh edit can open a new one (it could not while one is open)
    expect(api.putSection).toHaveBeenCalledTimes(1)
  })

  it.each(OUTCOMES.filter(([, o]) => o.kind !== 'converged' && o.kind !== 'schema'))('DELETE outcome %s → exactly one terminal event', async (_name, outcome, terminal) => {
    const { ex } = await synced({ hosts: 'H1', 'tabs.w1': 'T1' })
    api.deleteSection.mockResolvedValueOnce(outcome as never)
    ex.onSection({ key: 'tabs.w1', hash: null, payload: null })
    await flush()
    const terminals = h.events.filter((e) => TERMINAL.includes(e.event.type))
    expect(terminals.map((e) => e.event.type)).toEqual([terminal])
    expect(terminals[0].changed).toBe(true)
  })

  it('a 409 stashes the SOT payload and persists the conflict with BOTH payloads', async () => {
    const { ex } = await synced({ hosts: 'H1' })
    api.putSection.mockResolvedValue({ kind: 'conflict', rev: 5, hash: 'H9', payload: { theirs: true } })
    ex.onSection({ key: 'hosts', hash: 'H2', payload: { mine: true } })
    await flush()
    expect(ex.status().sections).toEqual({ hosts: 'locked:conflict' })
    expect(store.saveConflict).toHaveBeenCalledTimes(1)
    expect(store.saveConflict).toHaveBeenCalledWith(
      PROFILE, 'hosts',
      { base: { rev: 1, hash: 'H1' }, currentHash: 'H2', conflict: { localHash: 'H2', sot: { rev: 5, hash: 'H9' } } },
      { H2: { mine: true }, H9: { theirs: true } },
    )
    // once locked, the section is never written through saveSection (it refuses a conflict — the lock would be lost)
    const afterLock = store.saveSection.mock.invocationCallOrder.filter((n) => n > store.saveConflict.mock.invocationCallOrder[0])
    expect(afterLock).toEqual([])
  })

  it('the local side of a conflict is the snapshot that was SENT, even if the section was edited during the flight', async () => {
    const { ex } = await synced({ hosts: 'H1' })
    const put = deferred<PutOutcome>()
    api.putSection.mockReturnValueOnce(put.promise)
    ex.onSection({ key: 'hosts', hash: 'H2', payload: { sent: true } })
    await flush()
    ex.onSection({ key: 'hosts', hash: 'H3', payload: { later: true } })
    put.resolve({ kind: 'conflict', rev: 5, hash: 'H9', payload: { theirs: true } })
    await flush()
    expect(store.saveConflict).toHaveBeenLastCalledWith(
      PROFILE, 'hosts',
      expect.objectContaining({ currentHash: 'H3', conflict: { localHash: 'H2', sot: { rev: 5, hash: 'H9' } } }),
      { H2: { sent: true }, H9: { theirs: true } },
    )
  })

  it('a conflict the store refuses stays in memory and is reported ONCE', async () => {
    const { ex, problems } = await synced({ hosts: 'H1' })
    store.saveConflict.mockReturnValue('failed')
    api.putSection.mockResolvedValue({ kind: 'conflict', rev: 5, hash: 'H9', payload: { theirs: true } })
    ex.onSection({ key: 'hosts', hash: 'H2', payload: { mine: true } })
    await flush()
    // the lock learns of a newer SOT and is edited underneath: more refused writes, no more reports
    ex.onRemoteEvent({ hostId: HOST, profileId: PROFILE, section: 'hosts', rev: 6, hash: 'HA', writerClientId: OTHER_CLIENT })
    ex.onSection({ key: 'hosts', hash: 'H3', payload: {} })
    expect(store.saveConflict.mock.calls.length).toBeGreaterThan(1)
    expect(problems.filter((p) => p.kind === 'conflict-not-persisted')).toEqual([
      { kind: 'conflict-not-persisted', section: 'hosts', detail: expect.any(String) },
    ])
    expect(ex.status().sections).toEqual({ hosts: 'locked:conflict' })
  })

  it('resolving a persisted conflict writes the section without it and prunes the stash, keeping the snapshot to restore', async () => {
    const { ex } = await synced({ hosts: 'H1' })
    api.putSection.mockResolvedValueOnce({ kind: 'conflict', rev: 5, hash: 'H9', payload: { theirs: true } })
    ex.onSection({ key: 'hosts', hash: 'H2', payload: { sent: true } })
    await flush()
    ex.onSection({ key: 'hosts', hash: 'H3', payload: { later: true } })
    vi.clearAllMocks()
    applySectionToStores.mockReturnValue(new Promise(() => {}))
    ex.resolve('hosts', 'local')
    expect(store.saveSection).toHaveBeenCalledWith(PROFILE, 'hosts', { base: { rev: 5, hash: 'H9' }, currentHash: 'H3' })
    expect(store.pruneStash).toHaveBeenCalledTimes(1)
    const keep = store.pruneStash.mock.calls[0][1]
    expect(keep.has('H2')).toBe(true) // restoreLocal retains it
  })

  it('SERIALISED: the second section’s PUT is not sent before the first one resolved', async () => {
    const { ex } = await synced({ hosts: 'H1', settings: 'S1' })
    const first = deferred<PutOutcome>()
    api.putSection.mockReturnValueOnce(first.promise).mockResolvedValueOnce({ kind: 'applied', rev: 2 })
    ex.onSection({ key: 'hosts', hash: 'H2', payload: { a: 1 } })
    ex.onSection({ key: 'settings', hash: 'S2', payload: { b: 1 } })
    await flush()
    await vi.advanceTimersByTimeAsync(5000)
    expect(api.putSection).toHaveBeenCalledTimes(1)
    expect(api.putSection.mock.calls[0][2]).toBe('hosts')
    expect(eventsOf('push-started')).toHaveLength(1) // the second flight is not even open yet
    first.resolve({ kind: 'applied', rev: 2 })
    await flush()
    expect(api.putSection).toHaveBeenCalledTimes(2)
    expect(api.putSection.mock.calls[1][2]).toBe('settings')
  })

  it('SCHEMA 409: the write queued behind it is never sent, and the whole profile reads locked:schema', async () => {
    const { ex, problems } = await synced({ hosts: 'H1', settings: 'S1', workspaces: 'W1' })
    const first = deferred<PutOutcome>()
    api.putSection.mockReturnValueOnce(first.promise).mockResolvedValue({ kind: 'applied', rev: 2 })
    ex.onSection({ key: 'hosts', hash: 'H2', payload: { a: 1 } })
    ex.onSection({ key: 'settings', hash: 'S2', payload: { b: 1 } })
    ex.onSection({ key: 'workspaces', hash: 'W2', payload: { c: 1 } })
    await flush()
    first.resolve({ kind: 'schema', fingerprint: 'fp-newer', ordinal: 9 })
    await vi.advanceTimersByTimeAsync(120_000)
    expect(api.putSection).toHaveBeenCalledTimes(1)
    expect(eventsOf('push-started')).toHaveLength(1) // no flight was opened for the queued ones: nothing to close
    expect(ex.status().profile).toBe('locked:schema')
    expect(ex.status().schemaLock).toMatchObject({ section: 'hosts', kind: 'hosts', verdict: 'sot-is-newer', sot: { fingerprint: 'fp-newer', ordinal: 9 } })
    expect(problems.filter((p) => p.kind === 'schema-lock')).toHaveLength(1)
    // nor does a new edit go out
    ex.onSection({ key: 'hosts', hash: 'H3', payload: {} })
    await vi.advanceTimersByTimeAsync(120_000)
    expect(api.putSection).toHaveBeenCalledTimes(1)
  })

  it('TOKEN: a write whose section moved on while it was queued is not sent; the fresh decision is', async () => {
    const { ex } = await synced({ hosts: 'H1', settings: 'S1' })
    const first = deferred<PutOutcome>()
    api.putSection.mockReturnValueOnce(first.promise).mockResolvedValue({ kind: 'applied', rev: 2 })
    ex.onSection({ key: 'hosts', hash: 'H2', payload: { a: 1 } })
    ex.onSection({ key: 'settings', hash: 'S2', payload: { stale: true } })
    await flush()
    ex.onSection({ key: 'settings', hash: 'S3', payload: { fresh: true } }) // the queued token is dead now
    first.resolve({ kind: 'applied', rev: 2 })
    await flush()
    const sent = api.putSection.mock.calls.filter((c) => c[2] === 'settings').map((c) => c[3].hash)
    expect(sent).toEqual(['S3'])
    expect(h.events.filter((e) => e.event.type === 'push-started' && !e.changed)).toHaveLength(1)
  })

  it('TOKEN: a section that locked while its write was queued sends nothing', async () => {
    const { ex } = await synced({ hosts: 'H1', settings: 'S1' })
    const first = deferred<PutOutcome>()
    api.putSection.mockReturnValueOnce(first.promise).mockResolvedValue({ kind: 'applied', rev: 9 })
    ex.onSection({ key: 'hosts', hash: 'H2', payload: { a: 1 } })
    ex.onSection({ key: 'settings', hash: 'S2', payload: { b: 1 } })
    await flush()
    ex.onRemoteEvent({ hostId: HOST, profileId: PROFILE, section: 'settings', rev: 4, hash: 'S9', writerClientId: OTHER_CLIENT })
    first.resolve({ kind: 'applied', rev: 2 })
    await flush()
    expect(api.putSection.mock.calls.filter((c) => c[2] === 'settings')).toEqual([])
    expect(ex.status().sections.settings).toBe('locked:conflict')
  })

  it('NOT THE LEADER: nothing is sent, the opened flight is closed, and it is reported', async () => {
    const { ex, problems, env } = await synced({ hosts: 'H1' })
    env.leader = false
    ex.onSection({ key: 'hosts', hash: 'H2', payload: { a: 1 } })
    await flush()
    expect(api.putSection).not.toHaveBeenCalled()
    expect(eventsOf('push-started', ...TERMINAL).map((e) => e.type)).toEqual(['push-started', 'push-failed'])
    expect(problems.map((p) => p.kind)).toEqual(['not-leader'])
    // the flight is closed: once the lease is back the retry opens a new one and sends
    env.leader = true
    api.putSection.mockResolvedValue({ kind: 'applied', rev: 2 })
    await vi.advanceTimersByTimeAsync(2000)
    expect(api.putSection).toHaveBeenCalledTimes(1)
    expect(ex.status().profile).toBe('synced')
  })

  it('isLeader is asked for every write, not once', async () => {
    let asked = 0
    const { ex } = await synced({ hosts: 'H1' }, { isLeader: () => ((asked += 1), true) })
    api.putSection.mockResolvedValue({ kind: 'applied', rev: 2 })
    ex.onSection({ key: 'hosts', hash: 'H2', payload: {} })
    await flush()
    api.putSection.mockResolvedValue({ kind: 'applied', rev: 3 })
    ex.onSection({ key: 'hosts', hash: 'H3', payload: {} })
    await flush()
    expect(asked).toBe(2)
  })

  it('a payload that is not held closes the flight and is reported', async () => {
    const { ex, problems } = await synced({ hosts: 'H1' })
    ex.onSection({ key: 'hosts', hash: 'H2', payload: null })
    await flush()
    expect(api.putSection).not.toHaveBeenCalled()
    expect(eventsOf('push-started', ...TERMINAL).map((e) => e.type)).toEqual(['push-started', 'push-failed'])
    expect(problems.map((p) => p.kind)).toEqual(['push-payload-missing'])
  })

  it('contended retries after retryAfterMs; other failures back off 2 s → 4 s, and an edit meanwhile does not jump the queue', async () => {
    const { ex } = await synced({ hosts: 'H1' })
    api.putSection.mockResolvedValueOnce(failure('contended', { status: 503, retryAfterMs: 1000 }))
    api.putSection.mockResolvedValue(failure('server', { status: 500 }))
    ex.onSection({ key: 'hosts', hash: 'H2', payload: {} })
    await flush()
    expect(api.putSection).toHaveBeenCalledTimes(1)
    await vi.advanceTimersByTimeAsync(999)
    expect(api.putSection).toHaveBeenCalledTimes(1)
    await vi.advanceTimersByTimeAsync(1)
    expect(api.putSection).toHaveBeenCalledTimes(2) // → server: 2 s
    ex.onSection({ key: 'hosts', hash: 'H3', payload: {} }) // pumps the section, but the backoff holds
    await vi.advanceTimersByTimeAsync(1999)
    expect(api.putSection).toHaveBeenCalledTimes(2)
    await vi.advanceTimersByTimeAsync(1)
    expect(api.putSection).toHaveBeenCalledTimes(3) // → server again: 4 s
    expect(api.putSection.mock.calls[2][3].hash).toBe('H3')
    await vi.advanceTimersByTimeAsync(3999)
    expect(api.putSection).toHaveBeenCalledTimes(3)
    await vi.advanceTimersByTimeAsync(1)
    expect(api.putSection).toHaveBeenCalledTimes(4)
  })

  it('unauthorized on a write stops retrying until onReconnected / syncNow', async () => {
    const { ex, problems } = await synced({ hosts: 'H1' })
    api.putSection.mockResolvedValue(failure('unauthorized', { status: 401 }))
    ex.onSection({ key: 'hosts', hash: 'H2', payload: {} })
    await flush()
    await vi.advanceTimersByTimeAsync(120_000)
    expect(api.putSection).toHaveBeenCalledTimes(1)
    expect(problems.map((p) => p.kind)).toEqual(['unauthorized'])
    api.putSection.mockResolvedValue({ kind: 'applied', rev: 2 })
    ex.syncNow()
    await flush()
    expect(api.putSection).toHaveBeenCalledTimes(2)
  })

  it('a 404 on a write asks the list whether the profile is still there', async () => {
    const { ex } = await synced({ hosts: 'H1' })
    api.putSection.mockResolvedValue(failure('not-found', { status: 404 }))
    api.listProfiles.mockResolvedValue(index([], 'p_ffffffffffff'))
    ex.onSection({ key: 'hosts', hash: 'H2', payload: {} })
    await flush()
    expect(api.listProfiles).toHaveBeenCalledTimes(1)
    expect(ex.status().profile).toBe('locked:reset')
  })
})

/* ─── pull ─── */

describe('executor — pull', () => {
  const remote = (section: string, rev: number, hash: string | null, writerClientId = OTHER_CLIENT) => ({ hostId: HOST, profileId: PROFILE, section, rev, hash, writerClientId })

  it('a remote event on a clean section is fetched, applied, and recorded at the FETCHED rev', async () => {
    const { ex } = await synced({ hosts: 'H1' })
    api.getSection.mockResolvedValue(sectionOf(meta('hosts', 3, 'H3'), { v: 3 })) // the SOT moved again during the request
    applySectionToStores.mockResolvedValue({ ok: true, hash: 'H3' })
    ex.onRemoteEvent(remote('hosts', 2, 'H2'))
    await flush()
    expect(applySectionToStores).toHaveBeenCalledWith('hosts', { v: 3 }, { masterHostId: HOST })
    expect(eventsOf('pull-applied')).toEqual([{ type: 'pull-applied', rev: 3, hash: 'H3', localHash: 'H3' }])
    expect(store.saveSection).toHaveBeenLastCalledWith(PROFILE, 'hosts', { base: { rev: 3, hash: 'H3' }, currentHash: 'H3' })
    expect(ex.status().profile).toBe('synced')
  })

  it('NO ECHO: the collector reporting the applied content afterwards causes no second action', async () => {
    const { ex } = await synced({ hosts: 'H1' })
    api.getSection.mockResolvedValue(sectionOf(meta('hosts', 2, 'H2'), { v: 2 }))
    applySectionToStores.mockResolvedValue({ ok: true, hash: 'H2' })
    ex.onRemoteEvent(remote('hosts', 2, 'H2'))
    await flush()
    vi.clearAllMocks()
    h.events.length = 0
    ex.onSection({ key: 'hosts', hash: 'H2', payload: { v: 2 } }) // what the apply wrote, seen by the collector
    await vi.advanceTimersByTimeAsync(60_000)
    expect(h.events).toEqual([{ event: { type: 'local-changed', hash: 'H2' }, changed: false }])
    expect(api.putSection).not.toHaveBeenCalled()
    expect(api.getSection).not.toHaveBeenCalled()
    expect(store.saveSection).not.toHaveBeenCalled()
  })

  it('an own event pulls nothing', async () => {
    const { ex } = await synced({ hosts: 'H1' })
    ex.onRemoteEvent(remote('hosts', 2, 'H2', 'c_aaaaaaaaaaaa'))
    await flush()
    expect(h.events).toEqual([{ event: { type: 'remote-event', rev: 2, hash: 'H2', own: true }, changed: false }])
    expect(api.getSection).not.toHaveBeenCalled()
    // `own` follows getClientId() at the time of the event, not a cached id
    h.clientId = 'c_cccccccccccc'
    ex.onRemoteEvent(remote('hosts', 2, 'H2', 'c_aaaaaaaaaaaa'))
    expect(h.events.at(-1)?.event).toMatchObject({ own: false })
  })

  it('events of another profile, another host, or an unknown kind are ignored', async () => {
    const { ex } = await synced({ hosts: 'H1' })
    ex.onRemoteEvent({ ...remote('hosts', 2, 'H2'), profileId: 'p_ffffffffffff' })
    ex.onRemoteEvent({ ...remote('hosts', 2, 'H2'), hostId: 'host-2' })
    ex.onRemoteEvent(remote('plugins', 2, 'P2'))
    await flush()
    expect(h.events).toEqual([])
    expect(api.getSection).not.toHaveBeenCalled()
  })

  it('a remote event for a section nobody knows creates it and pulls', async () => {
    const { ex } = await synced({ hosts: 'H1' })
    api.listProfiles.mockResolvedValue(index([meta('hosts', 1, 'H1'), meta('settings', 1, 'S1')]))
    api.getSection.mockResolvedValue(sectionOf(meta('settings', 1, 'S1'), { s: 1 }))
    applySectionToStores.mockResolvedValue({ ok: true, hash: 'S1' })
    ex.onRemoteEvent(remote('settings', 1, 'S1'))
    await flush()
    expect(eventsOf('pull-applied')).toEqual([{ type: 'pull-applied', rev: 1, hash: 'S1', localHash: 'S1' }])
  })

  it('REGRESSION: a tabs.<id> deletion that arrives BEFORE its `workspaces` change is not applied — the tabs stay, nothing is written, and it converges once the workspace is gone', async () => {
    useWorkspaceStore.setState({ workspaces: [ws('w1')] })
    const { ex, problems } = await synced({ hosts: 'H1', workspaces: 'W1', 'tabs.w1': 'T1' })
    // the other client removed workspace w1: PUT workspaces + DELETE tabs.w1 — and the DELETE's event got here first
    api.getSection.mockImplementation(async (_h, _p, section) =>
      section === 'workspaces' ? sectionOf(meta('workspaces', 2, 'W2'), { order: [] }) : { kind: 'ok', value: null },
    )
    const list = deferred<Result<ProfileIndexEntry[]>>()
    api.listProfiles.mockReturnValue(list.promise)
    applySectionToStores.mockImplementation(async (key) => {
      if (key === 'workspaces') useWorkspaceStore.setState({ workspaces: [] }) // what the real apply does
      return { ok: true, hash: key === 'workspaces' ? 'W2' : 'EMPTY' }
    })
    ex.onRemoteEvent(remote('tabs.w1', 2, null))
    await flush()
    expect(applySectionToStores).not.toHaveBeenCalled() // the workspace's tabs were NOT emptied
    expect(eventsOf('pull-applied')).toEqual([])
    expect(api.listProfiles).toHaveBeenCalledTimes(1) // "is `workspaces` behind?" — asked at once
    expect(ex.status().sections['tabs.w1']).toBe('synced')

    // the index shows `workspaces` moved → it is pulled → w1 goes → the collector reports tabs.w1 gone
    list.resolve(index([meta('hosts', 1, 'H1'), meta('workspaces', 2, 'W2')]))
    await flush()
    expect(applySectionToStores.mock.calls.map((c) => c[0])).toEqual(['workspaces'])
    ex.onSection({ key: 'tabs.w1', hash: null, payload: null })
    await vi.advanceTimersByTimeAsync(120_000)

    expect(ex.status()).toEqual({ profile: 'synced', schemaLock: null, sections: { hosts: 'synced', workspaces: 'synced' } , locks: {} }) // forgotten
    expect(store.dropSection).toHaveBeenCalledWith(PROFILE, 'tabs.w1')
    expect(applySectionToStores.mock.calls.map((c) => c[0])).toEqual(['workspaces']) // never the deletion
    expect(api.putSection).not.toHaveBeenCalled() // no orphan re-created
    expect(api.deleteSection).not.toHaveBeenCalled() // and no second delete: both sides are absent, that is agreement
    expect(problems).toEqual([])
  })

  it('a tabs.<id> deleted on the SOT while its workspace STAYS (not a normal operation): retried with backoff, never applied, reported once', async () => {
    useWorkspaceStore.setState({ workspaces: [ws('w1')] })
    const { ex, problems } = await synced({ hosts: 'H1', workspaces: 'W1', 'tabs.w1': 'T1' })
    api.getSection.mockResolvedValue({ kind: 'ok', value: null })
    api.listProfiles.mockResolvedValue(index([meta('hosts', 1, 'H1'), meta('workspaces', 1, 'W1')]))
    ex.onRemoteEvent(remote('tabs.w1', 2, null))
    await flush()
    expect(api.getSection).toHaveBeenCalledTimes(1)
    await vi.advanceTimersByTimeAsync(1999)
    expect(api.getSection).toHaveBeenCalledTimes(1)
    await vi.advanceTimersByTimeAsync(1)
    expect(api.getSection).toHaveBeenCalledTimes(2)
    expect(problems).toEqual([])
    await vi.advanceTimersByTimeAsync(600_000)
    expect(applySectionToStores).not.toHaveBeenCalled()
    expect(api.putSection).not.toHaveBeenCalled()
    expect(api.listProfiles).toHaveBeenCalledTimes(1) // the index is asked once, not on every retry
    expect(problems).toEqual([{ kind: 'tabs-deleted-workspace-kept', section: 'tabs.w1', detail: expect.any(String) }])
  })

  it('a tabs.<id> deletion whose workspace is NOT here is never pulled at all: the local absence already agrees', async () => {
    const { ex } = await synced({ hosts: 'H1', workspaces: 'W1', 'tabs.w1': 'T1' })
    ex.onSection({ key: 'tabs.w1', hash: null, payload: null }) // the workspace went first
    api.deleteSection.mockReturnValue(new Promise(() => {}))
    await flush()
    vi.clearAllMocks()
    const { ex: ex2 } = await synced({ hosts: 'H1', workspaces: 'W1' })
    ex2.onRemoteEvent(remote('tabs.w9', 2, null))
    await vi.advanceTimersByTimeAsync(60_000)
    expect(api.getSection).not.toHaveBeenCalled()
    expect(applySectionToStores).not.toHaveBeenCalled()
    expect(ex2.status().sections).toEqual({ hosts: 'synced', workspaces: 'synced' })
    void ex
  })

  it('a 404 for a section the index lists as LIVE is not applied as a deletion: the list is asked', async () => {
    const { ex, problems } = await synced({ hosts: 'H1' })
    api.getSection.mockResolvedValue({ kind: 'ok', value: null })
    api.listProfiles.mockResolvedValue(index([], 'p_ffffffffffff')) // …and it turns out the profile is gone
    ex.onRemoteEvent(remote('hosts', 2, 'H2'))
    await flush()
    expect(applySectionToStores).not.toHaveBeenCalled()
    expect(problems.map((p) => p.kind)).toEqual(['pull-absent-but-listed', 'profile-gone'])
    expect(ex.status().profile).toBe('locked:reset')
  })

  it('a failed fetch dispatches nothing and is retried with backoff', async () => {
    const { ex } = await synced({ hosts: 'H1' })
    api.getSection.mockResolvedValue(failure('server', { status: 500 }))
    ex.onRemoteEvent(remote('hosts', 2, 'H2'))
    await flush()
    h.events.length = 0
    await vi.advanceTimersByTimeAsync(1999)
    expect(api.getSection).toHaveBeenCalledTimes(1)
    await vi.advanceTimersByTimeAsync(1)
    expect(api.getSection).toHaveBeenCalledTimes(2)
    await vi.advanceTimersByTimeAsync(4000)
    expect(api.getSection).toHaveBeenCalledTimes(3)
    expect(h.events).toEqual([])
  })

  it('a section that became dirty during the fetch is not applied', async () => {
    const { ex } = await synced({ hosts: 'H1' })
    const get = deferred<Result<Section | null>>()
    api.getSection.mockReturnValue(get.promise)
    ex.onRemoteEvent(remote('hosts', 2, 'H2'))
    await flush()
    ex.onSection({ key: 'hosts', hash: 'HL', payload: { local: true } })
    get.resolve(sectionOf(meta('hosts', 2, 'H2'), { v: 2 }))
    await flush()
    expect(applySectionToStores).not.toHaveBeenCalled()
    expect(ex.status().sections.hosts).toBe('locked:conflict') // dirty + moved: row 8, decided right after
  })

  it('busy: nothing is dispatched, the pull is retried shortly', async () => {
    const { ex } = await synced({ hosts: 'H1' })
    api.getSection.mockResolvedValue(sectionOf(meta('hosts', 2, 'H2'), { v: 2 }))
    applySectionToStores.mockResolvedValueOnce({ ok: false, reason: 'busy' }).mockResolvedValue({ ok: true, hash: 'H2' })
    ex.onRemoteEvent(remote('hosts', 2, 'H2'))
    await flush()
    expect(eventsOf('pull-applied', 'locked')).toEqual([])
    await vi.advanceTimersByTimeAsync(499)
    expect(applySectionToStores).toHaveBeenCalledTimes(1)
    await vi.advanceTimersByTimeAsync(1)
    expect(applySectionToStores).toHaveBeenCalledTimes(2)
    expect(eventsOf('pull-applied')).toHaveLength(1)
  })

  it('invalid: the section locks on the FETCHED rev, is reported, and is never fetched again on a timer', async () => {
    const { ex, problems } = await synced({ hosts: 'H1' })
    api.getSection.mockResolvedValue(sectionOf(meta('hosts', 2, 'H2'), { v: 2 }))
    applySectionToStores.mockResolvedValue({ ok: false, reason: 'invalid', detail: 'removes the master host' })
    ex.onRemoteEvent(remote('hosts', 2, 'H2'))
    await flush()
    expect(eventsOf('locked')).toEqual([{ type: 'locked', reason: 'invalid', rev: 2 }])
    expect(ex.status().sections.hosts).toBe('locked:invalid')
    // a lock WITHOUT a pair has a fingerprint too: what a resolve from the UI is checked against (sync-status.ts)
    expect(ex.status().locks).toEqual({ hosts: { status: 'locked:invalid', currentHash: 'H1', sot: { rev: 2, hash: 'H2' }, conflict: null } })
    expect(problems).toEqual([{ kind: 'pull-invalid', section: 'hosts', detail: 'removes the master host' }])
    await vi.advanceTimersByTimeAsync(600_000)
    expect(api.getSection).toHaveBeenCalledTimes(1)
    // resolve('sot') is refused by the reducer on locked:invalid — nothing to handle
    ex.resolve('hosts', 'sot')
    expect(ex.status().sections.hosts).toBe('locked:invalid')
  })

  it('an invalid verdict on a rev OLDER than the known SOT is refused by the reducer — and does not spin', async () => {
    const { ex } = await synced({ hosts: 'H1' })
    api.getSection.mockResolvedValue(sectionOf(meta('hosts', 2, 'H2'), { v: 2 }))
    applySectionToStores.mockResolvedValue({ ok: false, reason: 'invalid', detail: 'nope' })
    ex.onRemoteEvent(remote('hosts', 3, 'H3'))
    await flush()
    expect(h.events.filter((e) => e.event.type === 'locked')).toEqual([{ event: { type: 'locked', reason: 'invalid', rev: 2 }, changed: false }])
    expect(api.getSection).toHaveBeenCalledTimes(1)
    await vi.advanceTimersByTimeAsync(2000)
    expect(api.getSection).toHaveBeenCalledTimes(2)
  })

  it('a throwing apply is treated as a failure: reported, nothing dispatched, retried with backoff', async () => {
    const { ex, problems } = await synced({ hosts: 'H1' })
    api.getSection.mockResolvedValue(sectionOf(meta('hosts', 2, 'H2'), { v: 2 }))
    applySectionToStores.mockRejectedValueOnce(new Error('setState blew up')).mockResolvedValue({ ok: true, hash: 'H2' })
    ex.onRemoteEvent(remote('hosts', 2, 'H2'))
    await flush()
    expect(problems).toEqual([{ kind: 'apply-threw', section: 'hosts', detail: 'setState blew up' }])
    expect(eventsOf('pull-applied', 'locked')).toEqual([])
    await vi.advanceTimersByTimeAsync(2000)
    expect(eventsOf('pull-applied')).toHaveLength(1)
  })

  it('ORDER: hosts BEHIND (clean, so its status still reads synced) holds every tabs.* pull back; applying hosts releases them', async () => {
    useWorkspaceStore.setState({ workspaces: [ws('w1'), ws('w2')] })
    const { ex } = await synced({ hosts: 'H1', workspaces: 'W1', 'tabs.w1': 'T1', 'tabs.w2': 'U1' })
    const hostsGet = deferred<Result<Section | null>>()
    api.getSection.mockImplementation((_h, _p, section) => {
      if (section === 'hosts') return hostsGet.promise
      if (section === 'tabs.w2') return Promise.resolve(sectionOf(meta(section, 2, 'U2'), { order: [] }))
      w1Fetches += 1
      return Promise.resolve(sectionOf(meta(section, w1Fetches + 1, `T${w1Fetches + 1}`), { order: [] }))
    })
    let w1Fetches = 0
    const applied: string[] = []
    applySectionToStores.mockImplementation(async (key) => {
      applied.push(key)
      return { ok: true, hash: key === 'hosts' ? 'H2' : key === 'tabs.w1' ? `T${w1Fetches + 1}` : 'U2' }
    })
    ex.onRemoteEvent(remote('tabs.w1', 2, 'T2')) // hosts is up to date here: this one may go…
    await flush()
    expect(applied).toEqual(['tabs.w1'])
    ex.onRemoteEvent(remote('hosts', 2, 'H2')) // …now hosts is behind, its fetch is out
    ex.onRemoteEvent(remote('tabs.w2', 2, 'U2'))
    ex.onRemoteEvent(remote('tabs.w1', 3, 'T3'))
    await vi.advanceTimersByTimeAsync(10_000)
    expect(ex.status().sections.hosts).toBe('synced') // clean ≠ up to date
    expect(api.getSection.mock.calls.map((c) => c[2])).toEqual(['tabs.w1', 'hosts'])
    hostsGet.resolve(sectionOf(meta('hosts', 2, 'H2'), { v: 2 }))
    await flush()
    expect(applied.slice(0, 2)).toEqual(['tabs.w1', 'hosts'])
    expect(applied.slice(2).sort()).toEqual(['tabs.w1', 'tabs.w2'])
  })

  it('ORDER: hosts dirty (pending) holds the tabs.* pull back; its push landing releases it', async () => {
    useWorkspaceStore.setState({ workspaces: [ws('w1')] })
    const { ex } = await synced({ hosts: 'H1', workspaces: 'W1', 'tabs.w1': 'T1' })
    const put = deferred<PutOutcome>()
    api.putSection.mockReturnValue(put.promise)
    api.getSection.mockResolvedValue(sectionOf(meta('tabs.w1', 2, 'T2'), { order: [] }))
    applySectionToStores.mockResolvedValue({ ok: true, hash: 'T2' })
    ex.onSection({ key: 'hosts', hash: 'HL', payload: { local: 1 } })
    ex.onRemoteEvent(remote('tabs.w1', 2, 'T2'))
    await vi.advanceTimersByTimeAsync(10_000)
    expect(api.getSection).not.toHaveBeenCalled()
    put.resolve({ kind: 'applied', rev: 2 })
    await flush()
    expect(applySectionToStores).toHaveBeenCalledWith('tabs.w1', { order: [] }, { masterHostId: HOST })
  })

  it('ORDER: a tabs.* pull waits for workspaces too, and for its workspace to exist here', async () => {
    const { ex } = await synced({ hosts: 'H1', workspaces: 'W1' })
    const wsGet = deferred<Result<Section | null>>()
    api.getSection.mockImplementation((_h, _p, section) =>
      section === 'workspaces' ? wsGet.promise : Promise.resolve(sectionOf(meta('tabs.w9', 1, 'T1'), { order: [] })),
    )
    const applied: string[] = []
    applySectionToStores.mockImplementation(async (key) => {
      applied.push(key)
      if (key === 'workspaces') useWorkspaceStore.setState({ workspaces: [ws('w9')] }) // what the real apply does
      return { ok: true, hash: key === 'workspaces' ? 'W2' : 'T1' }
    })
    api.listProfiles.mockResolvedValue(index([meta('hosts', 1, 'H1'), meta('workspaces', 1, 'W1'), meta('tabs.w9', 1, 'T1')]))
    ex.onRemoteEvent(remote('tabs.w9', 1, 'T1')) // overtook its workspace (a never-seen section asks for the index first)
    await vi.advanceTimersByTimeAsync(10_000)
    expect(api.getSection).not.toHaveBeenCalled() // workspace w9 is not here
    ex.onRemoteEvent(remote('workspaces', 2, 'W2'))
    await flush()
    expect(api.getSection.mock.calls.map((c) => c[2])).toEqual(['workspaces'])
    wsGet.resolve(sectionOf(meta('workspaces', 2, 'W2'), { order: ['w9'] }))
    await flush()
    expect(applied).toEqual(['workspaces', 'tabs.w9']) // applying workspaces pumped the waiting tabs.*
    expect(ex.status().profile).toBe('synced')
  })

  it('an apply that comes back unrendered is NOT recorded as applied', async () => {
    useWorkspaceStore.setState({ workspaces: [ws('w1')] })
    const { ex, problems } = await synced({ hosts: 'H1', workspaces: 'W1', 'tabs.w1': 'T1' })
    api.getSection.mockResolvedValue(sectionOf(meta('tabs.w1', 2, 'T2'), { order: [] }))
    applySectionToStores.mockResolvedValue({ ok: true, hash: null }) // the workspace went away under the apply
    ex.onRemoteEvent(remote('tabs.w1', 2, 'T2'))
    await vi.advanceTimersByTimeAsync(60_000)
    expect(eventsOf('pull-applied')).toEqual([])
    expect(applySectionToStores).toHaveBeenCalledTimes(1) // and it does not spin
    expect(problems.map((p) => p.kind)).toEqual(['pull-unrendered'])
  })

  it('a read that is OLDER than the SOT already known does not spin: it is retried with backoff', async () => {
    const { ex } = await synced({ hosts: 'H1' })
    api.getSection.mockResolvedValue(sectionOf(meta('hosts', 2, 'H2'), { v: 2 }))
    applySectionToStores.mockResolvedValue({ ok: true, hash: 'H2' })
    ex.onRemoteEvent(remote('hosts', 3, 'H3')) // the event ran ahead of what the read serves
    await flush()
    expect(api.getSection).toHaveBeenCalledTimes(1)
    await vi.advanceTimersByTimeAsync(1999)
    expect(api.getSection).toHaveBeenCalledTimes(1)
    api.getSection.mockResolvedValue(sectionOf(meta('hosts', 3, 'H3'), { v: 3 }))
    applySectionToStores.mockResolvedValue({ ok: true, hash: 'H3' })
    await vi.advanceTimersByTimeAsync(1)
    expect(api.getSection).toHaveBeenCalledTimes(2)
    expect(ex.status().profile).toBe('synced')
    await vi.advanceTimersByTimeAsync(600_000)
    expect(api.getSection).toHaveBeenCalledTimes(2)
  })

  it('SANITISER: the stores did not keep what arrived → base is the SOT’s, and the sanitised content is pushed back over the pulled rev', async () => {
    const { ex, problems } = await synced({ hosts: 'H1' })
    api.getSection.mockResolvedValue(sectionOf(meta('hosts', 2, 'H2'), { v: 2, junk: true }))
    applySectionToStores.mockResolvedValue({ ok: true, hash: 'H2-clean' })
    api.putSection.mockReturnValue(new Promise(() => {}))
    ex.onRemoteEvent(remote('hosts', 2, 'H2'))
    await flush()
    expect(eventsOf('pull-applied')).toEqual([{ type: 'pull-applied', rev: 2, hash: 'H2', localHash: 'H2-clean' }])
    expect(store.saveSection).toHaveBeenLastCalledWith(PROFILE, 'hosts', { base: { rev: 2, hash: 'H2' }, currentHash: 'H2-clean' })
    expect(problems).toEqual([{ kind: 'pull-hash-mismatch', section: 'hosts', detail: expect.stringContaining('pushed back') }])
    expect(api.getSection).toHaveBeenCalledTimes(1) // dirty, not "moved": no re-pull
    // the payload of the sanitised content comes with the collector's report of it
    expect(api.putSection).not.toHaveBeenCalled()
    ex.onSection({ key: 'hosts', hash: 'H2-clean', payload: { v: 2 } })
    await flush()
    expect(api.putSection).toHaveBeenCalledTimes(1)
    expect(api.putSection.mock.calls[0][3]).toMatchObject({ baseRev: 2, hash: 'H2-clean', payload: { v: 2 } })
  })
})

/* ─── the shape of what a pull fetched (spec §4.4 / §4.5) ─── */

describe('executor — a pulled section carries its shape, and a newer one locks the WHOLE profile', () => {
  const remote = (section: string, rev: number, hash: string | null) => ({ hostId: HOST, profileId: PROFILE, section, rev, hash, writerClientId: OTHER_CLIENT })

  it.each([
    ['sot-is-newer', ['fp-newer', 99] as [string, number]],
    ['shape-changed-without-ordinal', ['fp-other', 3] as [string, number]],
  ])('%s, learnt from a remote-event → pull: nothing is applied, the section is NOT locked:invalid, and no other section writes any more', async (verdict, shape) => {
    const { ex, problems, statuses } = await synced({ hosts: 'H1', settings: 'S1', workspaces: 'W1' })
    // what a newer Purdex wrote: another fingerprint, and a store this build does not know
    api.getSection.mockResolvedValue(sectionOf(meta('settings', 2, 'S2', shape), { 'purdex-from-the-future': { x: 1 } }))
    applySectionToStores.mockResolvedValue({ ok: false, reason: 'invalid', detail: 'unknown store' })
    api.putSection.mockResolvedValue({ kind: 'applied', rev: 2 })
    ex.onRemoteEvent(remote('settings', 2, 'S2'))
    await flush()

    expect(applySectionToStores).not.toHaveBeenCalled()
    expect(eventsOf('locked', 'pull-applied')).toEqual([])
    expect(ex.status().profile).toBe('locked:schema')
    expect(ex.status().schemaLock).toEqual({
      section: 'settings', kind: 'settings', verdict,
      mine: { fingerprint: 'fp-settings', ordinal: 3 }, sot: { fingerprint: shape[0], ordinal: shape[1] },
    })
    expect(ex.status().sections.settings).toBe('synced') // the SECTION is fine; the PROFILE is locked
    expect(problems.map((p) => p.kind)).toEqual(['schema-lock'])
    expect(statuses.at(-1)?.profile).toBe('locked:schema')

    // the bug the real machines showed: a local edit of ANOTHER section went out
    ex.onSection({ key: 'workspaces', hash: 'W2', payload: { order: [] } })
    ex.onSection({ key: 'hosts', hash: 'H2', payload: { hosts: {} } })
    await vi.advanceTimersByTimeAsync(120_000)
    expect(api.putSection).not.toHaveBeenCalled()
    expect(api.deleteSection).not.toHaveBeenCalled()
    expect(api.getSection).toHaveBeenCalledTimes(1) // and the refused payload is not fetched again
  })

  it('while locked, a remote-event is recorded (the section KNOWS) and sends nothing', async () => {
    const { ex } = await synced({ hosts: 'H1', settings: 'S1' })
    api.getSection.mockResolvedValue(sectionOf(meta('settings', 2, 'S2', ['fp-newer', 99]), {}))
    ex.onRemoteEvent(remote('settings', 2, 'S2'))
    await flush()
    vi.clearAllMocks()
    h.events.length = 0
    ex.onRemoteEvent(remote('hosts', 2, 'H2'))
    ex.onRemoteEvent(remote('settings', 3, 'S3'))
    await vi.advanceTimersByTimeAsync(120_000)
    expect(h.events.map((e) => [e.event.type, e.changed])).toEqual([['remote-event', true], ['remote-event', true]])
    expect(api.getSection).not.toHaveBeenCalled()
    expect(api.listProfiles).not.toHaveBeenCalled()
    expect(api.putSection).not.toHaveBeenCalled()
  })

  it('NO OSCILLATION: a lock set by a pull survives the next reindex (the index lists the same shape); it lifts only when the index no longer offends', async () => {
    const { ex, problems } = await synced({ hosts: 'H1', settings: 'S1' })
    api.getSection.mockResolvedValue(sectionOf(meta('settings', 2, 'S2', ['fp-newer', 99]), {}))
    ex.onRemoteEvent(remote('settings', 2, 'S2'))
    await flush()
    expect(ex.status().profile).toBe('locked:schema')

    api.listProfiles.mockResolvedValue(index([meta('hosts', 1, 'H1'), meta('settings', 2, 'S2', ['fp-newer', 99])]))
    api.putSection.mockResolvedValue({ kind: 'applied', rev: 2 })
    ex.onReconnected()
    ex.onSection({ key: 'hosts', hash: 'H2', payload: { hosts: {} } })
    await vi.advanceTimersByTimeAsync(120_000)
    expect(ex.status().schemaLock).toMatchObject({ section: 'settings', verdict: 'sot-is-newer' })
    expect(api.putSection).not.toHaveBeenCalled()
    expect(api.getSection).toHaveBeenCalledTimes(1)
    expect(problems.filter((p) => p.kind === 'schema-lock')).toHaveLength(1) // the same lock is not announced twice

    // this build was upgraded / the SOT was rewritten in a shape it knows
    api.listProfiles.mockResolvedValue(index([meta('hosts', 1, 'H1'), meta('settings', 2, 'S2')]))
    api.getSection.mockResolvedValue(sectionOf(meta('settings', 2, 'S2'), { s: 2 }))
    applySectionToStores.mockResolvedValue({ ok: true, hash: 'S2' })
    ex.onReconnected()
    await flush()
    expect(ex.status().schemaLock).toBeNull()
    expect(api.putSection).toHaveBeenCalledTimes(1)
    expect(ex.status().profile).toBe('synced')
  })

  it.each([
    ['ok (same fingerprint, the ordinal may differ)', ['fp-settings', 9] as [string, number]],
    ['i-am-newer (an OLDER shape: a pull only ever lands on a clean section — see the header)', ['fp-older', 2] as [string, number]],
  ])('%s → applied as usual, no lock', async (_name, shape) => {
    const { ex } = await synced({ hosts: 'H1', settings: 'S1' })
    api.getSection.mockResolvedValue(sectionOf(meta('settings', 2, 'S2', shape), { s: 2 }))
    applySectionToStores.mockResolvedValue({ ok: true, hash: 'S2' })
    ex.onRemoteEvent(remote('settings', 2, 'S2'))
    await flush()
    expect(applySectionToStores).toHaveBeenCalledWith('settings', { s: 2 }, { masterHostId: HOST })
    expect(ex.status()).toMatchObject({ profile: 'synced', schemaLock: null })
  })

  it('a write queued behind the pull that locks is not sent', async () => {
    const { ex } = await synced({ hosts: 'H1', settings: 'S1' })
    const get = deferred<Result<Section | null>>()
    api.getSection.mockReturnValue(get.promise)
    const put = deferred<PutOutcome>()
    api.putSection.mockReturnValueOnce(put.promise).mockResolvedValue({ kind: 'applied', rev: 9 })
    ex.onRemoteEvent(remote('settings', 2, 'S2'))
    ex.onSection({ key: 'hosts', hash: 'H2', payload: { hosts: {} } }) // on the wire before anyone knew
    await flush()
    get.resolve(sectionOf(meta('settings', 2, 'S2', ['fp-newer', 99]), {}))
    await flush()
    put.resolve(failure('server', { status: 500 })) // it would be retried in 2 s …
    await vi.advanceTimersByTimeAsync(120_000)
    expect(api.putSection).toHaveBeenCalledTimes(1) // … but the profile is locked
  })
})

/* ─── the empty placeholder of a workspace that arrived from elsewhere ─── */

describe('executor — an empty tabs placeholder is not an edit while the SOT has (or may have) content', () => {
  const remote = (section: string, rev: number, hash: string | null) => ({ hostId: HOST, profileId: PROFILE, section, rev, hash, writerClientId: OTHER_CLIENT })
  const EMPTY = { order: [], tabs: {} }
  const localChanges = (): unknown[] => h.events.filter((e) => e.event.type === 'local-changed').map((e) => e.event.hash)

  it('REGRESSION: workspaces applied, the tabs pull is slow, the collector reports the empty placeholder first → NO lock-conflict; synced once the pull lands', async () => {
    const { ex } = await synced({ hosts: 'H1', workspaces: 'W1' })
    api.listProfiles.mockResolvedValue(index([meta('hosts', 1, 'H1'), meta('workspaces', 2, 'W2'), meta('tabs.w9', 1, 'T1')]))
    const tabsGet = deferred<Result<Section | null>>()
    api.getSection.mockImplementation((_h, _p, section) =>
      section === 'workspaces' ? Promise.resolve(sectionOf(meta('workspaces', 2, 'W2'), { order: ['w9'] })) : tabsGet.promise,
    )
    applySectionToStores.mockImplementation(async (key) => {
      if (key === 'workspaces') useWorkspaceStore.setState({ workspaces: [ws('w9')] }) // an EMPTY workspace appears here
      return { ok: true, hash: key === 'workspaces' ? 'W2' : 'T1' }
    })
    ex.onRemoteEvent(remote('tabs.w9', 1, 'T1'))
    ex.onRemoteEvent(remote('workspaces', 2, 'W2'))
    await flush()
    expect(api.getSection.mock.calls.map((c) => c[2])).toEqual(['workspaces', 'tabs.w9']) // the tabs fetch is out, and slow
    ex.onSection({ key: 'tabs.w9', hash: 'E', payload: EMPTY }) // 500 ms later the collector sees the empty workspace
    await vi.advanceTimersByTimeAsync(5000)
    expect(localChanges()).toEqual([])
    expect(eventsOf('locked')).toEqual([])
    expect(ex.status().sections['tabs.w9']).toBe('synced')
    tabsGet.resolve(sectionOf(meta('tabs.w9', 1, 'T1'), { order: ['t1'], tabs: { t1: {} } }))
    await flush()
    expect(applySectionToStores).toHaveBeenCalledWith('tabs.w9', { order: ['t1'], tabs: { t1: {} } }, { masterHostId: HOST })
    expect(ex.status()).toMatchObject({ profile: 'synced', sections: { 'tabs.w9': 'synced' } })
    expect(api.putSection).not.toHaveBeenCalled()
  })

  it('the suppressed report still gets the section pumped: a pull that was waiting for its workspace goes out', async () => {
    const { ex } = await synced({ hosts: 'H1', workspaces: 'W1' })
    api.listProfiles.mockResolvedValue(index([meta('hosts', 1, 'H1'), meta('workspaces', 1, 'W1'), meta('tabs.w9', 1, 'T1')]))
    api.getSection.mockResolvedValue(sectionOf(meta('tabs.w9', 1, 'T1'), { order: ['t1'], tabs: { t1: {} } }))
    applySectionToStores.mockResolvedValue({ ok: true, hash: 'T1' })
    ex.onRemoteEvent(remote('tabs.w9', 1, 'T1'))
    await vi.advanceTimersByTimeAsync(10_000)
    expect(api.getSection).not.toHaveBeenCalled() // w9 is not here
    useWorkspaceStore.setState({ workspaces: [ws('w9')] }) // it appears without any event reaching the executor…
    ex.onSection({ key: 'tabs.w9', hash: 'E', payload: EMPTY }) // …except the collector's report of its emptiness
    await flush()
    expect(localChanges()).toEqual([])
    expect(api.getSection).toHaveBeenCalledTimes(1)
    expect(ex.status().sections['tabs.w9']).toBe('synced')
  })

  it('(i) an empty workspace created HERE (fresh index, not on the SOT) is a local creation: it is pushed with baseRev 0', async () => {
    useWorkspaceStore.setState({ workspaces: [ws('w5')] })
    const { ex } = await synced({ hosts: 'H1', workspaces: 'W1' })
    api.listProfiles.mockResolvedValue(index([meta('hosts', 1, 'H1'), meta('workspaces', 1, 'W1')]))
    api.putSection.mockResolvedValue({ kind: 'applied', rev: 1 })
    ex.onSection({ key: 'tabs.w5', hash: 'E', payload: EMPTY })
    await flush()
    expect(localChanges()).toEqual(['E'])
    expect(api.putSection).toHaveBeenCalledTimes(1)
    expect(api.putSection.mock.calls[0].slice(2, 4)).toEqual(['tabs.w5', expect.objectContaining({ baseRev: 0, hash: 'E', payload: EMPTY })])
    expect(ex.status().sections['tabs.w5']).toBe('synced')
  })

  it('(ii) reported BEFORE the index has landed: held (latest only); the SOT turns out to have content → dropped, pulled, no conflict', async () => {
    useWorkspaceStore.setState({ workspaces: [ws('w9')] })
    h.stored = { hosts: { base: { rev: 1, hash: 'H1' }, currentHash: 'H1' }, workspaces: { base: { rev: 1, hash: 'W1' }, currentHash: 'W1' } }
    const list = deferred<Result<ProfileIndexEntry[]>>()
    api.listProfiles.mockReturnValue(list.promise)
    api.getSection.mockResolvedValue(sectionOf(meta('tabs.w9', 3, 'T3'), { order: ['t1'], tabs: { t1: {} } }))
    applySectionToStores.mockResolvedValue({ ok: true, hash: 'T3' })
    const { ex } = make()
    ex.onReconnected()
    ex.onSection({ key: 'tabs.w9', hash: 'E', payload: EMPTY })
    await flush()
    expect(localChanges()).toEqual([]) // held, not dispatched
    list.resolve(index([meta('hosts', 1, 'H1'), meta('workspaces', 1, 'W1'), meta('tabs.w9', 3, 'T3')]))
    await flush()
    expect(localChanges()).toEqual([])
    expect(eventsOf('locked')).toEqual([])
    expect(applySectionToStores).toHaveBeenCalledWith('tabs.w9', { order: ['t1'], tabs: { t1: {} } }, { masterHostId: HOST })
    expect(ex.status()).toMatchObject({ profile: 'synced', sections: { 'tabs.w9': 'synced' } })
    expect(api.putSection).not.toHaveBeenCalled()
  })

  it('(ii) reported before the index has landed, and the SOT has NOTHING → the held report is delivered and pushed', async () => {
    useWorkspaceStore.setState({ workspaces: [ws('w5')] })
    h.stored = { hosts: { base: { rev: 1, hash: 'H1' }, currentHash: 'H1' }, workspaces: { base: { rev: 1, hash: 'W1' }, currentHash: 'W1' } }
    const list = deferred<Result<ProfileIndexEntry[]>>()
    api.listProfiles.mockReturnValueOnce(list.promise).mockResolvedValue(index([meta('hosts', 1, 'H1'), meta('workspaces', 1, 'W1')]))
    api.putSection.mockResolvedValue({ kind: 'applied', rev: 1 })
    const { ex } = make()
    ex.onReconnected()
    ex.onSection({ key: 'tabs.w5', hash: 'E', payload: EMPTY })
    await flush()
    expect(localChanges()).toEqual([])
    list.resolve(index([meta('hosts', 1, 'H1'), meta('workspaces', 1, 'W1')]))
    await flush()
    expect(localChanges()).toEqual(['E'])
    expect(api.putSection.mock.calls.map((c) => [c[2], c[3].baseRev, c[3].payload])).toEqual([['tabs.w5', 0, EMPTY]])
  })

  it('(ii) a held placeholder is superseded by a later real report of the same section', async () => {
    useWorkspaceStore.setState({ workspaces: [ws('w9')] })
    h.stored = { hosts: { base: { rev: 1, hash: 'H1' }, currentHash: 'H1' }, workspaces: { base: { rev: 1, hash: 'W1' }, currentHash: 'W1' } }
    const list = deferred<Result<ProfileIndexEntry[]>>()
    api.listProfiles.mockReturnValue(list.promise)
    const { ex } = make()
    ex.onReconnected()
    ex.onSection({ key: 'tabs.w9', hash: 'E', payload: EMPTY })
    ex.onSection({ key: 'tabs.w9', hash: 'T-mine', payload: { order: ['x'], tabs: { x: {} } } })
    list.resolve(index([meta('hosts', 1, 'H1'), meta('workspaces', 1, 'W1'), meta('tabs.w9', 3, 'T3')]))
    await flush()
    expect(localChanges()).toEqual(['T-mine']) // and never 'E' afterwards
    expect(ex.status().sections['tabs.w9']).toBe('locked:conflict') // a real edit against a live SOT
  })

  it('(iii) a tab opened in that workspace before the pull is a REAL edit: dispatched, and it conflicts', async () => {
    useWorkspaceStore.setState({ workspaces: [ws('w9')] })
    const { ex } = await synced({ hosts: 'H1', workspaces: 'W1' })
    api.listProfiles.mockResolvedValue(index([meta('hosts', 1, 'H1'), meta('workspaces', 1, 'W1'), meta('tabs.w9', 1, 'T1')]))
    api.getSection.mockReturnValue(new Promise(() => {}))
    ex.onRemoteEvent(remote('tabs.w9', 1, 'T1'))
    await flush()
    ex.onSection({ key: 'tabs.w9', hash: 'T-mine', payload: { order: ['x'], tabs: { x: {} } } })
    expect(localChanges()).toEqual(['T-mine'])
    // (its pull is still out; the lock is decided as soon as that action ends — here: never resolves, so check the state machine's view)
    expect(ex.status().sections['tabs.w9']).toBe('pending')
  })

  it('(iv) once the section has a base, an empty report is an ordinary report (the echo of an applied empty section changes nothing)', async () => {
    useWorkspaceStore.setState({ workspaces: [ws('w1')] })
    const { ex } = await synced({ hosts: 'H1', workspaces: 'W1', 'tabs.w1': 'T1' })
    api.getSection.mockResolvedValue(sectionOf(meta('tabs.w1', 2, 'E'), EMPTY))
    applySectionToStores.mockResolvedValue({ ok: true, hash: 'E' })
    ex.onRemoteEvent(remote('tabs.w1', 2, 'E'))
    await flush()
    h.events.length = 0
    ex.onSection({ key: 'tabs.w1', hash: 'E', payload: EMPTY })
    expect(h.events).toEqual([{ event: { type: 'local-changed', hash: 'E' }, changed: false }])
    // …and emptying a synced workspace is an edit like any other
    api.putSection.mockResolvedValue({ kind: 'applied', rev: 3 })
    ex.onRemoteEvent(remote('tabs.w1', 0, 'x')) // no-op
    const { ex: ex2 } = await synced({ hosts: 'H1', workspaces: 'W1', 'tabs.w1': 'T1' })
    api.putSection.mockResolvedValue({ kind: 'applied', rev: 2 })
    ex2.onSection({ key: 'tabs.w1', hash: 'E', payload: EMPTY })
    await flush()
    expect(api.putSection.mock.calls.at(-1)?.[3]).toMatchObject({ baseRev: 1, hash: 'E' })
  })
})

/* ─── restore-local, resolve ─── */

describe('executor — resolve and restore-local', () => {
  async function locked(): Promise<Harness> {
    const harness = await synced({ hosts: 'H1' })
    api.putSection.mockResolvedValueOnce({ kind: 'conflict', rev: 5, hash: 'H9', payload: { theirs: true } })
    harness.ex.onSection({ key: 'hosts', hash: 'H2', payload: { sent: true } })
    await flush()
    harness.ex.onSection({ key: 'hosts', hash: 'H3', payload: { later: true } })
    h.events.length = 0
    harness.problems.length = 0
    vi.clearAllMocks()
    return harness
  }

  it('keep local: the SENT snapshot is put back, then pushed over the SOT rev that conflicted', async () => {
    const { ex } = await locked()
    applySectionToStores.mockResolvedValue({ ok: true, hash: 'H2' })
    api.putSection.mockResolvedValue({ kind: 'applied', rev: 6 })
    ex.resolve('hosts', 'local')
    await flush()
    expect(applySectionToStores).toHaveBeenCalledWith('hosts', { sent: true }, { masterHostId: HOST })
    expect(eventsOf('local-restored')).toEqual([{ type: 'local-restored', hash: 'H2' }])
    expect(api.putSection.mock.calls[0][3]).toMatchObject({ baseRev: 5, hash: 'H2', payload: { sent: true } })
    expect(ex.status().profile).toBe('synced')
  })

  it('keep sot: the SOT is pulled over the dirty section', async () => {
    const { ex } = await locked()
    api.getSection.mockResolvedValue(sectionOf(meta('hosts', 5, 'H9'), { theirs: true }))
    applySectionToStores.mockResolvedValue({ ok: true, hash: 'H9' })
    ex.resolve('hosts', 'sot')
    await flush()
    expect(applySectionToStores).toHaveBeenCalledWith('hosts', { theirs: true }, { masterHostId: HOST })
    expect(ex.status().profile).toBe('synced')
  })

  it('an edit after keep-local cancels the restore: the snapshot is not written', async () => {
    const { ex } = await locked()
    applySectionToStores.mockResolvedValueOnce({ ok: false, reason: 'busy' })
    api.putSection.mockResolvedValue({ kind: 'applied', rev: 6 })
    ex.resolve('hosts', 'local')
    await flush()
    ex.onSection({ key: 'hosts', hash: 'H4', payload: { typed: true } }) // while the restore waits out `busy`
    await vi.advanceTimersByTimeAsync(10_000)
    expect(applySectionToStores).toHaveBeenCalledTimes(1)
    expect(eventsOf('local-restored')).toEqual([])
    expect(api.putSection.mock.calls[0][3]).toMatchObject({ hash: 'H4', baseRev: 5 })
  })

  it('PAYLOAD MISSING: local-restored is not dispatched, it is reported once, the section does not spin, and an edit frees it', async () => {
    h.stored = { hosts: { base: { rev: 1, hash: 'H1' }, currentHash: 'H3', conflict: { localHash: 'H2', sot: { rev: 5, hash: 'H9' } } } }
    const { ex, problems } = make() // a restart: the memory stash is empty, and the stored payload is gone too
    ex.resolve('hosts', 'local')
    await vi.advanceTimersByTimeAsync(600_000)
    expect(store.getStash).toHaveBeenCalledWith(PROFILE, 'H2')
    expect(store.getStash).toHaveBeenCalledTimes(1)
    expect(eventsOf('local-restored')).toEqual([])
    expect(applySectionToStores).not.toHaveBeenCalled()
    expect(problems.filter((p) => p.kind === 'restore-payload-missing')).toHaveLength(1)
    expect(ex.status().sections.hosts).toBe('pending')
    // the only way out the reducer offers: a local edit cancels the restore
    api.listProfiles.mockResolvedValue(index([meta('hosts', 5, 'H9')]))
    api.putSection.mockResolvedValue({ kind: 'applied', rev: 6 })
    ex.onSection({ key: 'hosts', hash: 'H4', payload: { typed: true } })
    await flush()
    expect(api.putSection.mock.calls[0][3]).toMatchObject({ hash: 'H4', baseRev: 5 })
  })

  it('after a restart the snapshot comes from the persisted stash, and the push that follows sends it', async () => {
    h.stored = { hosts: { base: { rev: 1, hash: 'H1' }, currentHash: 'H3', conflict: { localHash: 'H2', sot: { rev: 5, hash: 'H9' } } } }
    h.persistedStash.set('H2', { sent: true })
    api.listProfiles.mockResolvedValue(index([meta('hosts', 5, 'H9')]))
    applySectionToStores.mockResolvedValue({ ok: true, hash: 'H2' })
    api.putSection.mockResolvedValue({ kind: 'applied', rev: 6 })
    const { ex } = make()
    ex.resolve('hosts', 'local')
    await flush()
    expect(applySectionToStores).toHaveBeenCalledWith('hosts', { sent: true }, { masterHostId: HOST })
    expect(api.putSection.mock.calls[0][3]).toMatchObject({ baseRev: 5, hash: 'H2', payload: { sent: true } })
  })

  it('resolve on an unknown section is a no-op', () => {
    const { ex } = make()
    ex.resolve('settings', 'local')
    expect(h.events).toEqual([])
  })

  it('lock-conflict / lock-reset: the decision is dispatched as a `locked` event', async () => {
    h.stored = { hosts: { base: { rev: 5, hash: 'H1' }, currentHash: 'H2' }, settings: { base: { rev: 9, hash: 'S1' }, currentHash: 'S1' } }
    api.listProfiles.mockResolvedValue(index([meta('hosts', 6, 'H9'), meta('settings', 2, 'S0')]))
    const { ex } = make()
    ex.onReconnected()
    await flush()
    expect(eventsOf('locked')).toEqual([{ type: 'locked', reason: 'conflict' }, { type: 'locked', reason: 'reset' }])
    expect(ex.status()).toMatchObject({ profile: 'locked:reset', sections: { hosts: 'locked:conflict', settings: 'locked:reset' } })
    expect(ex.status().locks).toEqual({
      hosts: { status: 'locked:conflict', currentHash: 'H2', sot: { rev: 6, hash: 'H9' }, conflict: { localHash: 'H2', sot: { rev: 6, hash: 'H9' } } },
      settings: { status: 'locked:reset', currentHash: 'S1', sot: { rev: 2, hash: 'S0' }, conflict: null },
    })
  })
})

/* ─── lifecycle ─── */

describe('executor — lifecycle', () => {
  it('offline or autoSync off: nothing goes out', async () => {
    const { ex, env } = await synced({ hosts: 'H1' })
    env.reachable = false
    ex.onSection({ key: 'hosts', hash: 'H2', payload: {} })
    await vi.advanceTimersByTimeAsync(60_000)
    expect(api.putSection).not.toHaveBeenCalled()
    expect(ex.status().sections.hosts).toBe('pending')
  })

  it('syncNow sends the write while autoSync is off — and the next edit stays home again', async () => {
    const { ex, env } = await synced({ hosts: 'H1' })
    env.autoSync = false
    ex.onSection({ key: 'hosts', hash: 'H2', payload: { a: 1 } })
    await flush()
    expect(api.putSection).not.toHaveBeenCalled()
    api.putSection.mockResolvedValue({ kind: 'applied', rev: 2 })
    ex.syncNow()
    await flush()
    expect(api.putSection).toHaveBeenCalledTimes(1)
    expect(eventsOf(...TERMINAL).map((e) => e.type)).toEqual(['push-applied']) // its flight ended normally
    ex.onSection({ key: 'hosts', hash: 'H3', payload: { a: 2 } })
    await vi.advanceTimersByTimeAsync(60_000)
    expect(api.putSection).toHaveBeenCalledTimes(1)
  })

  it('syncNow carries a whole round: index first, then the write it uncovers', async () => {
    h.stored = { hosts: { base: { rev: 1, hash: 'H1' }, currentHash: 'H2' } }
    api.listProfiles.mockResolvedValue(index([meta('hosts', 1, 'H1')]))
    api.putSection.mockResolvedValue({ kind: 'applied', rev: 2 })
    const { ex, env } = make()
    env.autoSync = false
    ex.onSection({ key: 'hosts', hash: 'H2', payload: { a: 1 } })
    ex.onReconnected()
    await flush()
    expect(api.listProfiles).not.toHaveBeenCalled()
    ex.syncNow()
    await flush()
    expect(api.listProfiles).toHaveBeenCalledTimes(1)
    expect(api.putSection).toHaveBeenCalledTimes(1)
    ex.onSection({ key: 'hosts', hash: 'H3', payload: { a: 2 } })
    await vi.advanceTimersByTimeAsync(60_000)
    expect(api.putSection).toHaveBeenCalledTimes(1)
  })

  it('onReconnected: `reconnected` reaches EVERY section before the index is requested, and the backoff is reset', async () => {
    const { ex } = await synced({ hosts: 'H1', settings: 'S1' })
    api.listProfiles.mockResolvedValue(failure('network'))
    ex.onReconnected()
    await flush()
    expect(h.events.slice(0, 2).map((e) => e.event.type)).toEqual(['reconnected', 'reconnected'])
    expect(api.listProfiles).toHaveBeenCalledTimes(1)
    await vi.advanceTimersByTimeAsync(2000) // → second failure, next wait would be 4 s
    expect(api.listProfiles).toHaveBeenCalledTimes(2)
    ex.onReconnected() // resets: asks at once, then 2 s again
    await flush()
    expect(api.listProfiles).toHaveBeenCalledTimes(3)
    await vi.advanceTimersByTimeAsync(2000)
    expect(api.listProfiles).toHaveBeenCalledTimes(4)
  })

  it('an index answer that was requested before a reconnect is discarded, and asked for again', async () => {
    h.stored = { hosts: { base: { rev: 1, hash: 'H1' }, currentHash: 'H1' } }
    const stale = deferred<Result<ProfileIndexEntry[]>>()
    api.listProfiles.mockReturnValueOnce(stale.promise).mockResolvedValue(index([meta('hosts', 1, 'H1')]))
    const { ex } = make()
    ex.onReconnected()
    await flush()
    ex.onReconnected() // the connection dropped and came back while the request was out
    stale.resolve(index([meta('hosts', 9, 'OLD')]))
    await flush()
    expect(api.listProfiles).toHaveBeenCalledTimes(2)
    expect(eventsOf('sot-index').map((e) => e.epoch)).toEqual([1, 2])
    expect(api.getSection).not.toHaveBeenCalled() // the OLD view was never acted on
  })

  it('DISPOSE: an in-flight PUT that resolves afterwards dispatches nothing, persists nothing, leaves no timer', async () => {
    const { ex } = await synced({ hosts: 'H1', settings: 'S1' })
    const put = deferred<PutOutcome>()
    api.putSection.mockReturnValue(put.promise)
    api.getSection.mockReturnValue(new Promise(() => {}))
    ex.onSection({ key: 'hosts', hash: 'H2', payload: { a: 1 } })
    ex.onSection({ key: 'settings', hash: 'S2', payload: { b: 1 } }) // queued behind it
    await flush()
    const signal = api.putSection.mock.calls[0][4]?.signal
    h.events.length = 0
    vi.clearAllMocks()
    ex.dispose()
    expect(signal?.aborted).toBe(true)
    put.resolve({ kind: 'conflict', rev: 5, hash: 'H9', payload: { theirs: true } })
    await flush()
    await vi.advanceTimersByTimeAsync(600_000)
    expect(h.events).toEqual([])
    expect(store.saveSection).not.toHaveBeenCalled()
    expect(store.saveConflict).not.toHaveBeenCalled()
    expect(api.putSection).not.toHaveBeenCalled() // the queued write died with the executor
    expect(vi.getTimerCount()).toBe(0)
    // and every entry point is dead
    ex.onSection({ key: 'hosts', hash: 'H3', payload: {} })
    ex.onReconnected()
    ex.syncNow()
    ex.resolve('hosts', 'local')
    ex.onRemoteEvent({ hostId: HOST, profileId: PROFILE, section: 'hosts', rev: 9, hash: 'HX', writerClientId: OTHER_CLIENT })
    await flush()
    expect(h.events).toEqual([])
  })

  it('DISPOSE: a pending retry timer and a pending index retry are cancelled; a late pull and a late index are dropped', async () => {
    const { ex } = await synced({ hosts: 'H1', settings: 'S1' })
    api.putSection.mockResolvedValue(failure('server', { status: 500 }))
    ex.onSection({ key: 'hosts', hash: 'H2', payload: {} })
    await flush()
    expect(vi.getTimerCount()).toBe(1)
    const get = deferred<Result<Section | null>>()
    api.getSection.mockReturnValue(get.promise)
    ex.onRemoteEvent({ hostId: HOST, profileId: PROFILE, section: 'settings', rev: 2, hash: 'S2', writerClientId: OTHER_CLIENT })
    const list = deferred<Result<ProfileIndexEntry[]>>()
    api.listProfiles.mockReturnValue(list.promise)
    await flush()
    h.events.length = 0
    ex.dispose()
    expect(vi.getTimerCount()).toBe(0)
    get.resolve(sectionOf(meta('settings', 2, 'S2'), { s: 2 }))
    list.resolve(index([]))
    await vi.advanceTimersByTimeAsync(600_000)
    expect(applySectionToStores).not.toHaveBeenCalled()
    expect(h.events).toEqual([])
    expect(vi.getTimerCount()).toBe(0)
  })

  it('DISPOSE during an apply: the late outcome is not dispatched', async () => {
    const { ex } = await synced({ hosts: 'H1' })
    api.getSection.mockResolvedValue(sectionOf(meta('hosts', 2, 'H2'), { v: 2 }))
    const apply = deferred<ApplyOutcome>()
    applySectionToStores.mockReturnValue(apply.promise)
    ex.onRemoteEvent({ hostId: HOST, profileId: PROFILE, section: 'hosts', rev: 2, hash: 'H2', writerClientId: OTHER_CLIENT })
    await flush()
    h.events.length = 0
    vi.clearAllMocks()
    ex.dispose()
    apply.resolve({ ok: true, hash: 'H2' })
    await flush()
    expect(h.events).toEqual([])
    expect(store.saveSection).not.toHaveBeenCalled()
  })
})

/* ─── the first reconciliation: an attach has a direction ─── */

describe('executor — the first reconciliation (initialDirection)', () => {
  type Direction = 'push' | 'pull' | null
  const SEQUENCE = ['locked', 'resolved', 'push-started', 'push-applied', 'pull-applied']
  const sequence = (): string[] =>
    h.events
      .filter((e) => e.changed && SEQUENCE.includes(e.event.type))
      .map((e) => (e.event.type === 'resolved' ? `resolved:${String(e.event.keep)}` : e.event.type === 'locked' ? `locked:${String(e.event.reason)}` : e.event.type))

  function first(direction: Direction, over: Partial<ExecutorDeps> = {}): Harness & { settled: ReturnType<typeof vi.fn>; dir: { value: Direction } } {
    const dir = { value: direction }
    // what the start layer does in the callback: the direction is cleared
    const settled = vi.fn(() => void (dir.value = null))
    return { ...make({ initialDirection: () => dir.value, onInitialSettled: settled, ...over }), settled, dir }
  }

  /** A client that never agreed on `hosts` (H2 here) meets a SOT that holds H9 at rev 5. */
  function bothSidesHaveHosts(): void {
    api.listProfiles.mockResolvedValue(index([meta('hosts', 5, 'H9')]))
    api.getSection.mockResolvedValue(sectionOf(meta('hosts', 5, 'H9'), { theirs: true }))
    applySectionToStores.mockResolvedValue({ ok: true, hash: 'H9' })
    api.putSection.mockResolvedValue({ kind: 'applied', rev: 6 })
  }

  it('pull: a decide-time conflict is locked, resolved keep-SOT at once, and pulled — nothing is written', async () => {
    bothSidesHaveHosts()
    const { ex, settled } = first('pull')
    ex.onSection({ key: 'hosts', hash: 'H2', payload: { mine: true } })
    ex.onReconnected()
    await flush()
    expect(sequence()).toEqual(['locked:conflict', 'resolved:sot', 'pull-applied'])
    expect(api.putSection).not.toHaveBeenCalled()
    expect(applySectionToStores).toHaveBeenCalledWith('hosts', { theirs: true }, { masterHostId: HOST })
    expect(ex.status().sections).toEqual({ hosts: 'synced' })
    expect(settled).toHaveBeenCalledTimes(1)
  })

  it('push: the same conflict is resolved keep-local and pushed over the SOT rev — nothing is pulled or re-applied', async () => {
    bothSidesHaveHosts()
    const { ex, settled } = first('push')
    ex.onSection({ key: 'hosts', hash: 'H2', payload: { mine: true } })
    ex.onReconnected()
    await flush()
    expect(sequence()).toEqual(['locked:conflict', 'resolved:local', 'push-started', 'push-applied'])
    expect(api.putSection).toHaveBeenCalledTimes(1)
    expect(api.putSection.mock.calls[0][3]).toMatchObject({ baseRev: 5, hash: 'H2', payload: { mine: true } })
    expect(api.getSection).not.toHaveBeenCalled()
    expect(applySectionToStores).not.toHaveBeenCalled()
    expect(ex.status().sections).toEqual({ hosts: 'synced' })
    expect(settled).toHaveBeenCalledTimes(1)
  })

  it('no direction: exactly as before — the conflict is the user\'s, and nothing ever settles', async () => {
    bothSidesHaveHosts()
    const { ex, settled } = first(null)
    ex.onSection({ key: 'hosts', hash: 'H2', payload: { mine: true } })
    ex.onReconnected()
    await flush()
    expect(sequence()).toEqual(['locked:conflict'])
    expect(ex.status().sections).toEqual({ hosts: 'locked:conflict' })
    expect(settled).not.toHaveBeenCalled()
  })

  it.each([
    ['pull', ['locked:reset', 'resolved:sot', 'pull-applied']],
    ['push', ['locked:reset', 'resolved:local', 'push-started', 'push-applied']],
  ] as const)('%s: a reset (the SOT rev went backwards) is resolved the same way', async (direction, expected) => {
    h.stored = { hosts: { base: { rev: 4, hash: 'H1' }, currentHash: 'H1' } }
    api.listProfiles.mockResolvedValue(index([meta('hosts', 2, 'H9')]))
    api.getSection.mockResolvedValue(sectionOf(meta('hosts', 2, 'H9'), { theirs: true }))
    applySectionToStores.mockResolvedValue({ ok: true, hash: 'H9' })
    api.putSection.mockResolvedValue({ kind: 'applied', rev: 3 })
    const { ex } = first(direction)
    ex.onSection({ key: 'hosts', hash: 'H1', payload: { mine: true } })
    ex.onReconnected()
    await flush()
    expect(sequence()).toEqual(expected)
    if (direction === 'push') expect(api.putSection.mock.calls[0][3]).toMatchObject({ baseRev: 2, hash: 'H1' })
  })

  /** `hosts` agreed at rev 1; `settings` is still being pushed, so the period is open. Returns the settings PUT. */
  async function openPeriod(direction: 'push' | 'pull'): Promise<Harness & { settingsPut: Deferred<PutOutcome> }> {
    h.stored = { hosts: { base: { rev: 1, hash: 'H1' }, currentHash: 'H1' } }
    api.listProfiles.mockResolvedValue(index([meta('hosts', 1, 'H1')]))
    const settingsPut = deferred<PutOutcome>()
    api.putSection.mockReturnValueOnce(settingsPut.promise)
    const harness = first(direction)
    harness.ex.onSection({ key: 'hosts', hash: 'H1', payload: { a: 1 } })
    harness.ex.onSection({ key: 'settings', hash: 'S1', payload: { b: 1 } })
    harness.ex.onReconnected()
    await flush()
    expect(api.putSection).toHaveBeenCalledTimes(1)
    return { ...harness, settingsPut }
  }

  it('a 409 during the first reconciliation is resolved too (pull)', async () => {
    const { ex, settingsPut } = await openPeriod('pull')
    api.putSection.mockResolvedValue({ kind: 'conflict', rev: 5, hash: 'H9', payload: { theirs: true } })
    api.getSection.mockResolvedValue(sectionOf(meta('hosts', 5, 'H9'), { theirs: true }))
    applySectionToStores.mockResolvedValue({ ok: true, hash: 'H9' })
    ex.onSection({ key: 'hosts', hash: 'H2', payload: { mine: true } })
    settingsPut.resolve({ kind: 'applied', rev: 1 })
    await flush()
    expect(ex.status().sections).toEqual({ hosts: 'synced', settings: 'synced' })
    expect(eventsOf('resolved')).toEqual([{ type: 'resolved', keep: 'sot' }])
  })

  it('push: a 409 whose sent snapshot is no longer what the stores hold is left to the user (keep-local would undo the newer edit)', async () => {
    const { ex, settingsPut } = await openPeriod('push')
    const put = deferred<PutOutcome>()
    api.putSection.mockReturnValueOnce(put.promise)
    ex.onSection({ key: 'hosts', hash: 'H2', payload: { mine: true } })
    settingsPut.resolve({ kind: 'applied', rev: 1 })
    await flush()
    ex.onSection({ key: 'hosts', hash: 'H3', payload: { mine: 'later' } })
    put.resolve({ kind: 'conflict', rev: 5, hash: 'H9', payload: { theirs: true } })
    await flush()
    expect(ex.status().sections.hosts).toBe('locked:conflict')
    expect(eventsOf('resolved')).toEqual([])
  })

  it.each(['pull', 'push'] as const)('%s: locked:invalid is NOT resolved by a direction, and nothing settles while it stands', async (direction) => {
    h.stored = { hosts: { base: { rev: 1, hash: 'H1' }, currentHash: 'H1' } }
    api.listProfiles.mockResolvedValue(index([meta('hosts', 2, 'H9')]))
    api.getSection.mockResolvedValue(sectionOf(meta('hosts', 2, 'H9'), { theirs: true }))
    applySectionToStores.mockResolvedValue({ ok: false, reason: 'invalid', detail: 'removes the master host' })
    const { ex, settled } = first(direction)
    ex.onSection({ key: 'hosts', hash: 'H1', payload: { mine: true } })
    ex.onReconnected()
    await flush()
    await vi.advanceTimersByTimeAsync(120_000)
    expect(ex.status().sections).toEqual({ hosts: 'locked:invalid' })
    expect(eventsOf('resolved')).toEqual([])
    expect(api.putSection).not.toHaveBeenCalled()
    expect(settled).not.toHaveBeenCalled()
  })

  it('a conflict restored from the section store is resolved when the host connects', async () => {
    h.stored = { hosts: { base: { rev: 4, hash: 'H1' }, currentHash: 'H2', conflict: { localHash: 'H2', sot: { rev: 5, hash: 'H9' } } } }
    bothSidesHaveHosts()
    const { ex, settled } = first('pull')
    expect(ex.status().sections).toEqual({ hosts: 'locked:conflict' })
    ex.onReconnected()
    await flush()
    expect(ex.status().sections).toEqual({ hosts: 'synced' })
    expect(settled).toHaveBeenCalledTimes(1)
  })

  describe('onInitialSettled', () => {
    it('exactly once, and only when nothing is pending, in flight or queued', async () => {
      api.listProfiles.mockResolvedValue(index([]))
      const hostsPut = deferred<PutOutcome>()
      const settingsPut = deferred<PutOutcome>()
      api.putSection.mockReturnValueOnce(hostsPut.promise).mockReturnValueOnce(settingsPut.promise)
      const { ex, settled } = first('push')
      ex.onSection({ key: 'hosts', hash: 'H1', payload: { a: 1 } })
      ex.onSection({ key: 'settings', hash: 'S1', payload: { b: 1 } })
      ex.onReconnected()
      await flush()
      expect(settled).not.toHaveBeenCalled() // hosts in flight, settings queued

      hostsPut.resolve({ kind: 'applied', rev: 1 })
      await flush()
      expect(ex.status().sections.hosts).toBe('synced')
      expect(settled).not.toHaveBeenCalled() // settings in flight

      settingsPut.resolve({ kind: 'applied', rev: 1 })
      await flush()
      expect(settled).toHaveBeenCalledTimes(1)

      // more work afterwards is ordinary work: still once
      api.putSection.mockResolvedValue({ kind: 'applied', rev: 2 })
      ex.onSection({ key: 'hosts', hash: 'H2', payload: { a: 2 } })
      await flush()
      expect(settled).toHaveBeenCalledTimes(1)
    })

    it('an empty SOT + pull: nothing to take, the ordinary rules push, and it settles', async () => {
      api.listProfiles.mockResolvedValue(index([]))
      api.putSection.mockResolvedValue({ kind: 'applied', rev: 1 })
      const { ex, settled } = first('pull')
      ex.onSection({ key: 'hosts', hash: 'H1', payload: { a: 1 } })
      ex.onSection({ key: 'workspaces', hash: 'W1', payload: { b: 1 } })
      ex.onReconnected()
      await flush()
      expect(api.putSection).toHaveBeenCalledTimes(2)
      expect(ex.status().profile).toBe('synced')
      expect(settled).toHaveBeenCalledTimes(1)
    })

    it('a section that is clean but BEHIND reads `synced` — and does not settle until its pull has landed', async () => {
      h.stored = { hosts: { base: { rev: 1, hash: 'H1' }, currentHash: 'H1' } }
      api.listProfiles.mockResolvedValue(index([meta('hosts', 2, 'H9')]))
      const get = deferred<Result<Section | null>>()
      api.getSection.mockReturnValue(get.promise)
      applySectionToStores.mockResolvedValue({ ok: true, hash: 'H9' })
      const { ex, settled } = first('pull')
      ex.onSection({ key: 'hosts', hash: 'H1', payload: { a: 1 } })
      ex.onReconnected()
      await flush()
      expect(ex.status().sections).toEqual({ hosts: 'synced' })
      expect(settled).not.toHaveBeenCalled()
      get.resolve(sectionOf(meta('hosts', 2, 'H9'), { theirs: true }))
      await flush()
      expect(settled).toHaveBeenCalledTimes(1)
    })

    it('not before an index has been SEEN: offline, or a failed list, settles nothing — and the next connect carries on', async () => {
      const { ex, settled, env } = await (async () => {
        h.stored = { hosts: { base: { rev: 1, hash: 'H1' }, currentHash: 'H1' } }
        return first('pull')
      })()
      env.reachable = false
      ex.onSection({ key: 'hosts', hash: 'H1', payload: { a: 1 } })
      ex.onReconnected()
      await flush()
      await vi.advanceTimersByTimeAsync(600_000)
      expect(api.listProfiles).not.toHaveBeenCalled()
      expect(settled).not.toHaveBeenCalled()

      env.reachable = true
      api.listProfiles.mockResolvedValueOnce(failure('network'))
      ex.onReconnected()
      await flush()
      expect(settled).not.toHaveBeenCalled()

      api.listProfiles.mockResolvedValue(index([meta('hosts', 1, 'H1')]))
      ex.onReconnected()
      await flush()
      expect(settled).toHaveBeenCalledTimes(1)
    })

    it('ONE period per executor. A direction that shows up after it (attack B: same master attached again, everything already synced, NO network action) is stale: reported, handed back to be cleared — and answers no lock', async () => {
      api.listProfiles.mockResolvedValue(index([meta('hosts', 1, 'H1')]))
      h.stored = { hosts: { base: { rev: 1, hash: 'H1' }, currentHash: 'H1' } }
      const { ex, settled, dir, problems } = first('pull')
      ex.onSection({ key: 'hosts', hash: 'H1', payload: { a: 1 } })
      ex.onReconnected()
      await flush()
      expect(settled).toHaveBeenCalledTimes(1)
      expect(dir.value).toBeNull()

      vi.clearAllMocks()
      dir.value = 'push' // set again, on a driver that was not rebuilt
      ex.syncNow() // all the start layer would do; every section is synced, so nothing goes out
      await flush()
      expect(api.listProfiles).not.toHaveBeenCalled()
      expect(api.putSection).not.toHaveBeenCalled()
      expect(settled).toHaveBeenCalledTimes(1) // counted from the clearAllMocks above: the second call in all
      expect(dir.value).toBeNull()
      expect(problems.filter((p) => p.kind === 'stale-direction')).toHaveLength(1)
    })

    it('a stale direction that is NOT cleared answers no lock, hours later or ever; it is reported once per appearance', async () => {
      api.listProfiles.mockResolvedValue(index([meta('hosts', 1, 'H1')]))
      h.stored = { hosts: { base: { rev: 1, hash: 'H1' }, currentHash: 'H1' } }
      const settled = vi.fn() // a start layer that fails to clear it
      const { ex, problems } = first('pull', { onInitialSettled: settled })
      ex.onSection({ key: 'hosts', hash: 'H1', payload: { a: 1 } })
      ex.onReconnected()
      await flush()
      expect(settled).toHaveBeenCalledTimes(2) // settled, then: still there → stale, handed back once more

      api.putSection.mockResolvedValue({ kind: 'conflict', rev: 5, hash: 'H9', payload: { theirs: true } })
      ex.onSection({ key: 'hosts', hash: 'H2', payload: { mine: true } })
      await flush()
      await vi.advanceTimersByTimeAsync(3_600_000)
      expect(ex.status().sections).toEqual({ hosts: 'locked:conflict' }) // the user's
      expect(eventsOf('resolved')).toEqual([])
      expect(api.getSection).not.toHaveBeenCalled()
      expect(settled).toHaveBeenCalledTimes(2)
      expect(problems.filter((p) => p.kind === 'stale-direction')).toHaveLength(1)
    })

    it('an executor born without a direction has no period: one that appears later is stale from the start', async () => {
      bothSidesHaveHosts()
      const { ex, settled, dir, problems } = first(null)
      dir.value = 'pull'
      ex.onSection({ key: 'hosts', hash: 'H2', payload: { mine: true } })
      ex.onReconnected()
      await flush()
      expect(ex.status().sections).toEqual({ hosts: 'locked:conflict' })
      expect(eventsOf('resolved')).toEqual([])
      expect(settled).toHaveBeenCalledTimes(1)
      expect(problems.filter((p) => p.kind === 'stale-direction')).toHaveLength(1)
    })

    it('works without an onInitialSettled listener: the period still ends', async () => {
      api.listProfiles.mockResolvedValue(index([meta('hosts', 1, 'H1')]))
      h.stored = { hosts: { base: { rev: 1, hash: 'H1' }, currentHash: 'H1' } }
      const { ex } = make({ initialDirection: () => 'pull' })
      ex.onSection({ key: 'hosts', hash: 'H1', payload: { a: 1 } })
      ex.onReconnected()
      await flush()
      api.putSection.mockResolvedValue({ kind: 'conflict', rev: 5, hash: 'H9', payload: { theirs: true } })
      ex.onSection({ key: 'hosts', hash: 'H2', payload: { mine: true } })
      await flush()
      expect(ex.status().sections).toEqual({ hosts: 'locked:conflict' })
    })

    it('a schema lock settles nothing', async () => {
      h.stored = { hosts: { base: { rev: 1, hash: 'H1' }, currentHash: 'H1' } }
      api.listProfiles.mockResolvedValue(index([meta('hosts', 1, 'H1', ['fp-future', 9])]))
      const { ex, settled } = first('pull')
      ex.onSection({ key: 'hosts', hash: 'H1', payload: { a: 1 } })
      ex.onReconnected()
      await flush()
      expect(ex.status().schemaLock).not.toBeNull()
      expect(settled).not.toHaveBeenCalled()
    })
  })

  describe('push: `tabs.*` of workspaces that are not here are deleted from the SOT', () => {
    /** Local: hosts, workspaces, tabs.w1 — all already equal to the SOT. The SOT also lists `others`. */
    function mineAgrees(others: SectionMeta[]): void {
      useWorkspaceStore.setState({ workspaces: [ws('w1')] })
      api.listProfiles.mockResolvedValue(index([meta('hosts', 1, 'H1'), meta('workspaces', 1, 'W1'), meta('tabs.w1', 1, 'T1'), ...others]))
    }
    function reportMine(ex: Executor): void {
      ex.onSection({ key: 'hosts', hash: 'H1', payload: { a: 1 } })
      ex.onSection({ key: 'workspaces', hash: 'W1', payload: { b: 1 } })
      ex.onSection({ key: 'tabs.w1', hash: 'T1', payload: { order: ['t'], tabs: { t: {} } } })
    }

    it('deletes it with the index rev as baseRev, forgets it, and settles; an unknown kind is carried', async () => {
      mineAgrees([meta('tabs.w7', 3, 'T7'), meta('gizmo.x', 2, 'G1')])
      api.deleteSection.mockResolvedValue({ kind: 'applied', rev: 4 })
      const { ex, settled, problems } = first('push')
      reportMine(ex)
      ex.onReconnected()
      await flush()
      expect(api.deleteSection).toHaveBeenCalledTimes(1)
      expect(api.deleteSection).toHaveBeenCalledWith(HOST, PROFILE, 'tabs.w7', { baseRev: 3, clientId: h.clientId }, expect.anything())
      expect(Object.keys(ex.status().sections).sort()).toEqual(['hosts', 'tabs.w1', 'workspaces'])
      expect(settled).toHaveBeenCalledTimes(1)
      expect(problems.filter((p) => p.kind.startsWith('orphan'))).toEqual([])
    })

    it.each(['pull', null] as const)('direction %s: nothing is deleted', async (direction) => {
      mineAgrees([meta('tabs.w7', 3, 'T7')])
      const { ex } = first(direction)
      reportMine(ex)
      ex.onReconnected()
      await flush()
      expect(api.deleteSection).not.toHaveBeenCalled()
    })

    it('goes through the write queue, and only once `workspaces` is up to date', async () => {
      useWorkspaceStore.setState({ workspaces: [ws('w1')] })
      api.listProfiles.mockResolvedValue(index([meta('hosts', 1, 'H1'), meta('workspaces', 2, 'W-theirs'), meta('tabs.w7', 3, 'T7')]))
      const put = deferred<PutOutcome>()
      api.putSection.mockReturnValue(put.promise)
      api.deleteSection.mockResolvedValue({ kind: 'applied', rev: 4 })
      const { ex, settled } = first('push')
      ex.onSection({ key: 'hosts', hash: 'H1', payload: { a: 1 } })
      ex.onSection({ key: 'workspaces', hash: 'W1', payload: { b: 1 } })
      ex.onReconnected()
      await flush()
      expect(api.putSection).toHaveBeenCalledTimes(1) // workspaces, keep-local over rev 2
      expect(api.deleteSection).not.toHaveBeenCalled()
      expect(settled).not.toHaveBeenCalled()
      put.resolve({ kind: 'applied', rev: 3 })
      await flush()
      expect(api.deleteSection).toHaveBeenCalledTimes(1)
      expect(settled).toHaveBeenCalledTimes(1)
    })

    it('a 409 is a problem and is NOT retried — not by the next index either; the rest still settles', async () => {
      mineAgrees([meta('tabs.w7', 3, 'T7')])
      api.deleteSection.mockResolvedValue({ kind: 'conflict', rev: 4, hash: 'T8', payload: { order: [], tabs: {} } })
      const { ex, settled, problems } = first('push')
      reportMine(ex)
      ex.onReconnected()
      await flush()
      ex.onReconnected()
      await flush()
      await vi.advanceTimersByTimeAsync(120_000)
      expect(api.deleteSection).toHaveBeenCalledTimes(1)
      expect(problems.filter((p) => p.kind === 'orphan-delete-conflict')).toHaveLength(1)
      expect(settled).toHaveBeenCalledTimes(1)
    })

    it('a delete that got no answer keeps the period open, and is tried again with the next index', async () => {
      mineAgrees([meta('tabs.w7', 3, 'T7')])
      api.deleteSection.mockResolvedValueOnce(failure('network'))
      const { ex, settled } = first('push')
      reportMine(ex)
      ex.onReconnected()
      await flush()
      await vi.advanceTimersByTimeAsync(120_000)
      expect(api.deleteSection).toHaveBeenCalledTimes(1) // no spinning between indexes
      expect(settled).not.toHaveBeenCalled()
      api.deleteSection.mockResolvedValue({ kind: 'applied', rev: 4 })
      ex.onReconnected()
      await flush()
      expect(api.deleteSection).toHaveBeenCalledTimes(2)
      expect(settled).toHaveBeenCalledTimes(1)
    })

    it('not sent without the lease', async () => {
      mineAgrees([meta('tabs.w7', 3, 'T7')])
      const { ex, env } = first('push')
      env.leader = false
      reportMine(ex)
      ex.onReconnected()
      await flush()
      expect(api.deleteSection).not.toHaveBeenCalled()
    })
  })

  it('pull: a local `tabs.*` is not pushed while `workspaces` is still behind — the workspace may be about to go', async () => {
    useWorkspaceStore.setState({ workspaces: [ws('w9')] })
    api.listProfiles.mockResolvedValue(index([meta('hosts', 1, 'H1'), meta('workspaces', 2, 'W-theirs')]))
    const get = deferred<Result<Section | null>>()
    api.getSection.mockReturnValue(get.promise)
    api.putSection.mockResolvedValue({ kind: 'applied', rev: 1 })
    applySectionToStores.mockImplementation(async () => {
      useWorkspaceStore.setState({ workspaces: [] })
      return { ok: true, hash: 'W-theirs' }
    })
    const { ex, settled } = first('pull')
    ex.onSection({ key: 'hosts', hash: 'H1', payload: { a: 1 } })
    ex.onSection({ key: 'workspaces', hash: 'W-mine', payload: { b: 1 } })
    ex.onSection({ key: 'tabs.w9', hash: 'T9', payload: { order: ['t'], tabs: { t: {} } } })
    ex.onReconnected()
    await flush()
    expect(api.putSection).not.toHaveBeenCalled()
    get.resolve(sectionOf(meta('workspaces', 2, 'W-theirs'), { theirs: true }))
    await flush()
    ex.onSection({ key: 'tabs.w9', hash: null, payload: null }) // the collector, after the workspace went
    await flush()
    expect(api.putSection).not.toHaveBeenCalled()
    expect(api.deleteSection).not.toHaveBeenCalled()
    expect(settled).toHaveBeenCalledTimes(1)
  })
})

/* ─── a device-local workspace (an id that cannot form `tabs.<id>`) ─── */

describe('executor — a workspace whose id cannot be synced is not the reconcile\'s business', () => {
  it('the section set is still reconciled: unrendered and unknown sections are reported, nothing fails', async () => {
    useWorkspaceStore.setState({ workspaces: [ws('w1'), ws('not a valid id!')] })
    api.listProfiles.mockResolvedValue(index([meta('hosts', 1, 'H1'), meta('tabs.w7', 3, 'T7'), meta('gizmo.x', 2, 'G1')]))
    const { ex, problems } = make()
    ex.onSection({ key: 'hosts', hash: 'H1', payload: { a: 1 } })
    ex.onReconnected()
    await flush()
    expect(problems.filter((p) => p.kind === 'reconcile-failed' || p.kind === 'executor-error')).toEqual([])
    expect(problems.filter((p) => p.kind === 'sections-unrendered').map((p) => p.detail)).toEqual(['tabs.w7'])
    expect(problems.filter((p) => p.kind === 'sections-unknown-kind').map((p) => p.detail)).toEqual(['gizmo.x'])
  })

  it('…also when the unsyncable one was there BEFORE a `workspaces` pull (the previous set)', async () => {
    useWorkspaceStore.setState({ workspaces: [ws('not a valid id!')] })
    h.stored = { workspaces: { base: { rev: 1, hash: 'W1' }, currentHash: 'W1' } }
    api.listProfiles.mockResolvedValue(index([meta('workspaces', 2, 'W2'), meta('tabs.w7', 3, 'T7')]))
    api.getSection.mockResolvedValue(sectionOf(meta('workspaces', 2, 'W2'), { theirs: true }))
    applySectionToStores.mockImplementation(async () => {
      useWorkspaceStore.setState({ workspaces: [ws('w1')] }) // the applier would keep the device-local one; irrelevant here
      return { ok: true, hash: 'W2' }
    })
    const { ex, problems } = make()
    ex.onSection({ key: 'workspaces', hash: 'W1', payload: { a: 1 } })
    ex.onReconnected()
    await flush()
    api.listProfiles.mockResolvedValue(index([meta('workspaces', 2, 'W2'), meta('tabs.w7', 3, 'T7'), meta('gizmo.x', 2, 'G1')]))
    ex.onReconnected()
    await flush()
    expect(problems.filter((p) => p.kind === 'reconcile-failed')).toEqual([])
    expect(problems.some((p) => p.kind === 'sections-unknown-kind')).toBe(true)
  })
})

/* ─── no request starts while the host is unreachable (critic C-2) ─── */

describe('executor — `isReachable()` is asked again where the request is MADE, not only where it is decided', () => {
  // The start layer keeps the executor "unreachable" until the daemon has confirmed the attachment, and makes
  // it unreachable again when the connection drops — WITHOUT disposing it. Whatever was scheduled before
  // (behind `await shapes()`, in the write queue, on a backoff timer) must not go out afterwards.
  type ApiName = 'listProfiles' | 'getSection' | 'putSection' | 'deleteSection'

  interface Watched extends Harness {
    /** Every api call that STARTED, with what `isReachable()` said at that moment. */
    started: Array<{ fn: string; reachable: boolean }>
    /** The answers go through here, so that the recording wrapper is never replaced by a `mockResolvedValue`. */
    answer(name: ApiName, value: unknown): void
    answerOnce(name: ApiName, value: unknown): void
  }

  function watch(harness: Harness): Watched {
    const started: Watched['started'] = []
    const always: Partial<Record<ApiName, unknown>> = {}
    const once: Partial<Record<ApiName, unknown[]>> = {}
    for (const name of ['listProfiles', 'getSection', 'putSection', 'deleteSection'] as const) {
      const mock = api[name] as unknown as ReturnType<typeof vi.fn<(...args: unknown[]) => unknown>>
      mock.mockReset()
      mock.mockImplementation(() => {
        started.push({ fn: name, reachable: harness.env.reachable })
        const queued = once[name]
        const value = queued !== undefined && queued.length > 0 ? queued.shift() : name in always ? always[name] : failure('network')
        return Promise.resolve(value)
      })
    }
    return {
      ...harness,
      started,
      answer: (name, value) => void (always[name] = value),
      answerOnce: (name, value) => void (once[name] = [...(once[name] ?? []), value]),
    }
  }

  const never = (w: Watched): void => expect(w.started.filter((c) => !c.reachable)).toEqual([])

  it('(i) reindex — the link drops while the shapes are being computed: no list request; the next connect asks', async () => {
    const w = watch(make())
    w.answer('listProfiles', index([meta('hosts', 1, 'H1')]))
    w.ex.onSection({ key: 'hosts', hash: 'H1', payload: { a: 1 } })
    w.ex.onReconnected() // → reindex → `await shapes()`
    w.env.reachable = false
    await flush()
    await vi.advanceTimersByTimeAsync(120_000) // and no busy retry either
    expect(api.listProfiles).not.toHaveBeenCalled()
    never(w)

    w.env.reachable = true
    w.ex.onReconnected()
    await flush()
    expect(api.listProfiles).toHaveBeenCalledTimes(1)
    expect(w.ex.status().sections).toEqual({ hosts: 'synced' })
  })

  it('(i) pull — decided while reachable, the link drops before the GET: not sent, nothing dispatched; the next connect pulls', async () => {
    const again = watch(await synced({ hosts: 'H1' }))
    again.answer('getSection', sectionOf(meta('hosts', 2, 'H9'), { theirs: true }))
    applySectionToStores.mockResolvedValue({ ok: true, hash: 'H9' })
    again.ex.onRemoteEvent({ hostId: HOST, profileId: PROFILE, section: 'hosts', rev: 2, hash: 'H9', writerClientId: OTHER_CLIENT })
    again.env.reachable = false
    await flush()
    await vi.advanceTimersByTimeAsync(120_000)
    expect(api.getSection).not.toHaveBeenCalled()
    expect(eventsOf('pull-applied', 'locked')).toEqual([])
    never(again)

    again.env.reachable = true
    again.answer('listProfiles', index([meta('hosts', 2, 'H9')]))
    again.ex.onReconnected()
    await flush()
    expect(api.getSection).toHaveBeenCalledTimes(1)
    expect(eventsOf('pull-applied')).toHaveLength(1)
  })

  it.each([
    ['put', { key: 'hosts', hash: 'H2', payload: { mine: true } }, 'putSection'],
    ['delete', { key: 'tabs.w1', hash: null, payload: null }, 'deleteSection'],
  ] as const)('(i) %s — the link drops before the write goes out: not sent, and the flight that was opened is CLOSED', async (_kind, report, fn) => {
    const again = watch(await synced({ hosts: 'H1', 'tabs.w1': 'T1' }))
    again.answer(fn, { kind: 'applied', rev: 2 })
    again.ex.onSection(report)
    again.env.reachable = false
    await flush()
    await vi.advanceTimersByTimeAsync(120_000)
    expect(api[fn]).not.toHaveBeenCalled()
    never(again)
    const opened = eventsOf('push-started').length
    expect(h.events.filter((e) => TERMINAL.includes(e.event.type) && e.changed)).toHaveLength(opened) // none left hanging
    expect(eventsOf('push-applied')).toEqual([])

    again.env.reachable = true
    again.answer('listProfiles', index([meta('hosts', 1, 'H1'), meta('tabs.w1', 1, 'T1')]))
    again.ex.onReconnected()
    await flush()
    expect(api[fn]).toHaveBeenCalledTimes(1)
  })

  it('(ii) the write queue — the link drops while a write waits its turn: the one on the wire ends normally, the waiting one is not sent', async () => {
    const again = watch(await synced({ hosts: 'H1', settings: 'S1' }))
    const first = deferred<PutOutcome>()
    again.answerOnce('putSection', first.promise)
    again.answer('putSection', { kind: 'applied', rev: 2 })
    again.ex.onSection({ key: 'hosts', hash: 'H2', payload: { a: 2 } })
    again.ex.onSection({ key: 'settings', hash: 'S2', payload: { b: 2 } })
    await flush()
    expect(api.putSection).toHaveBeenCalledTimes(1)
    again.env.reachable = false
    first.resolve({ kind: 'applied', rev: 2 })
    await flush()
    await vi.advanceTimersByTimeAsync(120_000)
    expect(api.putSection).toHaveBeenCalledTimes(1)
    never(again)
    expect(again.ex.status().sections).toEqual({ hosts: 'synced', settings: 'pending' })

    again.env.reachable = true
    again.answer('listProfiles', index([meta('hosts', 2, 'H2'), meta('settings', 1, 'S1')]))
    again.ex.onReconnected()
    await flush()
    expect(api.putSection).toHaveBeenCalledTimes(2)
    expect(again.ex.status().sections).toEqual({ hosts: 'synced', settings: 'synced' })
  })

  it('(ii) an orphan delete waiting in the queue is not sent either; the next index tries it again', async () => {
    useWorkspaceStore.setState({ workspaces: [ws('w1')] })
    const settingsPut = deferred<PutOutcome>()
    const w = watch(make({ initialDirection: () => 'push', onInitialSettled: () => {} }))
    // hosts and workspaces already agree with the SOT (the gates are up to date at once); `settings` is new and
    // its push holds the queue while the orphan waits behind it
    w.answer('listProfiles', index([meta('hosts', 1, 'H1'), meta('workspaces', 1, 'W1'), meta('tabs.w7', 3, 'T7')]))
    w.answerOnce('putSection', settingsPut.promise)
    w.answer('deleteSection', { kind: 'applied', rev: 4 })
    w.ex.onSection({ key: 'hosts', hash: 'H1', payload: { a: 1 } })
    w.ex.onSection({ key: 'workspaces', hash: 'W1', payload: { b: 1 } })
    w.ex.onSection({ key: 'settings', hash: 'S1', payload: { c: 1 } })
    w.ex.onReconnected()
    await flush()
    expect(api.putSection).toHaveBeenCalledTimes(1)
    expect(api.deleteSection).not.toHaveBeenCalled()

    w.env.reachable = false
    settingsPut.resolve({ kind: 'applied', rev: 1 })
    await flush()
    await vi.advanceTimersByTimeAsync(120_000)
    expect(api.deleteSection).not.toHaveBeenCalled()
    never(w)

    w.env.reachable = true
    w.answer('listProfiles', index([meta('hosts', 1, 'H1'), meta('workspaces', 1, 'W1'), meta('settings', 1, 'S1'), meta('tabs.w7', 3, 'T7')]))
    w.ex.onReconnected()
    await flush()
    expect(api.deleteSection).toHaveBeenCalledTimes(1)
  })

  it('(iii) a backoff timer that fires while unreachable sends nothing — section retry and index retry alike', async () => {
    const again = watch(await synced({ hosts: 'H1' }))
    again.ex.onSection({ key: 'hosts', hash: 'H2', payload: { a: 2 } })
    await flush()
    expect(api.putSection).toHaveBeenCalledTimes(1)
    again.env.reachable = false
    await vi.advanceTimersByTimeAsync(600_000)
    expect(api.putSection).toHaveBeenCalledTimes(1)
    never(again)

    // the index retry
    again.env.reachable = true
    again.ex.onReconnected()
    await flush()
    const lists = api.listProfiles.mock.calls.length
    again.env.reachable = false
    await vi.advanceTimersByTimeAsync(600_000)
    expect(api.listProfiles).toHaveBeenCalledTimes(lists)
    never(again)
  })
})
