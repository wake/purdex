// spa/src/lib/profile/tabs-local-only.integration.test.ts — interface-only tabs stay on the device
// (docs/specs/2026-09-23-tabs-local-only-spec.md), end to end: the REAL collector, section store,
// apply-to-stores and zustand stores on this side, and an in-memory daemon with P1's compare-and-set
// semantics (test-fake-daemon.ts). Only the network, the digest and `shapeTable` are replaced (as in
// executor.direction.integration.test.ts).
//
// One process plays both devices. Device A's first push is a real session; A's LATER writes are built
// with the real builders from A's world and written to the daemon as A's executor would, while B's
// session is live and receives them as remote events.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { useHostStore } from '../../stores/useHostStore'
import type { HostConfig } from '../../stores/useHostStore'
import { useTabStore } from '../../stores/useTabStore'
import { useWorkspaceStore } from '../../features/workspace/store'
import { useRebuildStore } from '../../stores/useRebuildStore'
import type { PaneContent, PaneLayout, Tab, Workspace } from '../../types/tab'
import { buildSectionPayload, startCollector, type Collector } from './collector'
import type { ProfileSectionKey } from './types'
import { createExecutor, type Executor } from './executor'
import { hashSection } from './hash'
import { buildTabsSection, buildWorkspacesSection } from './sections'
import { clearSectionStore } from './section-store'
import { FakeDaemon } from './test-fake-daemon'

const h = vi.hoisted(() => ({ clientId: 'c_aaaaaaaaaaaa' }))

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

const SHAPE = { hosts: ['fp-hosts', 1], settings: ['fp-settings', 3], workspaces: ['fp-workspaces', 1], tabs: ['fp-tabs', 1] } as const

vi.mock('./projections', async (importOriginal) => {
  const actual = await importOriginal<typeof import('./projections')>()
  return { ...actual, shapeTable: vi.fn(async () => SHAPE) }
})

vi.mock('../client-identity', () => ({ getClientId: () => h.clientId, isClientIdPersisted: () => true }))

const api = vi.mocked(await import('./api'))

const M = 'host-master'
const PROFILE = 'p_0123456789ab'
const A = 'c_aaaaaaaaaaaa'
const B = 'c_bbbbbbbbbbbb'

/* ─── the devices ─── */

function host(id: string): HostConfig {
  return { id, name: id, ip: '10.0.0.1', port: 7860, token: 'tok', order: 0 }
}

const TMUX = (code: string): PaneContent => ({ kind: 'tmux-session', hostId: M, sessionCode: code, mode: 'terminal', cachedName: code, tmuxInstance: 'inst' })
const SETTINGS: PaneContent = { kind: 'settings', scope: 'global' }
const NEW_TAB: PaneContent = { kind: 'new-tab' }

function leaf(paneId: string, content: PaneContent): PaneLayout {
  return { type: 'leaf', pane: { id: paneId, content } }
}

function tab(id: string, content: PaneContent = TMUX(`c-${id}`)): Tab {
  return { id, pinned: false, locked: false, createdAt: 1, layout: leaf(`p-${id}`, content) }
}

function ws(id: string, tabs: string[], activeTabId: string | null = tabs[0] ?? null): Workspace {
  return { id, name: id.toUpperCase(), tabs, activeTabId }
}

function world(workspaces: Workspace[], tabs: Tab[]): void {
  useHostStore.setState({ hosts: { [M]: host(M) }, hostOrder: [M], activeHostId: M, runtime: {} })
  useTabStore.setState({ tabs: Object.fromEntries(tabs.map((t) => [t.id, t])), tabOrder: tabs.map((t) => t.id), activeTabId: null, visitHistory: [] })
  useWorkspaceStore.setState({ workspaces, activeWorkspaceId: workspaces[0]?.id ?? null })
  useRebuildStore.setState({ operations: {}, lockedBy: null, lockGrant: null })
}

