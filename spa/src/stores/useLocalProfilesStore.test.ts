import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { STORAGE_KEYS } from '../lib/storage/keys'
import { PROJECTIONS } from '../lib/profile/projections'
import { DEVICE_NAME_MAX_CODE_POINTS } from '../lib/device-name'
import type { Tab, Workspace } from '../types/tab'

// `generateId` is scripted where a test needs a particular id (the collision
// with 'master', above all); everywhere else the queue is empty and a counter
// hands out distinct six-character ids.
const idQueue: string[] = []
let idCounter = 0
vi.mock('../lib/id', () => ({
  generateId: () => idQueue.shift() ?? `id${String(++idCounter).padStart(4, '0')}`,
}))

import { isParkedWorld, normalizeLocalProfileName, useLocalProfilesStore } from './useLocalProfilesStore'
import type { LocalProfile, LocalProfilesState, ParkedWorld } from './useLocalProfilesStore'

type Data = Pick<LocalProfilesState, 'slaves' | 'slaveOrder' | 'activeProfileId' | 'parkedMaster' | 'worldEpoch'>

/** A world whose single tab carries `sentinel` in its pane — so a test can tell which world it is holding. */
function world(sentinel: string): ParkedWorld {
  const tab: Tab = {
    id: `t-${sentinel}`,
    pinned: false,
    locked: false,
    createdAt: 1,
    layout: { type: 'leaf', pane: { id: `p-${sentinel}`, content: { kind: 'new-tab' } } },
  }
  const ws: Workspace = { id: `w-${sentinel}`, name: sentinel, tabs: [tab.id], activeTabId: tab.id, moduleConfig: {} }
  return { workspaces: [ws], tabs: { [tab.id]: tab }, activeWorkspaceId: ws.id, activeTabId: tab.id }
}

const EMPTY_WORLD: ParkedWorld = { workspaces: [], tabs: {}, activeWorkspaceId: null, activeTabId: null }

/** Written independently of the store's own sanitiser on purpose: it is the referee. */
function assertInvariant(s: Data): void {
  expect([...s.slaveOrder].sort(), 'slaveOrder ≡ keys of slaves').toEqual(Object.keys(s.slaves).sort())
  expect(new Set(s.slaveOrder).size, 'slaveOrder has no duplicates').toBe(s.slaveOrder.length)
  for (const [key, slave] of Object.entries(s.slaves)) {
    expect(slave.id, 'a slave is filed under its own id').toBe(key)
    expect(slave.id).not.toBe('master')
    expect(slave.name.trim()).toBe(slave.name)
    expect(slave.name).not.toBe('')
  }
  const onScreen = Object.values(s.slaves).filter((p) => p.world === null).map((p) => p.id)
  if (s.activeProfileId === 'master') {
    expect(s.parkedMaster, 'master on screen ⇒ nothing parked for it').toBeNull()
    expect(onScreen, 'master on screen ⇒ every slave is parked').toEqual([])
  } else {
    expect(onScreen, 'a slave on screen ⇒ it, and only it, has no parked world').toEqual([s.activeProfileId])
    expect(s.parkedMaster, 'a slave on screen ⇒ the master is parked').not.toBeNull()
  }
  expect(Number.isSafeInteger(s.worldEpoch) && s.worldEpoch >= 0).toBe(true)
}

const get = (): LocalProfilesState => useLocalProfilesStore.getState()

/** Merge-mode reset with every mutable field listed (the harness convention). */
const resetStore = (): void => {
  useLocalProfilesStore.setState({ slaves: {}, slaveOrder: [], activeProfileId: 'master', parkedMaster: null, worldEpoch: 0, relabelCount: 0, master: { name: null } })
}

const persistedEnvelope = (): { state: Record<string, unknown>; version: number } =>
  JSON.parse(localStorage.getItem(STORAGE_KEYS.LOCAL_PROFILES) ?? 'null')

async function rehydrateFrom(state: unknown): Promise<void> {
  localStorage.setItem(STORAGE_KEYS.LOCAL_PROFILES, JSON.stringify({ state, version: 1 }))
  await useLocalProfilesStore.persist.rehydrate()
}

/** addSlave that must succeed; returns the id. */
function add(name: string, w: ParkedWorld = world(name), shownHostIds?: string[]): string {
  const r = get().addSlave(name, w, shownHostIds)
  if (!r.ok) throw new Error(`addSlave failed: ${r.reason}`)
  return r.id
}

/** swapActive that must succeed; returns the world taken out. */
function swap(targetId: string, onScreen: ParkedWorld, epoch = get().worldEpoch + 1): ParkedWorld {
  const r = get().swapActive(targetId, onScreen, epoch)
  if (!r.ok) throw new Error(`swapActive failed: ${r.reason}`)
  return r.world
}

const slave = (id: string, name: string, w: ParkedWorld | null, createdAt = 1, shownHostIds: string[] = []): LocalProfile => ({ id, name, createdAt, shownHostIds, world: w })

beforeEach(() => {
  localStorage.clear()
  idQueue.length = 0
  idCounter = 0
  resetStore()
})

afterEach(() => {
  assertInvariant(get())
})

describe('useLocalProfilesStore', () => {
  it('uses its own storage key', () => {
    expect(STORAGE_KEYS.LOCAL_PROFILES).toBe('purdex-local-profiles')
  })

  it('starts with the master on screen and no slaves', () => {
    const s = get()
    expect(s.slaves).toEqual({})
    expect(s.slaveOrder).toEqual([])
    expect(s.activeProfileId).toBe('master')
    expect(s.parkedMaster).toBeNull()
    expect(s.worldEpoch).toBe(0)
  })

  // Slaves never reach the daemon (decision 9). PROJECTIONS is the only
  // allowlist, so "not listed" is the whole guarantee — pinned here.
  it('is not in the sync allowlist', () => {
    for (const list of Object.values(PROJECTIONS)) {
      for (const path of list) expect(path, path).not.toContain('purdex-local-profiles')
    }
  })
})

describe('isParkedWorld', () => {
  it('accepts a world and the empty world', () => {
    expect(isParkedWorld(world('a'))).toBe(true)
    expect(isParkedWorld(EMPTY_WORLD)).toBe(true)
  })

  it.each([
    ['null', null],
    ['an array', []],
    ['a string', 'world'],
    ['workspaces not an array', { ...EMPTY_WORLD, workspaces: {} }],
    ['a workspace without an id', { ...EMPTY_WORLD, workspaces: [{ name: 'x', tabs: [], activeTabId: null }] }],
    ['a workspace whose tabs is not a string list', { ...EMPTY_WORLD, workspaces: [{ id: 'w', name: 'x', tabs: [1], activeTabId: null }] }],
    ['a workspace with a numeric activeTabId', { ...EMPTY_WORLD, workspaces: [{ id: 'w', name: 'x', tabs: [], activeTabId: 3 }] }],
    ['tabs an array', { ...EMPTY_WORLD, tabs: [] }],
    ['tabs null', { ...EMPTY_WORLD, tabs: null }],
    ['a tab filed under another id', { ...EMPTY_WORLD, tabs: { x: world('a').tabs['t-a'] } }],
    ['a tab without a layout', { ...EMPTY_WORLD, tabs: { x: { id: 'x', pinned: false, locked: false, createdAt: 1 } } }],
    ['activeWorkspaceId a number', { ...EMPTY_WORLD, activeWorkspaceId: 1 }],
    ['activeTabId undefined', { workspaces: [], tabs: {}, activeWorkspaceId: null }],
  ])('refuses %s', (_label, v) => {
    expect(isParkedWorld(v)).toBe(false)
  })
})

