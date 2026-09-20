// spa/src/lib/profile/world-fence.windows.test.ts — two windows over one
// localStorage: a window that still holds the OLD world must not write it back
// over a switch another window made (found on real hardware; see
// lib/storage/world-fence.ts).
//
// Each `openWindow()` is a fresh module graph (`vi.resetModules()`): its own three
// world stores, its own syncManager and channel, its own switch-active — over the
// SAME localStorage. The BroadcastChannel here QUEUES: a real one delivers some
// turns later, one message at a time, and what a window does BETWEEN two
// deliveries is the whole bug. `deliver()` hands over the next message.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { ParkedWorld } from '../../stores/useLocalProfilesStore'
import { STORAGE_KEYS } from '../storage/keys'
import type { Tab, Workspace } from '../../types/tab'
import { FakeLockManager, navigatorWithLocks } from '../storage/__tests__/fake-web-locks'

interface Message {
  to: QueuedBroadcastChannel
  data: unknown
}

class QueuedBroadcastChannel {
  static bus = new Set<QueuedBroadcastChannel>()
  static queue: Message[] = []
  name: string
  onmessage: ((event: MessageEvent) => void) | null = null
  constructor(name: string) {
    this.name = name
    QueuedBroadcastChannel.bus.add(this)
  }
  postMessage(data: unknown): void {
    for (const peer of QueuedBroadcastChannel.bus) {
      if (peer !== this && peer.name === this.name) QueuedBroadcastChannel.queue.push({ to: peer, data })
    }
  }
  close(): void {
    QueuedBroadcastChannel.bus.delete(this)
  }
}

/** The keys still waiting to be delivered, in order. */
const waiting = (): string[] => QueuedBroadcastChannel.queue.map((m) => (m.data as { key: string }).key)

async function flush(): Promise<void> {
  for (let i = 0; i < 10; i++) await Promise.resolve()
}

/** Hands over the next queued message — which must be about `key` — and lets its rehydrate finish. */
async function deliver(key: string): Promise<void> {
  const next = QueuedBroadcastChannel.queue.shift()
  if (next === undefined) throw new Error(`nothing to deliver (wanted ${key})`)
  expect((next.data as { key: string }).key).toBe(key)
  next.to.onmessage?.({ data: next.data } as MessageEvent)
  await flush()
}

async function deliverAll(): Promise<void> {
  while (QueuedBroadcastChannel.queue.length > 0) await deliver(waiting()[0])
}

/** The `storage` listener of the window opened last — `syncManager`'s, installed by its first `register`. All
 *  windows of this file share ONE `window` object, so the listener is captured instead of installed: a storage
 *  event must reach the OTHER documents only, and `window.dispatchEvent` would hand it to the writer as well. */
let lastStorageListener: ((e: Event) => void) | null = null

async function openWindow() {
  vi.resetModules()
  lastStorageListener = null
  const realAdd = window.addEventListener.bind(window)
  const add = vi.spyOn(window, 'addEventListener').mockImplementation((type: string, fn: unknown, opts?: unknown) => {
    if (type === 'storage') lastStorageListener = fn as (e: Event) => void
    else realAdd(type, fn as EventListener, opts as AddEventListenerOptions)
  })
  try {
    return await loadWindow()
  } finally {
    add.mockRestore()
  }
}

async function loadWindow() {
  const onStorage = (): ((e: Event) => void) | null => lastStorageListener
  const [tab, ws, local, sw, mw, hosts, lifecycle, toast, rebuild] = await Promise.all([
    import('../../stores/useTabStore'),
    import('../../features/workspace/store'),
    import('../../stores/useLocalProfilesStore'),
    import('./switch-active'),
    import('./master-world'),
    import('../../stores/useHostStore'),
    import('../host-lifecycle'),
    import('../../stores/useUndoToast'),
    import('../../stores/useRebuildStore'),
  ])
  return {
    useTabStore: tab.useTabStore,
    useWorkspaceStore: ws.useWorkspaceStore,
    useLocalProfilesStore: local.useLocalProfilesStore,
    useHostStore: hosts.useHostStore,
    useUndoToast: toast.useUndoToast,
    useRebuildStoreForTest: rebuild.useRebuildStore,
    deleteHostWithUndoToast: lifecycle.deleteHostWithUndoToast,
    ...sw,
    ...mw,
    storageListener: onStorage(),
  }
}
type Win = Awaited<ReturnType<typeof openWindow>>

// === fixtures ===

const MASTER = 'SENTINEL-MASTER'
const SLAVE = 'SENTINEL-SLAVE'

function tab(id: string, sentinel: string): Tab {
  return { id, pinned: false, locked: false, createdAt: 1, layout: { type: 'leaf', pane: { id: `p-${id}`, content: { kind: 'tmux-session', hostId: 'h1', sessionCode: `c-${id}`, mode: 'terminal', cachedName: `${sentinel}-${id}`, tmuxInstance: 'i' } } } }
}

function world(prefix: string, sentinel: string): ParkedWorld {
  const a = `${prefix}t1`
  const ws: Workspace = { id: `${prefix}ws`, name: `${sentinel}-ws`, tabs: [a], activeTabId: a }
  const second: Workspace = { id: `${prefix}ws2`, name: `${sentinel}-ws2`, tabs: [], activeTabId: null }
  return { workspaces: [ws, second], tabs: { [a]: tab(a, sentinel) }, activeWorkspaceId: ws.id, activeTabId: a }
}

const disk = (key: string): string => localStorage.getItem(key) ?? ''
const diskState = (key: string): Record<string, unknown> => (JSON.parse(disk(key)) as { state: Record<string, unknown> }).state
const fence = (): string | null => localStorage.getItem(STORAGE_KEYS.WORLD_EPOCH)
/** The ONE epoch the three stores of `w` carry — and the fence with them. An epoch is an operation's own (clock-based): tests compare, they do not count. */
function epochOf(w: Win): number {
  const epoch = w.useLocalProfilesStore.getState().worldEpoch
  expect([w.useTabStore.getState().worldEpoch, w.useWorkspaceStore.getState().worldEpoch]).toEqual([epoch, epoch])
  return epoch
}

const screenOf = (w: Win): string => JSON.stringify([w.useTabStore.getState().tabs, w.useWorkspaceStore.getState().workspaces])

/** Window 2 with the master on screen and one parked slave; returns the slave's id. Everything it broadcast is dropped. */
async function seeded(): Promise<{ w2: Win; slaveId: string }> {
  const w2 = await openWindow()
  const m = world('m', MASTER)
  w2.commitTabWorld(m)
  const added = w2.useLocalProfilesStore.getState().addSlave('Slave', world('s', SLAVE))
  if (!added.ok) throw new Error(added.reason)
  QueuedBroadcastChannel.queue = []
  return { w2, slaveId: added.id }
}

beforeEach(() => {
  QueuedBroadcastChannel.bus.clear()
  QueuedBroadcastChannel.queue = []
  localStorage.clear()
  vi.stubGlobal('BroadcastChannel', QueuedBroadcastChannel)
})