/** A local edit, as the tab bar makes it: the tab record and the workspace's listing, in one go. */
function putTab(wsId: string, t: Tab, at?: number): void {
  const s = useTabStore.getState()
  useTabStore.setState({ tabs: { ...s.tabs, [t.id]: t }, tabOrder: [...s.tabOrder, t.id] })
  useWorkspaceStore.setState({
    workspaces: useWorkspaceStore.getState().workspaces.map((w) => {
      if (w.id !== wsId) return w
      const tabs = [...w.tabs]
      tabs.splice(at ?? tabs.length, 0, t.id)
      return { ...w, tabs }
    }),
  })
}

function dropTab(id: string): void {
  const s = useTabStore.getState()
  const tabs = { ...s.tabs }
  delete tabs[id]
  useTabStore.setState({ tabs, tabOrder: s.tabOrder.filter((t) => t !== id), activeTabId: s.activeTabId === id ? null : s.activeTabId })
  useWorkspaceStore.setState({ workspaces: useWorkspaceStore.getState().workspaces.map((w) => ({ ...w, tabs: w.tabs.filter((t) => t !== id), activeTabId: w.activeTabId === id ? null : w.activeTabId })) })
}

const listed = (wsId: string): string[] => useWorkspaceStore.getState().workspaces.find((w) => w.id === wsId)?.tabs ?? []
const sotOrder = (key: string): string[] => (daemon.rows.get(key)?.payload as { order: string[] } | null)?.order ?? []

let daemon: FakeDaemon
let executor: Executor | null = null
let collector: Collector | null = null
const problems: Array<{ kind: string; section?: string; detail: string }> = []

/** One device's session: attach with `direction`, connect, and let everything play out. */
async function attach(clientId: string, direction: 'push' | 'pull'): Promise<void> {
  h.clientId = clientId
  let initial: 'push' | 'pull' | null = direction
  executor = createExecutor({
    hostId: M,
    profileId: PROFILE,
    isLeader: () => true,
    isReachable: () => true,
    autoSync: () => true,
    onProblem: (p) => problems.push(p),
    buildNow: (key) => buildSectionPayload(key as ProfileSectionKey),
    initialDirection: () => initial,
    onInitialSettled: () => (initial = null),
  })
  const ex = executor
  collector = startCollector({ onSection: (r) => ex.onSection(r) })
  await collector.primeAll()
  executor.onReconnected()
  await settle()
}

const settle = async (): Promise<void> => {
  for (let i = 0; i < 6; i += 1) await vi.advanceTimersByTimeAsync(1_000)
}

/** The device goes away: another machine comes next, with its own storage. */
function leave(): void {
  collector?.stop()
  executor?.dispose()
  collector = null
  executor = null
  clearSectionStore()
  localStorage.clear()
}

/** Device A writes `key` (built by the real builders from A's world) the way its executor would, and B hears of it. */
async function aWrites(key: 'workspaces' | `tabs.${string}`, payload: object, opts: { settle?: boolean } = {}): Promise<void> {
  const cur = daemon.rows.get(key)
  const hash = await hashSection(payload)
  const [fingerprint, ordinal] = key === 'workspaces' ? SHAPE.workspaces : SHAPE.tabs
  const out = daemon.put(key, { clientId: A, baseRev: cur !== undefined && cur.hash !== null ? cur.rev : 0, hash, fingerprint, ordinal, payload: payload as Record<string, unknown> })
  if (out.kind !== 'applied') throw new Error(`A's write of ${key} did not land: ${out.kind}`)
  executor!.onRemoteEvent({ hostId: M, profileId: PROFILE, section: key, rev: out.rev, hash, writerClientId: A })
  if (opts.settle !== false) await settle()
}

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

/** A pushed [wa1: ta1]; B attached by pulling it and is live. */
async function bFollowsA(): Promise<void> {
  world([ws('wa1', ['ta1'])], [tab('ta1')])
  await attach(A, 'push')
  leave()
  world([], [])
  await attach(B, 'pull')
  expect(listed('wa1')).toEqual(['ta1'])
  problems.length = 0
}

/* ─── T5: the executor's "not arrived yet" placeholder (spec §3.7) ─── */