describe('addSlave', () => {
  it('adds a parked slave at the end of the order', () => {
    vi.spyOn(Date, 'now').mockReturnValue(1234)
    const a = add('alpha')
    const b = add('beta')
    vi.restoreAllMocks()
    expect(get().slaveOrder).toEqual([a, b])
    expect(get().slaves[a]).toEqual({ id: a, name: 'alpha', createdAt: 1234, shownHostIds: [], world: world('alpha') })
    expect(get().activeProfileId).toBe('master')
  })

  it('trims the name, and cuts it by code points at the device-name limit', () => {
    const long = '😀'.repeat(DEVICE_NAME_MAX_CODE_POINTS + 5)
    const a = add('  padded  ')
    const b = add(long)
    expect(get().slaves[a].name).toBe('padded')
    expect(Array.from(get().slaves[b].name)).toHaveLength(DEVICE_NAME_MAX_CODE_POINTS)
    expect(get().slaves[b].name).toBe('😀'.repeat(DEVICE_NAME_MAX_CODE_POINTS))
  })

  it('allows two slaves of one name — the id is the identity', () => {
    const a = add('same')
    const b = add('same')
    expect(a).not.toBe(b)
    expect(Object.keys(get().slaves)).toHaveLength(2)
  })

  it.each(['', '   ', '\n\t'])('refuses the name %j and changes nothing', (name) => {
    const before = get()
    expect(get().addSlave(name, world('x'))).toEqual({ ok: false, reason: 'bad-name' })
    expect(get()).toBe(before)
  })

  it('refuses a name that is not a string', () => {
    expect(get().addSlave(7 as never, world('x'))).toEqual({ ok: false, reason: 'bad-name' })
  })

  it('refuses a malformed world and changes nothing', () => {
    const before = get()
    expect(get().addSlave('x', { ...EMPTY_WORLD, tabs: [] } as never)).toEqual({ ok: false, reason: 'bad-world' })
    expect(get().addSlave('x', null as never)).toEqual({ ok: false, reason: 'bad-world' })
    expect(get()).toBe(before)
  })

  it("never hands out the id 'master' (six base-36 characters: generateId CAN produce it)", () => {
    idQueue.push('master', 'okid01')
    expect(add('x')).toBe('okid01')
    expect(get().slaves.master).toBeUndefined()
  })

  it('never hands out an id that is taken', () => {
    idQueue.push('taken1')
    add('first')
    idQueue.push('taken1', 'taken1', 'fresh1')
    expect(add('second')).toBe('fresh1')
    expect(get().slaves.taken1.name).toBe('first')
  })
})

describe('renameSlave', () => {
  it('renames, normalising like addSlave', () => {
    const a = add('alpha')
    expect(get().renameSlave(a, '  new name ')).toEqual({ ok: true })
    expect(get().slaves[a].name).toBe('new name')
    expect(get().slaves[a].world).toEqual(world('alpha'))
  })

  it('renames the slave that is on screen too', () => {
    const a = add('alpha')
    swap(a, world('M'))
    expect(get().renameSlave(a, 'live')).toEqual({ ok: true })
    expect(get().slaves[a]).toMatchObject({ name: 'live', world: null })
  })

  it('refuses an unknown id, and the master', () => {
    add('alpha')
    const before = get()
    expect(get().renameSlave('nope', 'x')).toEqual({ ok: false, reason: 'not-found' })
    expect(get().renameSlave('master', 'x')).toEqual({ ok: false, reason: 'not-found' })
    expect(get()).toBe(before)
  })

  it('refuses a blank name and changes nothing', () => {
    const a = add('alpha')
    const before = get()
    expect(get().renameSlave(a, '  ')).toEqual({ ok: false, reason: 'bad-name' })
    expect(get()).toBe(before)
  })
})

describe('removeSlave', () => {
  it('removes a parked slave and hands back its world', () => {
    const a = add('alpha')
    const b = add('beta')
    expect(get().removeSlave(a)).toEqual({ ok: true, world: world('alpha') })
    expect(get().slaves[a]).toBeUndefined()
    expect(get().slaveOrder).toEqual([b])
  })

  it('never removes the one on screen', () => {
    const a = add('alpha')
    swap(a, world('M'))
    const before = get()
    expect(get().removeSlave(a)).toEqual({ ok: false, reason: 'on-screen' })
    expect(get()).toBe(before)
  })

  it('removes a parked slave while another is on screen', () => {
    const a = add('alpha')
    const b = add('beta')
    swap(a, world('M'))
    expect(get().removeSlave(b).ok).toBe(true)
    expect(get().activeProfileId).toBe(a)
  })

  it('refuses an unknown id, and the master', () => {
    const before = get()
    expect(get().removeSlave('nope')).toEqual({ ok: false, reason: 'not-found' })
    expect(get().removeSlave('master')).toEqual({ ok: false, reason: 'not-found' })
    expect(get()).toBe(before)
  })
})

describe('reorderSlaves', () => {
  it('takes a permutation of the ids', () => {
    const a = add('a')
    const b = add('b')
    const c = add('c')
    expect(get().reorderSlaves([c, a, b])).toEqual({ ok: true })
    expect(get().slaveOrder).toEqual([c, a, b])
  })

  it.each([
    ['a missing id', (ids: string[]) => [ids[0], ids[1]]],
    ['a duplicate', (ids: string[]) => [ids[0], ids[0], ids[1]]],
    ['an unknown id', (ids: string[]) => [ids[0], ids[1], 'nope']],
    ['an extra id', (ids: string[]) => [...ids, 'nope']],
    ['not an array', () => 'abc' as never],
    ['a non-string member', (ids: string[]) => [ids[0], ids[1], 3 as never]],
  ])('refuses %s and changes nothing', (_label, make) => {
    const ids = [add('a'), add('b'), add('c')]
    const before = get()
    expect(get().reorderSlaves(make(ids))).toEqual({ ok: false, reason: 'bad-order' })
    expect(get()).toBe(before)
  })
})

describe('swapActive — the one atomic exchange', () => {
  it('master → slave: parks the screen as the master, hands out the slave world', () => {
    const a = add('alpha')
    const r = get().swapActive(a, world('M'), 1)
    expect(r).toEqual({ ok: true, world: world('alpha'), previousId: 'master' })
    const s = get()
    expect(s.activeProfileId).toBe(a)
    expect(s.parkedMaster).toEqual(world('M'))
    expect(s.slaves[a].world).toBeNull()
    expect(s.worldEpoch).toBe(1)
  })

  it('slave → master: parks the screen in that slave, hands out the master world', () => {
    const a = add('alpha')
    swap(a, world('M'))
    const r = get().swapActive('master', world('alpha-edited'), 2)
    expect(r).toEqual({ ok: true, world: world('M'), previousId: a })
    const s = get()
    expect(s.activeProfileId).toBe('master')
    expect(s.parkedMaster).toBeNull()
    expect(s.slaves[a].world).toEqual(world('alpha-edited'))
    expect(s.worldEpoch).toBe(2)
  })

  it('slave → slave: the master stays parked, untouched', () => {
    const a = add('alpha')
    const b = add('beta')
    swap(a, world('M'))
    const parkedBefore = get().parkedMaster
    const r = get().swapActive(b, world('alpha-edited'), 5)
    expect(r).toEqual({ ok: true, world: world('beta'), previousId: a })
    const s = get()
    expect(s.activeProfileId).toBe(b)
    expect(s.parkedMaster).toBe(parkedBefore)
    expect(s.slaves[a].world).toEqual(world('alpha-edited'))
    expect(s.slaves[b].world).toBeNull()
    expect(s.worldEpoch).toBe(5)
  })

  it('is ONE set: a subscriber hears once, and what it sees already satisfies the invariant', () => {
    const a = add('alpha')
    const seen: Data[] = []
    const off = useLocalProfilesStore.subscribe((s) => seen.push(s))
    swap(a, world('M'))
    off()
    expect(seen).toHaveLength(1)
    assertInvariant(seen[0])
    expect(seen[0].activeProfileId).toBe(a)
    expect(seen[0].worldEpoch).toBe(1)
  })

  it('is ONE write to storage', () => {
    const a = add('alpha')
    const setItem = vi.spyOn(Storage.prototype, 'setItem')
    swap(a, world('M'))
    const writes = setItem.mock.calls.filter(([key]) => key === STORAGE_KEYS.LOCAL_PROFILES)
    setItem.mockRestore()
    expect(writes).toHaveLength(1)
  })

  it.each([
    ['the master while the master is on screen', 'master'],
    ['an unknown slave', 'nope'],
  ])('refuses %s and changes nothing', (_label, target) => {
    add('alpha')
    const before = get()
    const r = get().swapActive(target, world('M'), 1)
    expect(r).toEqual({ ok: false, reason: target === 'master' ? 'already-on-screen' : 'not-found' })
    expect(get()).toBe(before)
  })

  it('refuses the slave that is already on screen', () => {
    const a = add('alpha')
    swap(a, world('M'))
    const before = get()
    expect(get().swapActive(a, world('x'), 9)).toEqual({ ok: false, reason: 'already-on-screen' })
    expect(get()).toBe(before)
  })

  it('refuses a malformed screen world and changes nothing', () => {
    const a = add('alpha')
    const before = get()
    expect(get().swapActive(a, { ...EMPTY_WORLD, workspaces: null } as never, 1)).toEqual({ ok: false, reason: 'bad-world' })
    expect(get()).toBe(before)
  })

  it.each([0, -1, 1.5, NaN, Infinity, '2', undefined, Number.MAX_SAFE_INTEGER + 1])('refuses the epoch %j (it must be a safe integer above the current one)', (epoch) => {
    const a = add('alpha')
    const before = get()
    expect(get().swapActive(a, world('M'), epoch as never)).toEqual({ ok: false, reason: 'bad-epoch' })
    expect(get()).toBe(before)
  })

  it('refuses an epoch equal to the current one', () => {
    const a = add('alpha')
    swap(a, world('M'), 4)
    expect(get().swapActive('master', world('x'), 4)).toEqual({ ok: false, reason: 'bad-epoch' })
    expect(get().worldEpoch).toBe(4)
  })
})