afterEach(() => {
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
  vi.resetModules()
})

describe('THE IRON RULE: a user who never switched', () => {
  it('has no side key, whatever the three stores write', async () => {
    const { w2 } = await seeded()
    w2.useWorkspaceStore.getState().addWorkspace('another')
    w2.useLocalProfilesStore.getState().renameSlave(Object.keys(w2.useLocalProfilesStore.getState().slaves)[0], 'renamed')
    expect(fence()).toBeNull()
    expect(Object.keys(localStorage).filter((k) => k.includes('epoch'))).toEqual([])
    // …and the writes are what they always were: the persist envelope of the state, nothing else.
    expect(diskState(STORAGE_KEYS.WORKSPACES).workspaces).toEqual(w2.useWorkspaceStore.getState().workspaces)
    expect(diskState(STORAGE_KEYS.LOCAL_PROFILES).slaves).toEqual(w2.useLocalProfilesStore.getState().slaves)
  })
})

describe('a switch raises the fence first', () => {
  it('the side key holds the new epoch — and is written BEFORE any of the three stores', async () => {
    const { w2, slaveId } = await seeded()
    const order: string[] = []
    const real = Storage.prototype.setItem
    vi.spyOn(Storage.prototype, 'setItem').mockImplementation(function (this: Storage, key: string, value: string) {
      order.push(key)
      real.call(this, key, value)
    })
    expect(await w2.switchActiveProfile(slaveId)).toEqual({ ok: true })
    vi.restoreAllMocks()
    expect(epochOf(w2)).toBeGreaterThan(0)
    expect(fence()).toBe(String(epochOf(w2)))
    expect(order[0]).toBe(STORAGE_KEYS.WORLD_EPOCH)
    expect(order.slice(1)).toEqual([STORAGE_KEYS.LOCAL_PROFILES, STORAGE_KEYS.TABS, STORAGE_KEYS.WORKSPACES])
  })

  it('a promote raises it too', async () => {
    const { w2, slaveId } = await seeded()
    expect(await w2.promoteToMaster(slaveId, 'Old master')).toMatchObject({ ok: true })
    expect(epochOf(w2)).toBeGreaterThan(0)
    expect(fence()).toBe(String(epochOf(w2)))
    expect(w2.readMasterWorld().settled).toBe(true)
  })

  it('a refused switch leaves no side key behind', async () => {
    const { w2 } = await seeded()
    expect(await w2.switchActiveProfile('nope')).toEqual({ ok: false, reason: 'not-found' })
    expect(fence()).toBeNull()
  })

  it('a refused promote leaves no side key behind', async () => {
    const { w2 } = await seeded()
    expect(await w2.promoteToMaster('nope', 'Old')).toEqual({ ok: false, reason: 'not-found' })
    expect(fence()).toBeNull()
  })

  it('a switch that fails half-way lowers the fence again BEFORE it puts the stores back — so the rollback reaches the disk', async () => {
    const { w2, slaveId } = await seeded()
    expect(await w2.switchActiveProfile(slaveId)).toEqual({ ok: true })
    const first = epochOf(w2)
    expect(fence()).toBe(String(first))
    const before = [disk(STORAGE_KEYS.LOCAL_PROFILES), disk(STORAGE_KEYS.TABS), disk(STORAGE_KEYS.WORKSPACES)]
    const real = Storage.prototype.setItem
    let thrown = 0
    vi.spyOn(Storage.prototype, 'setItem').mockImplementation(function (this: Storage, key: string, value: string) {
      if (key === STORAGE_KEYS.WORKSPACES && thrown++ === 0) throw new Error('quota')
      real.call(this, key, value)
    })
    expect(await w2.switchActiveProfile('master')).toEqual({ ok: false, reason: 'write-failed', detail: 'quota' })
    vi.restoreAllMocks()
    expect(fence()).toBe(String(first))
    expect([disk(STORAGE_KEYS.LOCAL_PROFILES), disk(STORAGE_KEYS.TABS), disk(STORAGE_KEYS.WORKSPACES)]).toEqual(before)
    await flush()
    expect(w2.readMasterWorld()).toMatchObject({ settled: true, onScreen: false })
    expect(epochOf(w2)).toBe(first)
  })

  it('a promote whose re-stamp fails lowers it again', async () => {
    const { w2, slaveId } = await seeded()
    const real = Storage.prototype.setItem
    let thrown = 0
    vi.spyOn(Storage.prototype, 'setItem').mockImplementation(function (this: Storage, key: string, value: string) {
      if (key === STORAGE_KEYS.WORKSPACES && thrown++ === 0) throw new Error('quota')
      real.call(this, key, value)
    })
    expect(await w2.promoteToMaster(slaveId, 'Old')).toEqual({ ok: false, reason: 'write-failed', detail: 'quota' })
    vi.restoreAllMocks()
    expect(fence()).toBeNull()
    expect(diskState(STORAGE_KEYS.LOCAL_PROFILES)).toMatchObject({ activeProfileId: 'master', worldEpoch: 0 })
    expect(diskState(STORAGE_KEYS.TABS)).toMatchObject({ worldId: 'master', worldEpoch: 0 })
  })
})