describe('a workspace whose tabs are all device-local builds the empty placeholder (spec §3.7)', () => {
  it('no base + the SOT has content: the report is held until the index lands, then the section is PULLED — the synced tab arrives, the device-local one stays, nothing is written', async () => {
    await bFollowsA()
    // A adds wa2 with a tmux tab (both rows on the daemon); B hears only of `workspaces`: wa2 appears here, empty.
    const wa2 = buildTabsSection(ws('wa2', ['t2']), { t2: tab('t2') })
    daemon.put('tabs.wa2', { clientId: A, baseRev: 0, hash: await hashSection(wa2), fingerprint: SHAPE.tabs[0], ordinal: SHAPE.tabs[1], payload: wa2 as unknown as Record<string, unknown> })
    const writesBefore = daemon.writes.length
    await aWrites('workspaces', buildWorkspacesSection([ws('wa1', ['ta1']), ws('wa2', ['t2'])]), { settle: false })
    for (let i = 0; i < 20 && !useWorkspaceStore.getState().workspaces.some((w) => w.id === 'wa2'); i += 1) await vi.advanceTimersByTimeAsync(10)
    expect(useWorkspaceStore.getState().workspaces.map((w) => w.id)).toEqual(['wa1', 'wa2'])
    expect(listed('wa2')).toEqual([])

    // B opens Settings in wa2 before the collector has reported it: the report is the EMPTY placeholder — this
    // client never agreed on tabs.wa2 and its index does not list it yet: held, a re-index asked, "not arrived".
    putTab('wa2', tab('sL', SETTINGS))
    await settle()

    expect(listed('wa2')).toEqual(['sL', 't2'])
    expect(useTabStore.getState().tabs.sL.layout).toEqual(leaf('p-sL', SETTINGS))
    expect(daemon.writes.slice(writesBefore).filter((w) => w.clientId === B)).toEqual([])
    expect(executor!.status().sections['tabs.wa2']).toBe('synced')
    expect(problems.filter((p) => p.kind !== 'sections-unrendered')).toEqual([])
  })

  it('with a base, an empty report is an edit: closing the last synced tab pushes the empty section; the device-local tab stays', async () => {
    await bFollowsA()
    putTab('wa1', tab('sL', SETTINGS), 0)
    await settle()
    expect(daemon.writes.filter((w) => w.clientId === B)).toEqual([]) // a device-local tab moves no hash
    dropTab('ta1')
    await settle()
    expect(daemon.writes.filter((w) => w.clientId === B)).toEqual([{ op: 'put', key: 'tabs.wa1', clientId: B, outcome: 'applied' }])
    expect(sotOrder('tabs.wa1')).toEqual([])
    expect(listed('wa1')).toEqual(['sL'])
  })

  it('launching the only New Tab of such a workspace turns the report into an ordinary non-empty one, pushed', async () => {
    await bFollowsA()
    dropTab('ta1')
    await settle()
    putTab('wa1', tab('n1', NEW_TAB))
    await settle()
    expect(sotOrder('tabs.wa1')).toEqual([]) // still only the placeholder
    useTabStore.getState().setPaneContent('n1', 'p-n1', TMUX('launched'))
    await settle()
    expect(sotOrder('tabs.wa1')).toEqual(['n1'])
    expect(executor!.status().sections['tabs.wa1']).toBe('synced')
  })
})

/* ─── T6: two devices end to end ─── */