describe('promoteSlave — a move, never a copy', () => {
  it('`relabelCount` goes up by one with every promote, and with nothing else — it says "the labels of the worlds have moved"', () => {
    const a = add('alpha')
    const b = add('beta')
    get().renameSlave(a, 'renamed')
    get().reorderSlaves([b, a])
    swap(a, world('M'))
    swap('master', world('alpha'))
    get().replaceParkedWorld(b, world('beta2'))
    get().removeSlave(b)
    expect(get().relabelCount).toBe(0)

    expect(get().promoteSlave('nope', 'x', 99)).toMatchObject({ ok: false }) // refused: not counted
    expect(get().relabelCount).toBe(0)
    const first = get().promoteSlave(a, 'old', get().worldEpoch + 1)
    if (!first.ok) throw new Error(first.reason)
    expect(get().relabelCount).toBe(1)
    expect(get().promoteSlave(first.demotedId, 'older', get().worldEpoch + 1)).toMatchObject({ ok: true })
    expect(get().relabelCount).toBe(2)
    expect(persistedEnvelope().state.relabelCount).toBe(2)
  })

  it('master on screen, the slave parked: the screen is relabelled as the demoted slave', () => {
    const a = add('alpha')
    const b = add('beta')
    const c = add('gamma')
    vi.spyOn(Date, 'now').mockReturnValue(777)
    idQueue.push('demote')
    const r = get().promoteSlave(b, ' old master ', 1)
    vi.restoreAllMocks()
    expect(r).toEqual({ ok: true, demotedId: 'demote', activeProfileId: 'demote' })
    const s = get()
    expect(s.parkedMaster).toEqual(world('beta'))
    expect(s.slaves[b]).toBeUndefined()
    expect(s.slaves.demote).toEqual({ id: 'demote', name: 'old master', createdAt: 777, shownHostIds: [], world: null })
    expect(s.activeProfileId).toBe('demote')
    expect(s.slaveOrder).toEqual([a, 'demote', c])
    expect(s.worldEpoch).toBe(1)
  })

  it('that slave on screen: the screen is relabelled as the master, the parked master becomes a slave', () => {
    const a = add('alpha')
    swap(a, world('M'))
    idQueue.push('demote')
    const r = get().promoteSlave(a, 'old master', 2)
    expect(r).toEqual({ ok: true, demotedId: 'demote', activeProfileId: 'master' })
    const s = get()
    expect(s.activeProfileId).toBe('master')
    expect(s.parkedMaster).toBeNull()
    expect(s.slaves[a]).toBeUndefined()
    expect(s.slaves.demote.world).toEqual(world('M'))
    expect(s.slaveOrder).toEqual(['demote'])
    expect(s.worldEpoch).toBe(2)
  })

  it('another slave on screen: the two parked worlds trade places, the screen is not involved', () => {
    const a = add('alpha')
    const b = add('beta')
    swap(a, world('M'))
    idQueue.push('demote')
    const r = get().promoteSlave(b, 'old master', 2)
    expect(r).toEqual({ ok: true, demotedId: 'demote', activeProfileId: a })
    const s = get()
    expect(s.activeProfileId).toBe(a)
    expect(s.slaves[a].world).toBeNull()
    expect(s.parkedMaster).toEqual(world('beta'))
    expect(s.slaves[b]).toBeUndefined()
    expect(s.slaves.demote.world).toEqual(world('M'))
    expect(s.slaveOrder).toEqual([a, 'demote'])
  })

  it('no world is copied or lost: the same world objects, each held exactly once', () => {
    const wA = world('alpha')
    const wB = world('beta')
    const wM = world('M')
    const a = add('alpha', wA)
    const b = add('beta', wB)
    swap(a, wM)
    get().promoteSlave(b, 'old master', 2)
    const held = [get().parkedMaster, ...Object.values(get().slaves).map((p) => p.world)].filter((w) => w !== null)
    expect(held).toHaveLength(2)
    expect(held).toContain(wB)
    expect(held).toContain(wM)
  })

  it('is one set', () => {
    const a = add('alpha')
    const seen: Data[] = []
    const off = useLocalProfilesStore.subscribe((s) => seen.push(s))
    get().promoteSlave(a, 'old', 1)
    off()
    expect(seen).toHaveLength(1)
    assertInvariant(seen[0])
  })

  it("the demoted slave's id is never 'master' nor one in use — the promoted slave's own included", () => {
    idQueue.push('alpha1')
    const a = add('alpha')
    idQueue.push('master', 'alpha1', 'fresh1')
    expect(get().promoteSlave(a, 'old', 1)).toMatchObject({ ok: true, demotedId: 'fresh1' })
  })

  it.each([
    ['an unknown slave', 'nope', 'old', 1, 'not-found'],
    ['the master', 'master', 'old', 1, 'not-found'],
    ['a blank name for the demoted master', null, '  ', 1, 'bad-name'],
    ['an epoch that does not move', null, 'old', 0, 'bad-epoch'],
    ['a fractional epoch', null, 'old', 1.5, 'bad-epoch'],
  ])('refuses %s and changes nothing', (_label, id, name, epoch, reason) => {
    const a = add('alpha')
    const before = get()
    expect(get().promoteSlave(id ?? a, name, epoch)).toEqual({ ok: false, reason })
    expect(get()).toBe(before)
  })
})

describe('replaceParkedWorld', () => {
  it('replaces a parked slave world', () => {
    const a = add('alpha')
    expect(get().replaceParkedWorld(a, world('next'))).toEqual({ ok: true })
    expect(get().slaves[a].world).toEqual(world('next'))
  })

  it('replaces the parked master world (the master keeps syncing while a slave is on screen)', () => {
    const a = add('alpha')
    swap(a, world('M'))
    expect(get().replaceParkedWorld('master', world('M2'))).toEqual({ ok: true })
    expect(get().parkedMaster).toEqual(world('M2'))
    expect(get().worldEpoch).toBe(1)
  })

  it('refuses whatever is on screen — that world lives in the tab stores, not here', () => {
    const a = add('alpha')
    expect(get().replaceParkedWorld('master', world('x'))).toEqual({ ok: false, reason: 'on-screen' })
    swap(a, world('M'))
    const before = get()
    expect(get().replaceParkedWorld(a, world('x'))).toEqual({ ok: false, reason: 'on-screen' })
    expect(get()).toBe(before)
  })

  it('refuses an unknown id and a malformed world', () => {
    const a = add('alpha')
    const before = get()
    expect(get().replaceParkedWorld('nope', world('x'))).toEqual({ ok: false, reason: 'not-found' })
    expect(get().replaceParkedWorld(a, { tabs: {} } as never)).toEqual({ ok: false, reason: 'bad-world' })
    expect(get()).toBe(before)
  })
})