describe('window 2 switches; window 1 still holds the old world', () => {
  /** Both windows on the slave; then window 2 switches back to the master — a higher epoch — undelivered. */
  async function switchedUnderWindow1(): Promise<{ w1: Win; w2: Win; slaveId: string; after: Record<string, string> }> {
    const { w2, slaveId } = await seeded()
    expect(await w2.switchActiveProfile(slaveId)).toEqual({ ok: true })
    QueuedBroadcastChannel.queue = []
    const w1 = await openWindow() // starts from storage: the slave on screen
    expect(w1.readMasterWorld()).toMatchObject({ settled: true, onScreen: false })
    expect(screenOf(w1)).toContain(SLAVE)

    expect(await w2.switchActiveProfile('master')).toEqual({ ok: true })
    expect(waiting()).toEqual([STORAGE_KEYS.LOCAL_PROFILES, STORAGE_KEYS.TABS, STORAGE_KEYS.WORKSPACES])
    const after = Object.fromEntries([STORAGE_KEYS.LOCAL_PROFILES, STORAGE_KEYS.TABS, STORAGE_KEYS.WORKSPACES].map((k) => [k, disk(k)]))
    return { w1, w2, slaveId, after }
  }

  it('THE FIELD CASE — a workspace action between the `tabs` and the `workspaces` rehydrate: the disk keeps window 2\'s world, window 1 settles on it', async () => {
    const { w1, w2, after } = await switchedUnderWindow1()
    await deliver(STORAGE_KEYS.LOCAL_PROFILES)
    await deliver(STORAGE_KEYS.TABS)
    expect(w1.readMasterWorld()).toEqual({ settled: false, reason: 'epoch-mismatch' })

    // Any subscriber of window 1 that reacts to the new tabs with a workspace action (route sync does).
    w1.useWorkspaceStore.getState().setActiveWorkspace('sws2')

    expect(disk(STORAGE_KEYS.WORKSPACES)).toBe(after[STORAGE_KEYS.WORKSPACES])
    expect(disk(STORAGE_KEYS.WORKSPACES)).not.toContain(SLAVE)
    expect(waiting()).toEqual([STORAGE_KEYS.WORKSPACES]) // and nothing was announced to window 2

    await deliverAll()
    expect(w1.readMasterWorld()).toMatchObject({ settled: true, onScreen: true })
    expect(screenOf(w1)).toBe(screenOf(w2))
    expect(screenOf(w1)).not.toContain(SLAVE)
    expect(w1.useWorkspaceStore.getState().activeWorkspaceId).toBe('mws')
    // window 2 never heard of it
    expect(w2.readMasterWorld()).toMatchObject({ settled: true, onScreen: true })
    expect(screenOf(w2)).toContain(MASTER)
    for (const key of Object.keys(after)) expect(disk(key)).toBe(after[key])
    // and window 1 can switch again
    expect(await w1.switchActiveProfile(Object.keys(w1.useLocalProfilesStore.getState().slaves)[0])).toEqual({ ok: true })
  })

  it('…and when the `workspaces` broadcast NEVER arrives, the dropped write alone brings window 1 to the new world', async () => {
    const { w1, w2 } = await switchedUnderWindow1()
    await deliver(STORAGE_KEYS.LOCAL_PROFILES)
    await deliver(STORAGE_KEYS.TABS)
    QueuedBroadcastChannel.queue = []
    w1.useWorkspaceStore.getState().setActiveWorkspace('sws2')
    await flush()
    expect(w1.readMasterWorld()).toMatchObject({ settled: true, onScreen: true })
    expect(screenOf(w1)).toBe(screenOf(w2))
  })

  it('a stale TAB store write is fenced', async () => {
    const { w1, w2, after } = await switchedUnderWindow1()
    w1.useTabStore.getState().setActiveTab('st1')
    w1.useTabStore.getState().togglePin('st1')
    expect(disk(STORAGE_KEYS.TABS)).toBe(after[STORAGE_KEYS.TABS])
    await flush()
    expect(w1.useTabStore.getState()).toMatchObject({ worldId: 'master', worldEpoch: epochOf(w2) })
    await deliverAll()
    expect(screenOf(w1)).toBe(screenOf(w2))
    for (const key of Object.keys(after)) expect(disk(key)).toBe(after[key])
  })

  it('a stale WORKSPACE store write is fenced', async () => {
    const { w1, w2, after } = await switchedUnderWindow1()
    w1.useWorkspaceStore.getState().renameWorkspace('sws', 'stale rename')
    expect(disk(STORAGE_KEYS.WORKSPACES)).toBe(after[STORAGE_KEYS.WORKSPACES])
    await flush()
    expect(w1.useWorkspaceStore.getState()).toMatchObject({ worldId: 'master', worldEpoch: epochOf(w2) })
    await deliverAll()
    expect(screenOf(w1)).toBe(screenOf(w2))
    for (const key of Object.keys(after)) expect(disk(key)).toBe(after[key])
  })

  it('a stale LOCAL-PROFILES store write is fenced', async () => {
    const { w1, w2, slaveId, after } = await switchedUnderWindow1()
    expect(w1.useLocalProfilesStore.getState().renameSlave(slaveId, 'stale rename')).toEqual({ ok: true })
    expect(disk(STORAGE_KEYS.LOCAL_PROFILES)).toBe(after[STORAGE_KEYS.LOCAL_PROFILES])
    await flush()
    expect(w1.useLocalProfilesStore.getState()).toMatchObject({ activeProfileId: 'master', worldEpoch: epochOf(w2) })
    expect(w1.useLocalProfilesStore.getState().slaves[slaveId].name).toBe('Slave')
    await deliverAll()
    expect(screenOf(w1)).toBe(screenOf(w2))
    for (const key of Object.keys(after)) expect(disk(key)).toBe(after[key])
  })

  it('a SWITCH made from the stale window is refused — it would park the old screen over window 2\'s world — and the window catches up', async () => {
    const { w1, w2, after } = await switchedUnderWindow1()
    expect(await w1.switchActiveProfile('master')).toEqual({ ok: false, reason: 'unsettled' })
    for (const key of Object.keys(after)) expect(disk(key)).toBe(after[key])
    await flush()
    expect(screenOf(w1)).toBe(screenOf(w2))
    expect(w1.readMasterWorld()).toMatchObject({ settled: true, onScreen: true })
  })

  it('THE ONE DOOR SAYS SO: three stores that agree on a world BEHIND the fence are unsettled — nothing of it is read, written or promoted', async () => {
    const { w1, w2, slaveId, after } = await switchedUnderWindow1()
    // No broadcast has arrived: window 1's three stores agree with each other, on the previous epoch. Without the fence this reads "settled".
    expect(w1.readMasterWorld()).toEqual({ settled: false, reason: 'behind-fence' })
    expect(w1.masterWorkspaceIds()).toBeNull() // the collector builds nothing
    const parked = w1.useLocalProfilesStore.getState().parkedMaster
    if (parked === null) throw new Error('window 1 should still hold the master parked')
    expect(w1.writeMasterWorld(parked)).toBe('unsettled') // an apply answers `busy`
    expect(w1.copyMasterAsSlave('copy')).toEqual({ ok: false, reason: 'unsettled' })
    const promoting = w1.promoteToMaster(slaveId, 'Old') // refused inside the call (no Web Locks here) — before the catch-up, which is a turn away
    for (const key of Object.keys(after)) expect(disk(key)).toBe(after[key])
    expect(await promoting).toEqual({ ok: false, reason: 'unsettled' })
    for (const key of Object.keys(after)) expect(disk(key)).toBe(after[key])
    // The look itself asked the stores to catch up.
    await flush()
    expect(w1.readMasterWorld()).toMatchObject({ settled: true, onScreen: true })
    expect(screenOf(w1)).toBe(screenOf(w2))
    expect(w2.readMasterWorld()).toMatchObject({ settled: true, onScreen: true })
  })

  it('writes of the window that is up to date go through as ever', async () => {
    const { w2 } = await switchedUnderWindow1()
    w2.useWorkspaceStore.getState().renameWorkspace('mws', 'renamed by 2')
    expect(disk(STORAGE_KEYS.WORKSPACES)).toContain('renamed by 2')
    expect(waiting().at(-1)).toBe(STORAGE_KEYS.WORKSPACES)
  })
})

