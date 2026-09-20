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
  const [tab, ws, local, sw, mw] = await Promise.all([
    import('../../stores/useTabStore'),
    import('../../features/workspace/store'),
    import('../../stores/useLocalProfilesStore'),
    import('./switch-active'),
    import('./master-world'),
  ])
  return { useTabStore: tab.useTabStore, useWorkspaceStore: ws.useWorkspaceStore, useLocalProfilesStore: local.useLocalProfilesStore, ...sw, ...mw, storageListener: onStorage() }
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
    expect(fence()).toBe('1')
    expect(order[0]).toBe(STORAGE_KEYS.WORLD_EPOCH)
    expect(order.slice(1)).toEqual([STORAGE_KEYS.LOCAL_PROFILES, STORAGE_KEYS.TABS, STORAGE_KEYS.WORKSPACES])
  })

  it('a promote raises it too', async () => {
    const { w2, slaveId } = await seeded()
    expect(w2.promoteToMaster(slaveId, 'Old master')).toMatchObject({ ok: true })
    expect(fence()).toBe('1')
    expect(w2.readMasterWorld().settled).toBe(true)
  })

  it('a refused switch leaves no side key behind', async () => {
    const { w2 } = await seeded()
    expect(await w2.switchActiveProfile('nope')).toEqual({ ok: false, reason: 'not-found' })
    expect(fence()).toBeNull()
  })

  it('a refused promote leaves no side key behind', async () => {
    const { w2 } = await seeded()
    expect(w2.promoteToMaster('nope', 'Old')).toEqual({ ok: false, reason: 'not-found' })
    expect(fence()).toBeNull()
  })

  it('a switch that fails half-way lowers the fence again BEFORE it puts the stores back — so the rollback reaches the disk', async () => {
    const { w2, slaveId } = await seeded()
    expect(await w2.switchActiveProfile(slaveId)).toEqual({ ok: true }) // epoch 1, fence '1'
    const before = [disk(STORAGE_KEYS.LOCAL_PROFILES), disk(STORAGE_KEYS.TABS), disk(STORAGE_KEYS.WORKSPACES)]
    const real = Storage.prototype.setItem
    let thrown = 0
    vi.spyOn(Storage.prototype, 'setItem').mockImplementation(function (this: Storage, key: string, value: string) {
      if (key === STORAGE_KEYS.WORKSPACES && thrown++ === 0) throw new Error('quota')
      real.call(this, key, value)
    })
    expect(await w2.switchActiveProfile('master')).toEqual({ ok: false, reason: 'write-failed', detail: 'quota' })
    vi.restoreAllMocks()
    expect(fence()).toBe('1')
    expect([disk(STORAGE_KEYS.LOCAL_PROFILES), disk(STORAGE_KEYS.TABS), disk(STORAGE_KEYS.WORKSPACES)]).toEqual(before)
    await flush()
    expect(w2.readMasterWorld()).toMatchObject({ settled: true, onScreen: false })
    expect(w2.useTabStore.getState().worldEpoch).toBe(1)
  })

  it('a promote whose re-stamp fails lowers it again', async () => {
    const { w2, slaveId } = await seeded()
    const real = Storage.prototype.setItem
    let thrown = 0
    vi.spyOn(Storage.prototype, 'setItem').mockImplementation(function (this: Storage, key: string, value: string) {
      if (key === STORAGE_KEYS.WORKSPACES && thrown++ === 0) throw new Error('quota')
      real.call(this, key, value)
    })
    expect(w2.promoteToMaster(slaveId, 'Old')).toEqual({ ok: false, reason: 'write-failed', detail: 'quota' })
    vi.restoreAllMocks()
    expect(fence()).toBeNull()
    expect(diskState(STORAGE_KEYS.LOCAL_PROFILES)).toMatchObject({ activeProfileId: 'master', worldEpoch: 0 })
    expect(diskState(STORAGE_KEYS.TABS)).toMatchObject({ worldId: 'master', worldEpoch: 0 })
  })
})

describe('window 2 switches; window 1 still holds the old world', () => {
  /** Both windows on the slave (epoch 1); then window 2 switches back to the master (epoch 2), undelivered. */
  async function switchedUnderWindow1(): Promise<{ w1: Win; w2: Win; slaveId: string; after: Record<string, string> }> {
    const { w2, slaveId } = await seeded()
    expect(await w2.switchActiveProfile(slaveId)).toEqual({ ok: true })
    QueuedBroadcastChannel.queue = []
    const w1 = await openWindow() // starts from storage: the slave on screen, epoch 1
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
    expect(w1.useTabStore.getState()).toMatchObject({ worldId: 'master', worldEpoch: 2 })
    await deliverAll()
    expect(screenOf(w1)).toBe(screenOf(w2))
    for (const key of Object.keys(after)) expect(disk(key)).toBe(after[key])
  })

  it('a stale WORKSPACE store write is fenced', async () => {
    const { w1, w2, after } = await switchedUnderWindow1()
    w1.useWorkspaceStore.getState().renameWorkspace('sws', 'stale rename')
    expect(disk(STORAGE_KEYS.WORKSPACES)).toBe(after[STORAGE_KEYS.WORKSPACES])
    await flush()
    expect(w1.useWorkspaceStore.getState()).toMatchObject({ worldId: 'master', worldEpoch: 2 })
    await deliverAll()
    expect(screenOf(w1)).toBe(screenOf(w2))
    for (const key of Object.keys(after)) expect(disk(key)).toBe(after[key])
  })

  it('a stale LOCAL-PROFILES store write is fenced', async () => {
    const { w1, w2, slaveId, after } = await switchedUnderWindow1()
    expect(w1.useLocalProfilesStore.getState().renameSlave(slaveId, 'stale rename')).toEqual({ ok: true })
    expect(disk(STORAGE_KEYS.LOCAL_PROFILES)).toBe(after[STORAGE_KEYS.LOCAL_PROFILES])
    await flush()
    expect(w1.useLocalProfilesStore.getState()).toMatchObject({ activeProfileId: 'master', worldEpoch: 2 })
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
    // No broadcast has arrived: window 1's three stores agree with each other, on epoch 1. Without the fence this reads "settled".
    expect(w1.readMasterWorld()).toEqual({ settled: false, reason: 'behind-fence' })
    expect(w1.masterWorkspaceIds()).toBeNull() // the collector builds nothing
    const parked = w1.useLocalProfilesStore.getState().parkedMaster
    if (parked === null) throw new Error('window 1 should still hold the master parked')
    expect(w1.writeMasterWorld(parked)).toBe('unsettled') // an apply answers `busy`
    expect(w1.promoteToMaster(slaveId, 'Old')).toEqual({ ok: false, reason: 'unsettled' })
    expect(w1.copyMasterAsSlave('copy')).toEqual({ ok: false, reason: 'unsettled' })
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