describe('updateParkedWorlds', () => {
  it('maps every parked world — master and slaves — in one set, and skips the one on screen', () => {
    const a = add('alpha')
    const b = add('beta')
    swap(a, world('M'))
    const owners: string[] = []
    const seen: Data[] = []
    const off = useLocalProfilesStore.subscribe((s) => seen.push(s))
    const changed = get().updateParkedWorlds((w, owner) => {
      owners.push(owner)
      return { ...w, activeTabId: null }
    })
    off()
    expect(changed).toBe(2)
    expect(owners.sort()).toEqual([b, 'master'].sort())
    expect(seen).toHaveLength(1)
    expect(get().parkedMaster?.activeTabId).toBeNull()
    expect(get().slaves[b].world?.activeTabId).toBeNull()
    expect(get().slaves[a].world).toBeNull()
  })

  it('nothing changed (same references back) → no set at all', () => {
    add('alpha')
    const before = get()
    expect(get().updateParkedWorlds((w) => w)).toBe(0)
    expect(get()).toBe(before)
  })

  it('a malformed result is ignored for that world; the others still change', () => {
    const a = add('alpha')
    const b = add('beta')
    const changed = get().updateParkedWorlds((w, owner) => (owner === a ? (null as never) : { ...w, activeTabId: null }))
    expect(changed).toBe(1)
    expect(get().slaves[a].world).toEqual(world('alpha'))
    expect(get().slaves[b].world?.activeTabId).toBeNull()
  })
})

describe('relabelCountInStorage — what storage holds right now, by the rule `merge` applies', () => {
  it('absent or unreadable → null; junk → 0; a count → the count — whatever this window\'s memory says', async () => {
    const { relabelCountInStorage } = await import('./useLocalProfilesStore')
    localStorage.removeItem(STORAGE_KEYS.LOCAL_PROFILES)
    expect(relabelCountInStorage()).toBeNull()
    localStorage.setItem(STORAGE_KEYS.LOCAL_PROFILES, '{not json')
    expect(relabelCountInStorage()).toBeNull()
    localStorage.setItem(STORAGE_KEYS.LOCAL_PROFILES, JSON.stringify({ state: 'junk', version: 1 }))
    expect(relabelCountInStorage()).toBeNull()
    for (const junk of [-1, 1.5, '3', null]) {
      localStorage.setItem(STORAGE_KEYS.LOCAL_PROFILES, JSON.stringify({ state: { relabelCount: junk }, version: 1 }))
      expect(relabelCountInStorage()).toBe(0)
    }
    localStorage.setItem(STORAGE_KEYS.LOCAL_PROFILES, JSON.stringify({ state: { relabelCount: 4 }, version: 1 }))
    expect(relabelCountInStorage()).toBe(4)
    expect(get().relabelCount).toBe(0)
    resetStore() // put a well-formed record back for the invariant check
  })
})

describe('persist', () => {
  it('persists exactly the seven data fields, at version 1', () => {
    const a = add('alpha')
    swap(a, world('M'))
    const env = persistedEnvelope()
    expect(env.version).toBe(1)
    expect(Object.keys(env.state).sort()).toEqual(['activeProfileId', 'master', 'parkedMaster', 'relabelCount', 'slaveOrder', 'slaves', 'worldEpoch'])
    expect(env.state).toEqual({
      slaves: { [a]: { ...get().slaves[a], world: null } },
      slaveOrder: [a],
      activeProfileId: a,
      parkedMaster: world('M'),
      worldEpoch: 1,
      relabelCount: 0,
      master: { name: null },
    })
  })

  it('a consistent record comes back as it was', async () => {
    const state = {
      slaves: { s1: slave('s1', 'one', null), s2: slave('s2', 'two', world('two'), 9) },
      slaveOrder: ['s2', 's1'],
      activeProfileId: 's1',
      parkedMaster: world('M'),
      worldEpoch: 12,
    }
    await rehydrateFrom(state)
    const { slaves, slaveOrder, activeProfileId, parkedMaster, worldEpoch } = get()
    expect({ slaves, slaveOrder, activeProfileId, parkedMaster, worldEpoch }).toEqual(state)
  })
})