describe('belt and braces: a window that MISSED a broadcast recovers by itself', () => {
  it('the `workspaces` broadcast is lost and nothing in the window writes: the first look at the unsettled world rehydrates the three stores — once', async () => {
    const { w2, slaveId } = await seeded()
    const w1 = await openWindow()
    expect(await w2.switchActiveProfile(slaveId)).toEqual({ ok: true })
    const heard = vi.fn()
    const stop = w1.subscribeMasterWorld(heard)
    await deliver(STORAGE_KEYS.LOCAL_PROFILES)
    QueuedBroadcastChannel.queue = [] // tabs and workspaces never arrive
    await flush()
    expect(w1.readMasterWorld()).toMatchObject({ settled: true, onScreen: false })
    expect(screenOf(w1)).toBe(screenOf(w2))
    stop()
  })

  it('a world that a rehydrate cannot settle is rehydrated ONCE per unsettled stretch, not on every look', async () => {
    const { w2 } = await seeded()
    const stop = w2.subscribeMasterWorld(() => {})
    const rehydrate = vi.spyOn(w2.useTabStore.persist, 'rehydrate')
    // Storage itself disagrees: nothing to recover from.
    w2.useTabStore.setState({ worldEpoch: 7 })
    await flush()
    w2.useTabStore.getState().setActiveTab('mt1')
    w2.masterWorldStuck(0)
    w2.masterWorldStuck(1)
    await flush()
    expect(w2.readMasterWorld().settled).toBe(false)
    expect(rehydrate).toHaveBeenCalledTimes(1)
    stop()
  })

  it('a window\'s OWN switch — unsettled for the length of its synchronous block — rehydrates nothing', async () => {
    const { w2, slaveId } = await seeded()
    const stop = w2.subscribeMasterWorld(() => {})
    const spies = [w2.useTabStore, w2.useWorkspaceStore, w2.useLocalProfilesStore].map((s) => vi.spyOn(s.persist, 'rehydrate'))
    const tabs = w2.useLocalProfilesStore.getState().slaves[slaveId].world?.tabs
    expect(await w2.switchActiveProfile(slaveId)).toEqual({ ok: true })
    await flush()
    for (const spy of spies) expect(spy).not.toHaveBeenCalled()
    expect(w2.useTabStore.getState().tabs).toBe(tabs) // same objects: no pane was rebuilt
    stop()
  })
})

// ---------------------------------------------------------------------------
// C1 — two windows, the same epoch, two switches at the same time. A window's
// block is synchronous, so two REAL blocks cannot interleave inside one test
// process. What can be reproduced faithfully is every order of their DISK
// operations: window B's switch is run for real first and the four strings it
// left in storage are captured, then storage is taken back to before — B is past
// its block, its writes "not visible yet" — and they are replayed INSIDE window
// A's real block, at chosen points. The points are A's reads of the side key
// after it raised its own fence: one before each of its three store writes (the
// fenced storage asks), one for its final `superseded` check.
// ---------------------------------------------------------------------------