describe('two devices: interface-only tabs stay on the device that opened them', () => {
  /** A's world, kept as data after its session: A's later edits are made here and built with the real builder. */
  const aTabs: Record<string, Tab> = {}
  let aOrder: string[] = []
  const aPushesWa1 = (): Promise<void> => aWrites('tabs.wa1', buildTabsSection(ws('wa1', aOrder), aTabs))

  /** A: wa1 = [tmux ta1, Settings sA, New Tab nA], pushed by a real session; then B attaches by pulling. */
  async function aThenB(): Promise<void> {
    for (const k of Object.keys(aTabs)) delete aTabs[k]
    Object.assign(aTabs, { ta1: tab('ta1'), sA: tab('sA', SETTINGS), nA: tab('nA', NEW_TAB) })
    aOrder = ['ta1', 'sA', 'nA']
    world([ws('wa1', aOrder)], Object.values(aTabs))
    await attach(A, 'push')
    expect(sotOrder('tabs.wa1')).toEqual(['ta1']) // what A's own build sent
    leave()
    world([], [])
    await attach(B, 'pull')
    problems.length = 0
  }

  /** B never had to push `tabs.wa1` back: every payload A sent was already canonical. */
  const bRewroteNothing = (): void => expect(daemon.writes.filter((w) => w.clientId === B && w.key === 'tabs.wa1')).toEqual([])

  it('A pushes [tmux, Settings, New Tab]: the SOT and B hold the tmux tab only', async () => {
    await aThenB()
    expect(sotOrder('tabs.wa1')).toEqual(['ta1'])
    expect(listed('wa1')).toEqual(['ta1'])
    expect(Object.keys(useTabStore.getState().tabs)).toEqual(['ta1'])
    expect(executor!.status().profile).toBe('synced')
    bRewroteNothing()
  })

  it('A launches its New Tab (same tab id) → B gets that tab', async () => {
    await aThenB()
    aTabs.nA = tab('nA', TMUX('launched'))
    await aPushesWa1()
    expect(listed('wa1')).toEqual(['ta1', 'nA'])
    expect(useTabStore.getState().tabs.nA.layout).toEqual(leaf('p-nA', TMUX('launched')))
    expect(problems).toEqual([])
    bRewroteNothing()
  })

  it('B\'s own Settings tab between two synced tabs survives A\'s next push, at the same place; B writes nothing', async () => {
    await aThenB()
    aTabs.nA = tab('nA', TMUX('launched'))
    await aPushesWa1()
    const mine = tab('sB', SETTINGS)
    putTab('wa1', mine, 1)
    await settle()
    expect(listed('wa1')).toEqual(['ta1', 'sB', 'nA'])
    const bWrites = daemon.writes.filter((w) => w.clientId === B).length

    aTabs.ta1 = { ...aTabs.ta1, pinned: true }
    await aPushesWa1()
    expect(listed('wa1')).toEqual(['ta1', 'sB', 'nA'])
    expect(useTabStore.getState().tabs.ta1.pinned).toBe(true)
    expect(useTabStore.getState().tabs.sB).toBe(mine)
    expect(daemon.writes.filter((w) => w.clientId === B)).toHaveLength(bWrites)
    expect(sotOrder('tabs.wa1')).toEqual(['ta1', 'nA'])
    expect(executor!.status().sections['tabs.wa1']).toBe('synced')
  })

  it('a split [New Tab, tmux] reaches B whole; A closes the tmux leaf → B\'s copy is deleted', async () => {
    await aThenB()
    const splitLayout: PaneLayout = { type: 'split', id: 'sp', direction: 'h', sizes: [50, 50], children: [leaf('p-new', NEW_TAB), leaf('p-tmux', TMUX('split'))] }
    aTabs.sp = { id: 'sp', pinned: false, locked: false, createdAt: 1, layout: splitLayout }
    aOrder = [...aOrder, 'sp']
    await aPushesWa1()
    expect(listed('wa1')).toEqual(['ta1', 'sp'])
    const got = useTabStore.getState().tabs.sp.layout as Extract<PaneLayout, { type: 'split' }>
    expect(got.children.map((c) => (c as Extract<PaneLayout, { type: 'leaf' }>).pane.content.kind)).toEqual(['new-tab', 'tmux-session'])

    aTabs.sp = { ...aTabs.sp, layout: leaf('p-new', NEW_TAB) } // only the New Tab leaf is left: device-local now
    await aPushesWa1()
    expect(listed('wa1')).toEqual(['ta1'])
    expect(Object.hasOwn(useTabStore.getState().tabs, 'sp')).toBe(false)
    bRewroteNothing()
  })

  it('each device\'s Settings singleton is its own: B finds B\'s, never A\'s', async () => {
    await aThenB()
    putTab('wa1', tab('sB', SETTINGS))
    await settle()
    expect(useTabStore.getState().openSingletonTab(SETTINGS)).toBe('sB')
    expect(Object.hasOwn(useTabStore.getState().tabs, 'sA')).toBe(false)
    expect(sotOrder('tabs.wa1')).toEqual(['ta1'])
  })
})