describe('rehydrate sanitises what storage holds', () => {
  const data = (): Data => {
    const { slaves, slaveOrder, activeProfileId, parkedMaster, worldEpoch } = get()
    return { slaves, slaveOrder, activeProfileId, parkedMaster, worldEpoch }
  }

  it.each([
    ['null', null],
    ['a string', 'junk'],
    ['an array', []],
    ['an empty object', {}],
  ])('%s → the initial state', async (_label, state) => {
    add('left over in memory')
    await rehydrateFrom(state)
    // zustand merges into the CURRENT state; the sanitiser must not let what
    // memory held survive a storage that says otherwise.
    expect(data()).toEqual({ slaves: {}, slaveOrder: [], activeProfileId: 'master', parkedMaster: null, worldEpoch: 0 })
  })

  it('persisted junk can neither add a key nor replace an action', async () => {
    await rehydrateFrom({ slaves: {}, slaveOrder: [], activeProfileId: 'master', parkedMaster: null, worldEpoch: 1, addSlave: 'junk', extra: 1 })
    expect(typeof get().addSlave).toBe('function')
    expect((get() as unknown as Record<string, unknown>).extra).toBeUndefined()
  })

  it.each([
    ['not an object', 'junk'],
    ['filed under another id', slave('other', 'x', world('x'))],
    ["the id 'master'", slave('master', 'x', world('x'))],
    ['an empty id', slave('', 'x', world('x'))],
    ['a malformed world', { id: 'bad', name: 'x', createdAt: 1, world: { tabs: [] } }],
    ['a world of undefined', { id: 'bad', name: 'x', createdAt: 1 }],
  ])('a broken slave (%s) is dropped alone; the others stay', async (_label, broken) => {
    const key = (broken as { id?: string }).id === 'master' ? 'master' : (broken as { id?: string }).id === '' ? '' : 'bad'
    await rehydrateFrom({
      slaves: { good: slave('good', 'good', world('good')), [key]: broken },
      slaveOrder: [key, 'good'],
      activeProfileId: 'master',
      parkedMaster: null,
      worldEpoch: 3,
    })
    expect(data()).toEqual({ slaves: { good: slave('good', 'good', world('good')) }, slaveOrder: ['good'], activeProfileId: 'master', parkedMaster: null, worldEpoch: 3 })
  })

  it('a slave whose NAME is broken keeps its world: the name is replaced, the data is not thrown away', async () => {
    await rehydrateFrom({
      slaves: { a: { id: 'a', name: '   ', createdAt: 1, world: world('a') }, b: { id: 'b', name: 7, createdAt: 'x', world: world('b') } },
      slaveOrder: ['a', 'b'],
      activeProfileId: 'master',
      parkedMaster: null,
      worldEpoch: 0,
    })
    expect(get().slaves.a).toEqual({ id: 'a', name: 'Recovered', createdAt: 1, shownHostIds: [], world: world('a') })
    expect(get().slaves.b).toEqual({ id: 'b', name: 'Recovered', createdAt: 0, shownHostIds: [], world: world('b') })
  })

  it('an over-long persisted name is cut like a new one', async () => {
    await rehydrateFrom({ slaves: { a: slave('a', ` ${'x'.repeat(100)} `, world('a')) }, slaveOrder: ['a'], activeProfileId: 'master', parkedMaster: null, worldEpoch: 0 })
    expect(get().slaves.a.name).toBe('x'.repeat(DEVICE_NAME_MAX_CODE_POINTS))
  })

  it('slaveOrder is rebuilt: unknown ids and duplicates go, missing ids are appended (oldest first, then by id)', async () => {
    await rehydrateFrom({
      slaves: { a: slave('a', 'a', world('a'), 5), b: slave('b', 'b', world('b'), 2), c: slave('c', 'c', world('c'), 2), d: slave('d', 'd', world('d'), 9) },
      slaveOrder: ['d', 'ghost', 'd', 7],
      activeProfileId: 'master',
      parkedMaster: null,
      worldEpoch: 0,
    })
    expect(get().slaveOrder).toEqual(['d', 'b', 'c', 'a'])
  })

  it.each([['not an array', 'abc'], ['missing', undefined]])('slaveOrder %s → rebuilt from the slaves', async (_label, slaveOrder) => {
    await rehydrateFrom({ slaves: { a: slave('a', 'a', world('a')) }, slaveOrder, activeProfileId: 'master', parkedMaster: null, worldEpoch: 0 })
    expect(get().slaveOrder).toEqual(['a'])
  })

  it.each([-1, 1.5, NaN, Infinity, '3', null, undefined, Number.MAX_SAFE_INTEGER + 1])('relabelCount %j → 0', async (relabelCount) => {
    useLocalProfilesStore.setState({ relabelCount: 5 })
    await rehydrateFrom({ slaves: {}, slaveOrder: [], activeProfileId: 'master', parkedMaster: null, worldEpoch: 0, relabelCount })
    expect(get().relabelCount).toBe(0)
  })

  it('a record from before `relabelCount` existed comes back with 0; a good one as it is', async () => {
    useLocalProfilesStore.setState({ relabelCount: 5 })
    await rehydrateFrom({ slaves: {}, slaveOrder: [], activeProfileId: 'master', parkedMaster: null, worldEpoch: 0 })
    expect(get().relabelCount).toBe(0)
    await rehydrateFrom({ slaves: {}, slaveOrder: [], activeProfileId: 'master', parkedMaster: null, worldEpoch: 0, relabelCount: 3 })
    expect(get().relabelCount).toBe(3)
  })

  it.each([-1, 1.5, NaN, Infinity, '3', null, undefined, Number.MAX_SAFE_INTEGER + 1])('worldEpoch %j → 0', async (worldEpoch) => {
    await rehydrateFrom({ slaves: {}, slaveOrder: [], activeProfileId: 'master', parkedMaster: null, worldEpoch })
    expect(get().worldEpoch).toBe(0)
  })

  // --- the invariant ------------------------------------------------------

  it('master on screen BUT a master world is parked: the parked world is rescued as a slave, never dropped', async () => {
    await rehydrateFrom({
      slaves: { a: slave('a', 'a', world('a')) },
      slaveOrder: ['a'],
      activeProfileId: 'master',
      parkedMaster: world('M'),
      worldEpoch: 4,
    })
    expect(data()).toEqual({
      slaves: { a: slave('a', 'a', world('a')), recovered: { id: 'recovered', name: 'Recovered master', createdAt: 0, shownHostIds: [], world: world('M') } },
      slaveOrder: ['a', 'recovered'],
      activeProfileId: 'master',
      parkedMaster: null,
      worldEpoch: 4,
    })
  })

  it('the rescue id is deterministic (every window must arrive at the same one) and steps past one in use', async () => {
    await rehydrateFrom({
      slaves: { recovered: slave('recovered', 'mine', world('r')), 'recovered-2': slave('recovered-2', 'mine too', world('r2')) },
      slaveOrder: ['recovered', 'recovered-2'],
      activeProfileId: 'master',
      parkedMaster: world('M'),
      worldEpoch: 0,
    })
    expect(get().slaveOrder).toEqual(['recovered', 'recovered-2', 'recovered-3'])
    expect(get().slaves['recovered-3'].world).toEqual(world('M'))
  })

  it('master on screen but a slave has no world: that slave holds nothing and goes', async () => {
    await rehydrateFrom({
      slaves: { a: slave('a', 'a', null), b: slave('b', 'b', world('b')) },
      slaveOrder: ['a', 'b'],
      activeProfileId: 'master',
      parkedMaster: null,
      worldEpoch: 0,
    })
    expect(data()).toMatchObject({ slaves: { b: slave('b', 'b', world('b')) }, slaveOrder: ['b'], activeProfileId: 'master', parkedMaster: null })
    expect(get().slaves.a).toBeUndefined()
  })

  it('a slave on screen and a SECOND slave without a world: the second goes, the pointer stays where the tab stores are', async () => {
    await rehydrateFrom({
      slaves: { a: slave('a', 'a', null), b: slave('b', 'b', null), c: slave('c', 'c', world('c')) },
      slaveOrder: ['a', 'b', 'c'],
      activeProfileId: 'a',
      parkedMaster: world('M'),
      worldEpoch: 2,
    })
    expect(data()).toEqual({
      slaves: { a: slave('a', 'a', null), c: slave('c', 'c', world('c')) },
      slaveOrder: ['a', 'c'],
      activeProfileId: 'a',
      parkedMaster: world('M'),
      worldEpoch: 2,
    })
  })

  it.each([
    ['names a slave that does not exist', 'ghost'],
    ['names a slave that is parked', 'c'],
    ['is not a string', 42],
    ['is missing', undefined],
  ])('the pointer %s → master on screen; the parked master is rescued, parked slaves stay, world-less ones go', async (_label, activeProfileId) => {
    await rehydrateFrom({
      slaves: { b: slave('b', 'b', null), c: slave('c', 'c', world('c')) },
      slaveOrder: ['b', 'c'],
      activeProfileId,
      parkedMaster: world('M'),
      worldEpoch: 2,
    })
    const s = get()
    expect(s.activeProfileId).toBe('master')
    expect(s.parkedMaster).toBeNull()
    expect(s.slaveOrder).toEqual(['c', 'recovered'])
    expect(s.slaves.c.world).toEqual(world('c'))
    expect(s.slaves.recovered.world).toEqual(world('M'))
  })

  it.each([
    ['null', null],
    ['malformed', { tabs: [] }],
  ])('a slave on screen but the parked master is %s → master on screen (no master world is invented)', async (_label, parkedMaster) => {
    await rehydrateFrom({
      slaves: { a: slave('a', 'a', null), c: slave('c', 'c', world('c')) },
      slaveOrder: ['a', 'c'],
      activeProfileId: 'a',
      parkedMaster,
      worldEpoch: 2,
    })
    expect(data()).toEqual({ slaves: { c: slave('c', 'c', world('c')) }, slaveOrder: ['c'], activeProfileId: 'master', parkedMaster: null, worldEpoch: 2 })
  })

  it('master on screen and a malformed parked master: nothing to rescue, it goes', async () => {
    await rehydrateFrom({ slaves: {}, slaveOrder: [], activeProfileId: 'master', parkedMaster: 'junk', worldEpoch: 0 })
    expect(data()).toEqual({ slaves: {}, slaveOrder: [], activeProfileId: 'master', parkedMaster: null, worldEpoch: 0 })
  })

  it('slaves not a record → none', async () => {
    await rehydrateFrom({ slaves: [slave('a', 'a', world('a'))], slaveOrder: ['a'], activeProfileId: 'master', parkedMaster: null, worldEpoch: 0 })
    expect(get().slaves).toEqual({})
    expect(get().slaveOrder).toEqual([])
  })
})

// ---------------------------------------------------------------------------
// Two windows. Each `openWindow()` is a fresh module graph — its own store, its
// own syncManager, its own channel — over the SAME localStorage and one shared
// BroadcastChannel bus (the pattern of useProfileStore.test.ts).
// ---------------------------------------------------------------------------