describe('C1 — two switches from the same epoch, at the same time', () => {
  const NOW = 1_800_000_000_000
  const FENCE = STORAGE_KEYS.WORLD_EPOCH
  const ORDER = [FENCE, STORAGE_KEYS.LOCAL_PROFILES, STORAGE_KEYS.TABS, STORAGE_KEYS.WORKSPACES] as const
  type Key = (typeof ORDER)[number]

  const snapshot = (): Record<string, string | null> => Object.fromEntries(ORDER.map((k) => [k, localStorage.getItem(k)]))
  const put = (key: string, value: string | null): void => (value === null ? localStorage.removeItem(key) : localStorage.setItem(key, value))

  /** Windows A and B, both with the master on screen at the same epoch, and two parked slaves. */
  async function twoWindows(): Promise<{ a: Win; b: Win; s1: string; s2: string }> {
    const { w2: b, slaveId: s1 } = await seeded()
    const second = b.useLocalProfilesStore.getState().addSlave('Other', world('o', 'SENTINEL-OTHER'))
    if (!second.ok) throw new Error(second.reason)
    QueuedBroadcastChannel.queue = []
    const a = await openWindow()
    vi.spyOn(Date, 'now').mockReturnValue(NOW)
    return { a, b, s1, s2: second.id }
  }

  /** B's switch, for real — then storage as it was before: B's writes are "not visible yet". Returns what they are. */
  async function switchedButNotVisible(b: Win, target: string, random: number): Promise<Record<Key, string | null>> {
    const before = snapshot()
    vi.spyOn(Math, 'random').mockReturnValue(random)
    expect(await b.switchActiveProfile(target)).toEqual({ ok: true })
    const written = snapshot() as Record<Key, string | null>
    for (const k of ORDER) put(k, before[k])
    return written
  }

  /**
   * A's switch, for real, with B's writes landing at `plan[n]` = just before A's n-th read of the side key after A
   * raised its fence (1–3: before A's local-profiles / tabs / workspaces write; 4: before A's final check).
   */
  async function switchWithReplay(a: Win, target: string, random: number, written: Record<Key, string | null>, plan: Partial<Record<1 | 2 | 3 | 4, Key[]>>, beforeFenceWrite: Key[] = []) {
    vi.spyOn(Math, 'random').mockReturnValue(random)
    const realGet = Storage.prototype.getItem
    const realSet = Storage.prototype.setItem
    let raised = false
    let reads = 0
    let replaying = false
    const set = vi.spyOn(Storage.prototype, 'setItem').mockImplementation(function (this: Storage, key: string, value: string) {
      if (key === FENCE && !replaying && !raised) {
        // A has READ the fence and found it below its epoch; this is the write. What lands in between is `beforeFenceWrite`.
        replaying = true
        for (const k of beforeFenceWrite) put(k, written[k])
        replaying = false
      }
      realSet.call(this, key, value)
      if (key === FENCE && !replaying) raised = true
    })
    const get = vi.spyOn(Storage.prototype, 'getItem').mockImplementation(function (this: Storage, key: string) {
      if (key === FENCE && raised && !replaying) {
        reads += 1
        replaying = true
        for (const k of plan[reads as 1 | 2 | 3 | 4] ?? []) put(k, written[k])
        replaying = false
      }
      return realGet.call(this, key)
    })
    try {
      return await a.switchActiveProfile(target)
    } finally {
      set.mockRestore()
      get.mockRestore()
    }
  }

  const stateOnDisk = (key: string): Record<string, unknown> => diskState(key)

  /** Both windows hear everything; then: ONE world, in both memories and on disk, under one epoch. */
  async function expectOneWorld(a: Win, b: Win, worldId: string, sentinel: string): Promise<void> {
    await deliverAll()
    await flush()
    const epoch = stateOnDisk(STORAGE_KEYS.LOCAL_PROFILES).worldEpoch
    expect(stateOnDisk(STORAGE_KEYS.LOCAL_PROFILES).activeProfileId).toBe(worldId)
    expect(stateOnDisk(STORAGE_KEYS.TABS)).toMatchObject({ worldId, worldEpoch: epoch })
    expect(stateOnDisk(STORAGE_KEYS.WORKSPACES)).toMatchObject({ worldId, worldEpoch: epoch })
    expect(fence()).toBe(String(epoch))
    expect(disk(STORAGE_KEYS.TABS)).toContain(sentinel)
    expect(disk(STORAGE_KEYS.WORKSPACES)).toContain(sentinel)
    for (const w of [a, b]) {
      expect(w.readMasterWorld()).toMatchObject({ settled: true, onScreen: false })
      expect(w.useLocalProfilesStore.getState().activeProfileId).toBe(worldId)
      expect(screenOf(w)).toContain(sentinel)
    }
    expect(screenOf(a)).toBe(screenOf(b))
    expect(JSON.stringify(a.useLocalProfilesStore.getState().slaves)).toBe(JSON.stringify(b.useLocalProfilesStore.getState().slaves))
  }

  it.each([
    ['B\'s fence lands between A\'s fence and A\'s first store write, B\'s stores right behind it', { 1: [...ORDER] }],
    ['completely interleaved — fence, fence, store, store, …', { 1: [FENCE], 2: [STORAGE_KEYS.LOCAL_PROFILES], 3: [STORAGE_KEYS.TABS], 4: [STORAGE_KEYS.WORKSPACES] }],
    ['A\'s pointer is already down when B\'s fence comes', { 2: [FENCE, STORAGE_KEYS.LOCAL_PROFILES], 3: [STORAGE_KEYS.TABS], 4: [STORAGE_KEYS.WORKSPACES] }],
    ['A\'s pointer and tabs are down when B arrives', { 3: [...ORDER] }],
    ['all of A is down, B arrives before A has looked back', { 4: [...ORDER] }],
  ] as [string, Partial<Record<1 | 2 | 3 | 4, Key[]>>][])('%s: B (the higher epoch) wins whole, A says `superseded` and ends up in B\'s world', async (_name, plan) => {
    const { a, b, s1, s2 } = await twoWindows()
    const written = await switchedButNotVisible(b, s2, 0.9)
    const result = await switchWithReplay(a, s1, 0.1, written, plan)
    expect(result).toEqual({ ok: false, reason: 'superseded' })
    expect(fence()).toBe(written[FENCE])
    await expectOneWorld(a, b, s2, 'SENTINEL-OTHER')
  })

  it('B\'s writes land only after A is completely done and has looked back: A said ok, and then follows B like any later switch', async () => {
    const { a, b, s1, s2 } = await twoWindows()
    const written = await switchedButNotVisible(b, s2, 0.9)
    expect(await switchWithReplay(a, s1, 0.1, written, {})).toEqual({ ok: true })
    for (const k of ORDER) put(k, written[k])
    // what the browser tells A of B's writes (B's broadcasts are in the queue already)
    await expectOneWorld(a, b, s2, 'SENTINEL-OTHER')
  })

  it('one after the other, B not having heard of A yet: B is behind A\'s fence, refused, and catches up', async () => {
    const { a, b, s1, s2 } = await twoWindows()
    vi.spyOn(Math, 'random').mockReturnValue(0.1)
    expect(await a.switchActiveProfile(s1)).toEqual({ ok: true })
    expect(await b.switchActiveProfile(s2)).toEqual({ ok: false, reason: 'unsettled' })
    await expectOneWorld(a, b, s1, SLAVE)
  })

  it('the three stores of a switch carry ONE epoch, above everything before it — and it is the operation\'s own, not `epoch + 1`', async () => {
    const { a, s1 } = await twoWindows()
    vi.spyOn(Math, 'random').mockReturnValue(0.123)
    expect(await a.switchActiveProfile(s1)).toEqual({ ok: true })
    const epoch = NOW * 1000 + 123
    expect([a.useLocalProfilesStore.getState().worldEpoch, a.useTabStore.getState().worldEpoch, a.useWorkspaceStore.getState().worldEpoch]).toEqual([epoch, epoch, epoch])
    expect(fence()).toBe(String(epoch))
    // the clock stands still and the dice repeat: the next one is still strictly above
    expect(await a.switchActiveProfile('master')).toEqual({ ok: true })
    expect(a.useTabStore.getState().worldEpoch).toBe(epoch + 1)
  })

  it('A FAILS after B has raised its fence: A\'s rollback does not lower B\'s fence', async () => {
    const { a, b, s1, s2 } = await twoWindows()
    const written = await switchedButNotVisible(b, s2, 0.9)
    vi.spyOn(Math, 'random').mockReturnValue(0.1)
    vi.spyOn(a.useWorkspaceStore, 'setState').mockImplementationOnce(() => {
      for (const k of ORDER) put(k, written[k]) // B's whole switch becomes visible at this very moment
      throw new Error('workspace write failed')
    })
    expect(await a.switchActiveProfile(s1)).toEqual({ ok: false, reason: 'write-failed', detail: 'workspace write failed' })
    expect(fence()).toBe(written[FENCE])
    await expectOneWorld(a, b, s2, 'SENTINEL-OTHER')
  })

  describe('the fence is raised by someone else between drawing the epoch and raising it', () => {
    /** The side key as A reads it: real, except that the reads listed in `lost` see another window's higher fence.
     *  Reads per attempt: the door (1), then `nextWorldEpoch` and `raise` — 2, 3 for the first draw, 4, 5 for the second. */
    function losingTheRace(lost: number[]): { reads: () => number; restore: () => void } {
      const realGet = Storage.prototype.getItem
      let n = 0
      const spy = vi.spyOn(Storage.prototype, 'getItem').mockImplementation(function (this: Storage, key: string) {
        const value = realGet.call(this, key)
        if (key !== FENCE) return value
        n += 1
        return lost.includes(n) ? String(NOW * 1000 + 1_000 * n) : value
      })
      return { reads: () => n, restore: () => spy.mockRestore() }
    }

    it('once: a new epoch is drawn and the switch goes through', async () => {
      const { a, s1 } = await twoWindows()
      vi.spyOn(Math, 'random').mockReturnValue(0.1)
      const race = losingTheRace([3])
      const result = await a.switchActiveProfile(s1)
      race.restore()
      expect(result).toEqual({ ok: true })
      expect(fence()).toBe(String(epochOf(a)))
    })

    it('twice: `busy` — nothing written, no side key', async () => {
      const { a, s1 } = await twoWindows()
      vi.spyOn(Math, 'random').mockReturnValue(0.1)
      const before = [a.useLocalProfilesStore.getState(), a.useTabStore.getState(), a.useWorkspaceStore.getState()]
      const race = losingTheRace([3, 5])
      const result = await a.switchActiveProfile(s1)
      race.restore()
      expect(result).toEqual({ ok: false, reason: 'busy' })
      expect(race.reads()).toBe(5)
      expect(fence()).toBeNull()
      expect([a.useLocalProfilesStore.getState(), a.useTabStore.getState(), a.useWorkspaceStore.getState()]).toEqual(before)
      expect(a.useLocalProfilesStore.getState()).toBe(before[0])
    })
  })

  // === C1' — the read-check-write of the fence itself ===

  it('WITHOUT WEB LOCKS, THE RESIDUE, PINNED: B raises its (higher) fence between A\'s read of the fence and A\'s write of it — A lowers it, both windows\' stores pass, storage ends up MIXED. Silent, never wrong: both windows unsettled, nothing reported, and the parked worlds — one atomic key — are one window\'s, whole', async () => {
    const { a, b, s1, s2 } = await twoWindows()
    const written = await switchedButNotVisible(b, s2, 0.9)
    // B's fence lands inside A's raise; B's pointer and tabs after A's pointer (A's tabs then overwrite B's); B's workspaces last.
    const result = await switchWithReplay(a, s1, 0.1, written, { 2: [STORAGE_KEYS.LOCAL_PROFILES, STORAGE_KEYS.TABS], 4: [STORAGE_KEYS.WORKSPACES] }, [FENCE])
    expect(result).toEqual({ ok: true }) // A cannot know: the fence it reads back is its own
    expect(Number(fence())).toBeLessThan(Number(written[FENCE])) // the fence went DOWN — what a mutex is for

    await deliverAll()
    await flush()
    // Storage: B's pointer, A's tabs, B's workspaces.
    expect(disk(STORAGE_KEYS.LOCAL_PROFILES)).toBe(written[STORAGE_KEYS.LOCAL_PROFILES])
    expect(diskState(STORAGE_KEYS.LOCAL_PROFILES).activeProfileId).toBe(s2)
    expect(diskState(STORAGE_KEYS.TABS).worldId).toBe(s1)
    expect(diskState(STORAGE_KEYS.WORKSPACES).worldId).toBe(s2)
    for (const w of [a, b]) {
      expect(w.readMasterWorld().settled).toBe(false)
      expect(w.masterWorkspaceIds()).toBeNull() // the collector builds nothing, an apply answers `busy`
      // every PARKED world is what B parked, byte for byte: the master, and the slave nobody put on screen
      expect(JSON.stringify(w.useLocalProfilesStore.getState().parkedMaster)).toBe(JSON.stringify(world('m', MASTER)))
      expect(JSON.stringify(w.useLocalProfilesStore.getState().slaves[s1].world)).toBe(JSON.stringify(world('s', SLAVE)))
    }
  })

  describe('WITH WEB LOCKS: one block at a time, across windows', () => {
    let locks: FakeLockManager

    beforeEach(() => {
      locks = new FakeLockManager()
      vi.stubGlobal('navigator', navigatorWithLocks(locks))
    })

    it('two switches asked for at the same instant are serialised: the second enters its block only when the first has left, reads what the first wrote — and is a late-comer to the fence like any other', async () => {
      const { a, b, s1, s2 } = await twoWindows()
      vi.spyOn(Math, 'random').mockReturnValue(0.5)
      const entered: string[] = []
      for (const [name, w] of [['a', a], ['b', b]] as const) {
        const acquire = w.useRebuildStoreForTest.getState().acquireOperationLock
        vi.spyOn(w.useRebuildStoreForTest.getState(), 'acquireOperationLock').mockImplementation((...args) => {
          entered.push(`${name} enters; fence ${fence() === null ? 'absent' : 'raised'}`)
          return acquire(...args)
        })
      }
      const first = a.switchActiveProfile(s1)
      const second = b.switchActiveProfile(s2)
      expect(entered).toEqual([]) // neither block runs inside the call
      expect(await first).toEqual({ ok: true })
      expect(await second).toEqual({ ok: false, reason: 'unsettled' })
      expect(entered).toEqual(['a enters; fence absent', 'b enters; fence raised'])
      expect(locks.grants.map((g) => g.returned instanceof Promise)).toEqual([false, false]) // THE BLOCK IS SYNCHRONOUS: it hands the lock a value
      expect(locks.isHeld('purdex-world-switch')).toBe(false)
      await expectOneWorld(a, b, s1, SLAVE)
    })

    it('a promote goes through the same lock', async () => {
      const { a, b, s1, s2 } = await twoWindows()
      const first = a.promoteToMaster(s1, 'Old master')
      const second = b.switchActiveProfile(s2)
      expect(await first).toMatchObject({ ok: true })
      expect(await second).toEqual({ ok: false, reason: 'unsettled' })
      expect(locks.grants.map((g) => [g.name, g.returned instanceof Promise])).toEqual([['purdex-world-switch', false], ['purdex-world-switch', false]])
    })

    it('the lock is not granted within 3 s (a renderer frozen inside its block): `busy`, nothing written — and nothing happens when the lock comes free after all', async () => {
      const { a, s1 } = await twoWindows()
      vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] })
      let release = (): void => {}
      void locks.request('purdex-world-switch', {}, () => new Promise<void>((done) => (release = done)))
      const before = [disk(STORAGE_KEYS.LOCAL_PROFILES), disk(STORAGE_KEYS.TABS), disk(STORAGE_KEYS.WORKSPACES)]
      const pending = a.switchActiveProfile(s1)
      await vi.advanceTimersByTimeAsync(3_000)
      expect(await pending).toEqual({ ok: false, reason: 'busy' })
      release()
      await vi.advanceTimersByTimeAsync(100)
      vi.useRealTimers()
      expect([disk(STORAGE_KEYS.LOCAL_PROFILES), disk(STORAGE_KEYS.TABS), disk(STORAGE_KEYS.WORKSPACES)]).toEqual(before)
      expect(fence()).toBeNull()
      expect(a.useLocalProfilesStore.getState().activeProfileId).toBe('master')
    })

    it('a store write that throws inside the block: everything is put back, the fence is lowered, and the lock is free for the next window', async () => {
      const { a, b, s1, s2 } = await twoWindows()
      vi.spyOn(a.useWorkspaceStore, 'setState').mockImplementationOnce(() => {
        throw new Error('workspace write failed')
      })
      const first = a.switchActiveProfile(s1)
      const second = b.switchActiveProfile(s2)
      expect(await first).toEqual({ ok: false, reason: 'write-failed', detail: 'workspace write failed' })
      expect(await second).toEqual({ ok: true })
      expect(locks.isHeld('purdex-world-switch')).toBe(false)
      await expectOneWorld(a, b, s2, 'SENTINEL-OTHER')
    })

    it('the block itself THROWS (not a store write it catches — the operation lock blows up): the call rejects, and the lock is released all the same', async () => {
      const { a, b, s1, s2 } = await twoWindows()
      vi.spyOn(a.useRebuildStoreForTest.getState(), 'acquireOperationLock').mockImplementationOnce(() => {
        throw new Error('lock store broken')
      })
      await expect(a.switchActiveProfile(s1)).rejects.toThrow('lock store broken')
      expect(locks.isHeld('purdex-world-switch')).toBe(false)
      expect(await b.switchActiveProfile(s2)).toEqual({ ok: true })
    })

    it('…and a throw from INSIDE the block, the operation lock already taken: both locks are released', async () => {
      const { a, s1 } = await twoWindows()
      vi.spyOn(a.useLocalProfilesStore, 'getState').mockImplementationOnce(() => {
        throw new Error('store broken')
      })
      await expect(a.switchActiveProfile(s1)).rejects.toThrow('store broken')
      expect(a.useRebuildStoreForTest.getState().lockedBy).toBeNull()
      expect(locks.isHeld('purdex-world-switch')).toBe(false)
      expect(await a.switchActiveProfile(s1)).toEqual({ ok: true })
    })
  })

  it('a promote is superseded the same way', async () => {
    const { a, b, s1, s2 } = await twoWindows()
    const written = await switchedButNotVisible(b, s2, 0.9)
    vi.spyOn(Math, 'random').mockReturnValue(0.1)
    const realGet = Storage.prototype.getItem
    const realSet = Storage.prototype.setItem
    let raised = false
    let done = false
    const set = vi.spyOn(Storage.prototype, 'setItem').mockImplementation(function (this: Storage, key: string, value: string) {
      realSet.call(this, key, value)
      if (key === FENCE && !done) raised = true
    })
    const get = vi.spyOn(Storage.prototype, 'getItem').mockImplementation(function (this: Storage, key: string) {
      if (key === FENCE && raised && !done) {
        done = true
        for (const k of ORDER) put(k, written[k])
      }
      return realGet.call(this, key)
    })
    const result = await a.promoteToMaster(s1, 'Old master')
    set.mockRestore()
    get.mockRestore()
    expect(result).toEqual({ ok: false, reason: 'superseded' })
    await expectOneWorld(a, b, s2, 'SENTINEL-OTHER')
  })
})