class FakeBroadcastChannel {
  static bus = new Set<FakeBroadcastChannel>()
  name: string
  onmessage: ((event: MessageEvent) => void) | null = null
  constructor(name: string) {
    this.name = name
    FakeBroadcastChannel.bus.add(this)
  }
  postMessage(data: unknown): void {
    for (const peer of FakeBroadcastChannel.bus) {
      if (peer === this || peer.name !== this.name) continue
      peer.onmessage?.({ data } as MessageEvent)
    }
  }
  close(): void {
    FakeBroadcastChannel.bus.delete(this)
  }
}

type Win = typeof import('./useLocalProfilesStore')

async function openWindow(): Promise<Win> {
  vi.resetModules()
  const mod = await import('./useLocalProfilesStore')
  await mod.useLocalProfilesStore.persist.rehydrate()
  return mod
}

async function flush(): Promise<void> {
  for (let i = 0; i < 5; i++) await Promise.resolve()
}

describe('every window agrees on which world is on screen', () => {
  beforeEach(() => {
    FakeBroadcastChannel.bus.clear()
    localStorage.clear()
    vi.stubGlobal('BroadcastChannel', FakeBroadcastChannel)
  })

  afterEach(() => {
    vi.unstubAllGlobals()
    vi.resetModules()
  })

  const dataOf = (w: Win): Data => {
    const { slaves, slaveOrder, activeProfileId, parkedMaster, worldEpoch } = w.useLocalProfilesStore.getState()
    return { slaves, slaveOrder, activeProfileId, parkedMaster, worldEpoch }
  }

  it('a slave added and a switch made in window A reach window B, which was already open', async () => {
    const a = await openWindow()
    const b = await openWindow()
    expect(a.useLocalProfilesStore).not.toBe(b.useLocalProfilesStore)

    const added = a.useLocalProfilesStore.getState().addSlave('alpha', world('alpha'))
    if (!added.ok) throw new Error('addSlave failed')
    a.useLocalProfilesStore.getState().swapActive(added.id, world('M'), 1)
    await flush()

    expect(dataOf(b)).toEqual(dataOf(a))
    expect(dataOf(b).activeProfileId).toBe(added.id)
    assertInvariant(dataOf(b))
  })

  it('a window opened later starts from what storage holds', async () => {
    const a = await openWindow()
    const added = a.useLocalProfilesStore.getState().addSlave('alpha', world('alpha'))
    if (!added.ok) throw new Error('addSlave failed')
    const c = await openWindow()
    expect(dataOf(c)).toEqual(dataOf(a))
  })

  it('a corrupt record is repaired to the SAME state in both windows (the rescue is deterministic)', async () => {
    localStorage.setItem(
      STORAGE_KEYS.LOCAL_PROFILES,
      JSON.stringify({ state: { slaves: {}, slaveOrder: [], activeProfileId: 'master', parkedMaster: world('M'), worldEpoch: 1 }, version: 1 }),
    )
    const a = await openWindow()
    const b = await openWindow()
    expect(dataOf(a)).toEqual(dataOf(b))
    expect(dataOf(a).slaveOrder).toEqual(['recovered'])
  })
})

// A profile's appearance — name, icon (+ weight), colour — is what the Home button shows. The master has one too
// (`master`), and with nothing set it is `{ name: null }`: the button is then today's `Home` + logo.
describe('appearance', () => {
  const LOOK = { icon: 'Rocket', iconWeight: 'fill', color: '#3b82f6' } as const

  it('a fresh store has an unnamed, unadorned master', () => {
    expect(get().master).toEqual({ name: null })
  })

  it('is persisted (device-local like the rest of this store: see "is not in the sync allowlist")', () => {
    expect(get().setProfileAppearance('master', { name: 'Work', ...LOOK })).toEqual({ ok: true })
    expect(persistedEnvelope().state.master).toEqual({ name: 'Work', ...LOOK })
  })

  describe('setProfileAppearance', () => {
    it('master: sets what the patch names and leaves the rest', () => {
      get().setProfileAppearance('master', { name: '  Work  ', icon: 'Rocket' })
      expect(get().master).toEqual({ name: 'Work', icon: 'Rocket' })
      get().setProfileAppearance('master', { color: '#3B82F6' })
      expect(get().master).toEqual({ name: 'Work', icon: 'Rocket', color: '#3b82f6' })
    })

    it('master: null clears a field — the name included (back to `Home`), and a blank name is null', () => {
      get().setProfileAppearance('master', { name: 'Work', ...LOOK })
      get().setProfileAppearance('master', { name: null, color: null })
      expect(get().master).toEqual({ name: null, icon: 'Rocket', iconWeight: 'fill' })
      get().setProfileAppearance('master', { name: 'Work' })
      get().setProfileAppearance('master', { name: '   ' })
      expect(get().master.name).toBeNull()
    })

    it('clearing the icon clears its weight: a weight alone means nothing', () => {
      get().setProfileAppearance('master', LOOK)
      get().setProfileAppearance('master', { icon: null })
      expect(get().master).toEqual({ name: null, color: '#3b82f6' })
    })

    it('a slave: the same, except that its name cannot be cleared', () => {
      const a = add('a')
      expect(get().setProfileAppearance(a, { name: 'Scratch', ...LOOK })).toEqual({ ok: true })
      expect(get().slaves[a]).toMatchObject({ id: a, name: 'Scratch', ...LOOK })
      expect(get().slaves[a].world).not.toBeNull() // the world is not an appearance
      const before = get().slaves
      expect(get().setProfileAppearance(a, { name: null })).toEqual({ ok: false, reason: 'bad-name' })
      expect(get().setProfileAppearance(a, { name: ' ' })).toEqual({ ok: false, reason: 'bad-name' })
      expect(get().slaves).toBe(before)
    })

    it('an unknown id is refused', () => {
      expect(get().setProfileAppearance('nope', { icon: 'Rocket' })).toEqual({ ok: false, reason: 'not-found' })
    })

    it.each([
      [{ icon: 'NotAnIcon' }, 'bad-icon'],
      [{ icon: '<b>x</b>' }, 'bad-icon'],
      [{ icon: 7 }, 'bad-icon'],
      [{ iconWeight: 'heavy' }, 'bad-weight'],
      [{ color: 'red' }, 'bad-color'],
      [{ color: '#12345' }, 'bad-color'],
      [{ color: 'url(x)' }, 'bad-color'],
      [{ name: 7 }, 'bad-name'],
    ])('a bad value refuses the WHOLE patch, state untouched: %j → %s', (patch, reason) => {
      get().setProfileAppearance('master', { name: 'Work', ...LOOK })
      const before = get().master
      expect(get().setProfileAppearance('master', { name: 'Other', ...(patch as object) })).toEqual({ ok: false, reason })
      expect(get().master).toBe(before)
    })
  })

  describe('a copy starts plain', () => {
    it('addSlave gives the new slave a name and nothing else, whatever the master looks like', () => {
      get().setProfileAppearance('master', { name: 'Work', ...LOOK })
      const a = add('copy')
      expect(get().slaves[a]).toEqual({ id: a, name: 'copy', createdAt: expect.any(Number), shownHostIds: [], world: expect.anything() })
    })
  })

  describe('promoteSlave — the appearance goes with the WORLD, not with the label', () => {
    it('the promoted slave\'s look becomes the master\'s; the old master\'s goes to the demoted slave', () => {
      get().setProfileAppearance('master', { name: 'Work', icon: 'Briefcase', color: '#ef4444' })
      const a = add('Scratch')
      get().setProfileAppearance(a, LOOK)
      const r = get().promoteSlave(a, 'fallback', 1)
      if (!r.ok) throw new Error(r.reason)
      expect(get().master).toEqual({ name: 'Scratch', ...LOOK })
      expect(get().slaves[r.demotedId]).toMatchObject({ name: 'Work', icon: 'Briefcase', color: '#ef4444' })
      expect(get().slaves[r.demotedId].iconWeight).toBeUndefined()
    })

    it('an unnamed master takes `demotedName` as the demoted slave\'s name; nothing else is invented', () => {
      const a = add('Scratch')
      const r = get().promoteSlave(a, 'This Mac', 1)
      if (!r.ok) throw new Error(r.reason)
      expect(get().slaves[r.demotedId]).toEqual({ id: r.demotedId, name: 'This Mac', createdAt: expect.any(Number), shownHostIds: [], world: null })
      expect(get().master).toEqual({ name: 'Scratch' })
    })
  })

  describe('rehydrate', () => {
    const base = { slaveOrder: ['a'], activeProfileId: 'master', parkedMaster: null, worldEpoch: 0 }

    it('keeps a well-formed appearance', async () => {
      await rehydrateFrom({ ...base, slaves: { a: { ...slave('a', 'a', world('a')), ...LOOK } }, master: { name: 'Work', ...LOOK } })
      expect(get().master).toEqual({ name: 'Work', ...LOOK })
      expect(get().slaves.a).toMatchObject(LOOK)
    })

    it('storage from before appearances existed: an unnamed master', async () => {
      get().setProfileAppearance('master', { name: 'Work' })
      await rehydrateFrom({ ...base, slaves: { a: slave('a', 'a', world('a')) } })
      expect(get().master).toEqual({ name: null })
    })

    it.each([
      ['an unknown icon name', { icon: 'NotAnIcon', iconWeight: 'fill', color: '#3b82f6' }, { color: '#3b82f6' }],
      ['markup as an icon', { icon: '<img src=x>', color: '#3b82f6' }, { color: '#3b82f6' }],
      ['a bad weight', { icon: 'Rocket', iconWeight: 'heavy' }, { icon: 'Rocket' }],
      ['a weight without an icon', { iconWeight: 'fill' }, {}],
      ['a bad colour', { icon: 'Rocket', color: 'expression(alert(1))' }, { icon: 'Rocket' }],
      ['an upper-case colour', { color: '#3B82F6' }, { color: '#3b82f6' }],
    ])('%s: that field goes, the profile stays', async (_, stored, kept) => {
      await rehydrateFrom({ ...base, slaves: { a: { ...slave('a', 'a', world('a')), ...stored } }, master: { name: 'Work', ...stored } })
      expect(get().master).toEqual({ name: 'Work', ...kept })
      expect(get().slaves.a).toEqual({ ...slave('a', 'a', world('a')), ...kept })
    })

    it.each([null, 7, 'x', [], { name: 7 }, { name: '  ' }])('a malformed master record (%j) is an unnamed master', async (master) => {
      await rehydrateFrom({ ...base, slaves: { a: slave('a', 'a', world('a')) }, master })
      expect(get().master).toEqual({ name: null })
    })

    it('the rescued parked master (pointer says master, a world is parked) keeps no look: it is a recovery, not a profile anyone styled', async () => {
      await rehydrateFrom({ slaves: {}, slaveOrder: [], activeProfileId: 'master', parkedMaster: world('m'), worldEpoch: 0, master: { name: 'Work', ...LOOK } })
      expect(get().master).toEqual({ name: 'Work', ...LOOK })
      expect(get().slaves.recovered).toEqual(slave('recovered', 'Recovered master', world('m'), 0))
    })
  })
})

// A name is rendered as it is — the Home row, the menu, `title`, `aria-label`. Characters nobody can see re-order
// the text around them or make two names look alike, so they are removed; nothing else is touched.
describe('profile names — invisible characters are removed, and nothing else', () => {
  const RLO = '\u202E'
  const ZWSP = '\u200B'
  const DIRTY: [string, string, string][] = [
    ['a bidi override', `Work${RLO}abc`, 'Workabc'],
    ['a zero-width space', `Work${ZWSP}`, 'Work'],
    ['a line break and a tab', 'Work\nnight\tshift', 'Worknightshift'],
  ]

  describe('normalizeLocalProfileName', () => {
    it.each(DIRTY)('%s', (_, dirty, clean) => {
      expect(normalizeLocalProfileName(dirty)).toBe(clean)
    })

    it.each([
      ['C0', '\u0000\u0007\u001B\u007F'],
      ['C1', '\u0080\u0085\u009F'],
      ['bidi marks and embeddings', '\u061C\u200E\u200F\u202A\u202B\u202C\u202D\u202E'],
      ['bidi isolates', '\u2066\u2067\u2068\u2069'],
      ['zero-width and invisible format', '\u200B\u200C\u200D\u2060\uFEFF\u00AD'],
    ])('every one of %s goes', (_, chars) => {
      for (const ch of Array.from(chars)) expect(normalizeLocalProfileName(`a${ch}b`), `U+${ch.codePointAt(0)!.toString(16)}`).toBe('ab')
    })

    it('nothing but invisible characters is no name at all', () => {
      expect(normalizeLocalProfileName(`${ZWSP}${RLO}\n`)).toBeNull()
      expect(normalizeLocalProfileName(` ${ZWSP} `)).toBeNull()
    })

    it('removal comes first, then trim, then the 64 code point cut', () => {
      expect(normalizeLocalProfileName(`${ZWSP} Work ${RLO}`)).toBe('Work') // the spaces the removal exposed are trimmed
      expect(normalizeLocalProfileName(`${ZWSP.repeat(10)}${'x'.repeat(64)}`)).toBe('x'.repeat(64)) // invisible ones do not use up the budget
      expect(Array.from(normalizeLocalProfileName('😀'.repeat(70))!)).toHaveLength(DEVICE_NAME_MAX_CODE_POINTS)
    })

    it.each([
      ['CJK', '工作用 プロファイル 작업'],
      ['an emoji with a variation selector and a skin tone', '❤️ 👍🏽 Work'],
      ['a combining accent, NFC', 'Caf\u00E9'],
      ['a combining accent, NFD — kept decomposed: no Unicode normalisation happens here', 'Cafe\u0301'],
      ['Arabic and Hebrew (right-to-left by themselves, no control needed)', 'عمل עבודה'],
      ['inner spaces', 'My  work'],
      ['a no-break space inside', 'a\u00A0b'],
    ])('%s is left exactly as it is', (_, name) => {
      expect(normalizeLocalProfileName(name)).toBe(name)
    })

    // THE TRADE-OFF: U+200D also joins emoji. Keeping it would keep a zero-width hole in the rule.
    it('a ZWJ emoji sequence comes apart — uglier, not wrong; consistency over ligatures', () => {
      expect(normalizeLocalProfileName('👨\u200D👩\u200D👧')).toBe('👨👩👧')
    })
  })

  describe('every way in', () => {
    it.each(DIRTY)('addSlave — %s', (_, dirty, clean) => {
      const r = get().addSlave(dirty, world('a'))
      if (!r.ok) throw new Error(r.reason)
      expect(get().slaves[r.id].name).toBe(clean)
    })

    it.each(DIRTY)('renameSlave — %s', (_, dirty, clean) => {
      const a = add('a')
      expect(get().renameSlave(a, dirty)).toEqual({ ok: true })
      expect(get().slaves[a].name).toBe(clean)
    })

    it.each(DIRTY)('setProfileAppearance, master and slave — %s', (_, dirty, clean) => {
      const a = add('a')
      get().setProfileAppearance('master', { name: dirty })
      get().setProfileAppearance(a, { name: dirty })
      expect(get().master.name).toBe(clean)
      expect(get().slaves[a].name).toBe(clean)
    })

    it.each(DIRTY)('promoteSlave\'s demotedName — %s', (_, dirty, clean) => {
      const a = add('a')
      const r = get().promoteSlave(a, dirty, 1)
      if (!r.ok) throw new Error(r.reason)
      expect(get().slaves[r.demotedId].name).toBe(clean)
    })

    it.each(DIRTY)('rehydrate cleans what an older build stored — %s', async (_, dirty, clean) => {
      await rehydrateFrom({ slaves: { a: slave('a', dirty, world('a')) }, slaveOrder: ['a'], activeProfileId: 'master', parkedMaster: null, worldEpoch: 0, master: { name: dirty } })
      expect(get().slaves.a.name).toBe(clean)
      expect(get().master.name).toBe(clean)
    })

    it('a name that is empty once cleaned: refused for a slave (nothing changes), null for the master, `Recovered` on rehydrate', async () => {
      const invisible = `${ZWSP}${RLO}`
      expect(get().addSlave(invisible, world('x'))).toEqual({ ok: false, reason: 'bad-name' })
      const a = add('a')
      const before = get().slaves
      expect(get().renameSlave(a, invisible)).toEqual({ ok: false, reason: 'bad-name' })
      expect(get().setProfileAppearance(a, { name: invisible })).toEqual({ ok: false, reason: 'bad-name' })
      expect(get().slaves).toBe(before)
      get().setProfileAppearance('master', { name: 'Work' })
      expect(get().setProfileAppearance('master', { name: invisible })).toEqual({ ok: true })
      expect(get().master.name).toBeNull()

      await rehydrateFrom({ slaves: { a: slave('a', invisible, world('a')) }, slaveOrder: ['a'], activeProfileId: 'master', parkedMaster: null, worldEpoch: 0, master: { name: invisible } })
      expect(get().slaves.a.name).toBe('Recovered')
      expect(get().master.name).toBeNull()
    })
  })
})