// ---------------------------------------------------------------------------
// S1 — two renderer PROCESSES. Measured on real hardware: the BroadcastChannel
// message reaches window 1 BEFORE window 2's write is visible to window 1's
// `localStorage`; the native `storage` event comes after it is. So window 1 gets
// its own VIEW of storage here: frozen at some point, caught up key by key. Reads
// made while `reader === 1` go to the view; every write goes to the real storage
// (and to the view: a process sees its own writes). Window code is synchronous
// or a few microtasks long, so `inWindow1` brackets it.
// ---------------------------------------------------------------------------

class LaggedView {
  reader: 1 | 2 = 2
  private frozen: Map<string, string | null> | null = null
  private readonly real: Storage
  constructor(real: Storage) {
    this.real = real
  }
  /** From now on window 1 sees storage as it is at this instant. */
  freeze(): void {
    this.frozen = new Map(Object.keys(this.real).map((k) => [k, this.real.getItem(k)]))
  }
  /** Window 1's process has caught up with `key` (all keys when omitted). */
  catchUp(key?: string): void {
    if (key === undefined) this.frozen = null
    else this.frozen?.set(key, this.real.getItem(key))
  }
  getItem(key: string): string | null {
    if (this.reader === 1 && this.frozen !== null) return this.frozen.get(key) ?? null
    return this.real.getItem(key)
  }
  setItem(key: string, value: string): void {
    this.real.setItem(key, value)
    if (this.reader === 1) this.frozen?.set(key, value)
  }
  removeItem(key: string): void {
    this.real.removeItem(key)
    if (this.reader === 1) this.frozen?.set(key, null)
  }
  clear(): void {
    this.real.clear()
  }
}