// === Per-workbench shown hosts (2026-09-25 plan, A1): a slave's own shown-hosts list lives on its record ===

describe('shownHostIds — a local workbench keeps its own shown-hosts list', () => {
  const base = { slaveOrder: ['a'], activeProfileId: 'master', parkedMaster: null, worldEpoch: 0 }

  describe('rehydrate (the upgrade is this default: plan §0.5)', () => {
    it('a record from before the field existed reads as [] — every host hidden in that workbench', async () => {
      await rehydrateFrom({ ...base, slaves: { a: { id: 'a', name: 'a', createdAt: 1, world: world('a') } } })
      expect(get().slaves.a.shownHostIds).toEqual([])
    })

    it("a stored list is sanitised like the master's: strings only, deduped keeping the first, unknown ids and order kept", async () => {
      await rehydrateFrom({ ...base, slaves: { a: { ...slave('a', 'a', world('a')), shownHostIds: ['d1_z', 'local-1', 3, null, 'd1_z', 'd1_a'] } } })
      expect(get().slaves.a.shownHostIds).toEqual(['d1_z', 'local-1', 'd1_a'])
    })

    it.each([['a string', 'd1_a'], ['an object', { ids: ['d1_a'] }], ['null', null]])('a non-array (%s) reads as []', async (_label, stored) => {
      await rehydrateFrom({ ...base, slaves: { a: { ...slave('a', 'a', world('a')), shownHostIds: stored } } })
      expect(get().slaves.a.shownHostIds).toEqual([])
    })

    it('the rescued parked master is created with [] (it states its list)', async () => {
      await rehydrateFrom({ slaves: {}, slaveOrder: [], activeProfileId: 'master', parkedMaster: world('m'), worldEpoch: 0 })
      expect(get().slaves.recovered.shownHostIds).toEqual([])
    })

    it('a reload keeps the list', async () => {
      const a = add('alpha', world('alpha'), ['d1_a', 'd1_b'])
      await rehydrateFrom(persistedEnvelope().state)
      expect(get().slaves[a].shownHostIds).toEqual(['d1_a', 'd1_b'])
    })
  })

  describe('every path that creates a record states its list', () => {
    it('addSlave: [] unless a list is given; a given list is sanitised and kept', () => {
      const a = add('alpha')
      expect(get().slaves[a].shownHostIds).toEqual([])
      const b = add('beta', world('beta'), ['d1_a', 'd1_a', 'local-2'])
      expect(get().slaves[b].shownHostIds).toEqual(['d1_a', 'local-2'])
    })

    it('promoteSlave: the demoted master is created with a list', () => {
      const a = add('alpha')
      const r = get().promoteSlave(a, 'old', get().worldEpoch + 1)
      if (!r.ok) throw new Error(r.reason)
      expect(Array.isArray(get().slaves[r.demotedId].shownHostIds)).toBe(true)
    })
  })

  describe('every path that rebuilds a record keeps the list (codex #1)', () => {
    const LIST = ['d1_a', 'local-2']

    it('setProfileAppearance keeps the same array', () => {
      const a = add('alpha', world('alpha'), LIST)
      const list = get().slaves[a].shownHostIds
      expect(get().setProfileAppearance(a, { name: 'Renamed', icon: 'Rocket', color: '#3b82f6' })).toEqual({ ok: true })
      expect(get().slaves[a].shownHostIds).toBe(list)
      expect(get().setProfileAppearance(a, { icon: null, color: null })).toEqual({ ok: true })
      expect(get().slaves[a].shownHostIds).toBe(list)
    })

    it('renameSlave keeps the same array', () => {
      const a = add('alpha', world('alpha'), LIST)
      const list = get().slaves[a].shownHostIds
      get().renameSlave(a, 'beta')
      expect(get().slaves[a].shownHostIds).toBe(list)
    })

    it('swapActive, replaceParkedWorld, updateParkedWorlds keep it', () => {
      const a = add('alpha', world('alpha'), LIST)
      const list = get().slaves[a].shownHostIds
      swap(a, world('M'))
      expect(get().slaves[a].shownHostIds).toBe(list)
      swap('master', world('alpha'))
      expect(get().slaves[a].shownHostIds).toBe(list)
      get().replaceParkedWorld(a, world('alpha2'))
      expect(get().slaves[a].shownHostIds).toBe(list)
      get().updateParkedWorlds((w) => ({ ...w }))
      expect(get().slaves[a].shownHostIds).toBe(list)
    })

    it("promoteSlave keeps every OTHER slave's list", () => {
      const a = add('alpha')
      const b = add('beta', world('beta'), LIST)
      const list = get().slaves[b].shownHostIds
      expect(get().promoteSlave(a, 'old', get().worldEpoch + 1)).toMatchObject({ ok: true })
      expect(get().slaves[b].shownHostIds).toBe(list)
    })
  })

  describe('setSlaveShownHosts', () => {
    it("maps the slave's list and persists it", () => {
      const a = add('alpha', world('alpha'), ['d1_a'])
      expect(get().setSlaveShownHosts(a, (ids) => [...ids, 'd1_b'])).toEqual({ ok: true })
      expect(get().slaves[a].shownHostIds).toEqual(['d1_a', 'd1_b'])
      expect((persistedEnvelope().state.slaves as Record<string, LocalProfile>)[a].shownHostIds).toEqual(['d1_a', 'd1_b'])
    })

    it('works for the slave on screen too, and leaves its world and the other slaves alone', () => {
      const a = add('alpha')
      const b = add('beta')
      swap(a, world('M'))
      const other = get().slaves[b]
      expect(get().setSlaveShownHosts(a, () => ['d1_x'])).toEqual({ ok: true })
      expect(get().slaves[a]).toMatchObject({ world: null, shownHostIds: ['d1_x'] })
      expect(get().slaves[b]).toBe(other)
    })

    it('the result is sanitised', () => {
      const a = add('alpha')
      get().setSlaveShownHosts(a, () => ['d1_x', 'd1_x', 7 as never])
      expect(get().slaves[a].shownHostIds).toEqual(['d1_x'])
    })

    it('unchanged (the same reference back) → no set: the state object is the same', () => {
      const a = add('alpha', world('alpha'), ['d1_a'])
      const before = get()
      expect(get().setSlaveShownHosts(a, (ids) => ids)).toEqual({ ok: true })
      expect(get()).toBe(before)
    })

    it("an unknown id, and the master (its list is useShownHostsStore's), are not-found; nothing changes", () => {
      add('alpha')
      const before = get()
      expect(get().setSlaveShownHosts('nope', () => ['d1_x'])).toEqual({ ok: false, reason: 'not-found' })
      expect(get().setSlaveShownHosts('master', () => ['d1_x'])).toEqual({ ok: false, reason: 'not-found' })
      expect(get()).toBe(before)
    })
  })
})