describe('S1 — another PROCESS: the broadcast arrives before the write is visible, the storage event after', () => {
  const WORLD_KEYS = [STORAGE_KEYS.LOCAL_PROFILES, STORAGE_KEYS.TABS, STORAGE_KEYS.WORKSPACES]
  let view: LaggedView
  let real: Storage

  beforeEach(() => {
    real = window.localStorage
    view = new LaggedView(real)
    vi.stubGlobal('localStorage', view)
  })

  async function inWindow1(fn: () => unknown): Promise<void> {
    view.reader = 1
    try {
      await fn()
      await flush()
    } finally {
      view.reader = 2
    }
  }

  /** What the browser hands window 1 for window 2's write of `key`: by then the key IS visible to window 1. */
  function storageEventFor(key: string): Event {
    view.catchUp(key)
    return Object.assign(new Event('storage'), { key, newValue: real.getItem(key), storageArea: view })
  }

  /** Both windows on the slave; window 1's view freezes; window 2 switches back to the master. */
  async function switchedInAnotherProcess(): Promise<{ w1: Win; w2: Win }> {
    const { w2, slaveId } = await seeded()
    expect(await w2.switchActiveProfile(slaveId)).toEqual({ ok: true })
    QueuedBroadcastChannel.queue = []
    const w1 = await openWindow()
    view.freeze()
    expect(await w2.switchActiveProfile('master')).toEqual({ ok: true })
    expect(waiting()).toEqual(WORLD_KEYS)
    return { w1, w2 }
  }

  it('THE FIELD CASE — the first two broadcasts read the OLD value, the third the new one: one write behind, unsettled; the storage events bring the window level', async () => {
    const { w1, w2 } = await switchedInAnotherProcess()
    await inWindow1(async () => {
      await deliver(STORAGE_KEYS.LOCAL_PROFILES)
      await deliver(STORAGE_KEYS.TABS)
      view.catchUp()
      await deliver(STORAGE_KEYS.WORKSPACES)
    })
    // What was measured: two stores one write behind, the third level.
    expect(w1.useWorkspaceStore.getState().worldEpoch).toBe(w2.useWorkspaceStore.getState().worldEpoch)
    expect(w1.useTabStore.getState().worldEpoch).toBeLessThan(w2.useTabStore.getState().worldEpoch)
    expect(w1.useLocalProfilesStore.getState().worldEpoch).toBeLessThan(w2.useLocalProfilesStore.getState().worldEpoch)

    await inWindow1(() => {
      for (const key of [STORAGE_KEYS.WORLD_EPOCH, ...WORLD_KEYS]) w1.storageListener?.(storageEventFor(key))
    })

    expect(w1.readMasterWorld()).toMatchObject({ settled: true, onScreen: true })
    expect(screenOf(w1)).toBe(screenOf(w2))
    expect(screenOf(w1)).toContain(MASTER)
    expect(screenOf(w1)).not.toContain(SLAVE)
  })

  it('EVERY broadcast reads the old value, and each key becomes visible only with its storage event: level after the last one', async () => {
    const { w1, w2 } = await switchedInAnotherProcess()
    await inWindow1(() => deliverAll())
    expect(screenOf(w1)).toContain(SLAVE) // three rehydrates, all of the previous world
    for (const key of [STORAGE_KEYS.WORLD_EPOCH, ...WORLD_KEYS]) {
      expect(w1.readMasterWorld().settled && screenOf(w1) === screenOf(w2)).toBe(false)
      await inWindow1(() => w1.storageListener?.(storageEventFor(key)))
    }
    expect(w1.readMasterWorld()).toMatchObject({ settled: true, onScreen: true })
    expect(screenOf(w1)).toBe(screenOf(w2))
  })

  it('the ONE background recovery of an unsettled stretch was spent while storage still read old — a refused SWITCH (the user asking) tries again, every time', async () => {
    const { w1, w2 } = await switchedInAnotherProcess()
    QueuedBroadcastChannel.queue = [] // no signal of any kind reaches window 1 in this test
    const rehydrate = vi.spyOn(w1.useTabStore.persist, 'rehydrate')
    await inWindow1(() => {
      view.catchUp(STORAGE_KEYS.WORLD_EPOCH) // only the fence is visible yet
      expect(w1.masterWorldStuck(0)).toBe(false) // a background look: `behind-fence` → the stretch's one recovery…
    })
    expect(rehydrate).toHaveBeenCalledTimes(1) // …which read the old world
    expect(w1.readMasterWorld()).toEqual({ settled: false, reason: 'behind-fence' })

    view.catchUp()
    await inWindow1(() => w1.masterWorldStuck(1)) // background: once per stretch, and that was it
    expect(rehydrate).toHaveBeenCalledTimes(1)

    await inWindow1(async () => expect(await w1.switchActiveProfile('master')).toEqual({ ok: false, reason: 'unsettled' }))
    expect(rehydrate).toHaveBeenCalledTimes(2)
    expect(w1.readMasterWorld()).toMatchObject({ settled: true, onScreen: true })
    expect(screenOf(w1)).toBe(screenOf(w2))
  })

  it('no broadcast at all (it is not guaranteed to arrive): the storage events alone do it', async () => {
    const { w1, w2 } = await switchedInAnotherProcess()
    QueuedBroadcastChannel.queue = []
    await inWindow1(() => {
      for (const key of [STORAGE_KEYS.WORLD_EPOCH, ...WORLD_KEYS]) w1.storageListener?.(storageEventFor(key))
    })
    expect(w1.readMasterWorld()).toMatchObject({ settled: true, onScreen: true })
    expect(screenOf(w1)).toBe(screenOf(w2))
  })
})

// ---------------------------------------------------------------------------
// C2' — the undo of a host delete, pressed in a window that has NOT HEARD of
// another window's promote yet. Its memory says nothing was relabelled; storage
// says otherwise, twice: the fence is above this window (`behind-fence`), and
// the persisted `relabelCount` has moved.
// ---------------------------------------------------------------------------

describe("C2' — undo in a window that has not heard of another window's promote", () => {
  const WORLD_KEYS = [STORAGE_KEYS.LOCAL_PROFILES, STORAGE_KEYS.TABS, STORAGE_KEYS.WORKSPACES]
  const MESSAGES = { deleted: 'h1 deleted', worldSkipped: 'h1 is back, its tabs are not' }
  let view: LaggedView

  beforeEach(() => {
    view = new LaggedView(window.localStorage)
    vi.stubGlobal('localStorage', view)
  })

  const memoryOf = (w: Win): string => JSON.stringify([w.useTabStore.getState().tabs, w.useTabStore.getState().tabOrder, w.useWorkspaceStore.getState().workspaces, w.useLocalProfilesStore.getState().parkedMaster, w.useLocalProfilesStore.getState().slaves])

  /** Window 1 deletes host h1 (closing the master's tab on it); window 2 hears of it, and promotes the slave. Nothing of that is delivered to window 1. */
  async function deletedThenPromotedElsewhere(): Promise<{ w1: Win; w2: Win }> {
    const { w2, slaveId } = await seeded()
    const w1 = await openWindow()
    for (const w of [w1, w2]) {
      w.useHostStore.setState({ hosts: { h1: { id: 'h1', name: 'h1', ip: '10.0.0.1', port: 7860, order: 0 }, h2: { id: 'h2', name: 'h2', ip: '10.0.0.2', port: 7860, order: 1 } }, hostOrder: ['h2', 'h1'], activeHostId: 'h2', runtime: {} })
    }
    QueuedBroadcastChannel.queue = []
    w1.deleteHostWithUndoToast('h1', true, MESSAGES)
    expect(w1.useTabStore.getState().tabs.mt1).toBeUndefined()
    await deliverAll() // window 2 is level with the delete
    view.freeze() // from here on window 1's PROCESS sees nothing new until told
    expect(await w2.promoteToMaster(slaveId, 'Old master')).toMatchObject({ ok: true })
    return { w1, w2 }
  }

  /** What GlobalUndoToast does on a click, in window 1. */
  async function clickUndoInWindow1(w1: Win): Promise<{ memoryRightAfter: string }> {
    view.reader = 1
    try {
      w1.useUndoToast.getState().toast?.action?.()
      const memoryRightAfter = memoryOf(w1)
      w1.useUndoToast.getState().dismiss()
      await flush()
      return { memoryRightAfter }
    } finally {
      view.reader = 2
    }
  }

  it.each([
    ['the promote is visible to this process — fence and stores — but no event has been delivered: the door says `behind-fence`', undefined],
    ['only `purdex-local-profiles` is visible yet (not the fence): the persisted `relabelCount` says so', STORAGE_KEYS.LOCAL_PROFILES],
  ])('%s → the host is back, NO world is touched, and the user is told', async (_name, visible) => {
    const { w1, w2 } = await deletedThenPromotedElsewhere()
    view.catchUp(visible)
    const diskBefore = WORLD_KEYS.map(disk)
    const memoryBefore = memoryOf(w1)
    const otherBefore = memoryOf(w2)
    expect(w1.useLocalProfilesStore.getState().relabelCount).toBe(0) // this window's memory knows of no promote

    const { memoryRightAfter } = await clickUndoInWindow1(w1)

    expect(memoryRightAfter).toBe(memoryBefore) // the undo itself wrote no world
    expect(WORLD_KEYS.map(disk)).toEqual(diskBefore)
    expect(memoryOf(w2)).toBe(otherBefore)
    expect(w1.useHostStore.getState().hosts.h1).toBeDefined()
    expect(w1.useUndoToast.getState().toast).toMatchObject({ message: MESSAGES.worldSkipped })
  })

  it('another window\'s SWITCH, not heard of yet — no label moved, no count moved, but nobody here can name the worlds (`behind-fence`): skipped and said, too', async () => {
    const { w2, slaveId } = await seeded()
    const w1 = await openWindow()
    w1.useHostStore.setState({ hosts: { h1: { id: 'h1', name: 'h1', ip: '10.0.0.1', port: 7860, order: 0 }, h2: { id: 'h2', name: 'h2', ip: '10.0.0.2', port: 7860, order: 1 } }, hostOrder: ['h2', 'h1'], activeHostId: 'h2', runtime: {} })
    w1.deleteHostWithUndoToast('h1', true, MESSAGES)
    await deliverAll()
    expect(await w2.switchActiveProfile(slaveId)).toEqual({ ok: true })
    const diskBefore = WORLD_KEYS.map(disk)
    const memoryBefore = memoryOf(w1)

    const { memoryRightAfter } = await clickUndoInWindow1(w1)

    expect(memoryRightAfter).toBe(memoryBefore)
    expect(WORLD_KEYS.map(disk)).toEqual(diskBefore)
    expect(w1.useHostStore.getState().hosts.h1).toBeDefined()
    expect(w1.useUndoToast.getState().toast).toMatchObject({ message: MESSAGES.worldSkipped })
  })

  it('…and the window is asked to catch up: with storage visible it is level with the promote a turn later', async () => {
    const { w1, w2 } = await deletedThenPromotedElsewhere()
    view.catchUp()
    await clickUndoInWindow1(w1)
    expect(w1.readMasterWorld().settled).toBe(true)
    expect(memoryOf(w1)).toBe(memoryOf(w2))
  })
})

