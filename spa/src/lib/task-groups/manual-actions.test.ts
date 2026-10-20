import { beforeEach, describe, expect, it } from 'vitest'
import { TEAM_COLORS } from '../../components/team/team-display'
import { useManualGroupsStore } from '../../stores/useManualGroupsStore'
import { useTabStore } from '../../stores/useTabStore'
import type { Tab } from '../../types/tab'
import { manualGroupKey } from './group-key'
import {
  createManualGroup, defaultManualGroupName, deleteManualGroup, putMemberInGroup, recolorManualGroup, renameManualGroup, takeOutOfGroups,
} from './manual-actions'

const tab = (id: string, extra: Partial<Tab> = {}): Tab => ({
  id, pinned: false, locked: false, createdAt: 1,
  layout: { type: 'leaf', pane: { id: `p-${id}`, content: { kind: 'new-tab' } } },
  ...extra,
})
const groups = () => useManualGroupsStore.getState().groups
const order = () => useManualGroupsStore.getState().order

beforeEach(() => {
  useManualGroupsStore.setState({ order: [], groups: {} })
  useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null })
})

describe('a new group\'s colour (TG-5c)', () => {
  it('is the first swatch colour no other manual group uses, and starts again when all eight are used', () => {
    const colours = () => Object.values(groups()).map((g) => g.color)
    for (let i = 0; i < 8; i++) { const r = createManualGroup({ name: `G${i}` }); expect(r.ok).toBe(true) }
    expect(colours()).toEqual([...TEAM_COLORS])
    createManualGroup({ name: 'ninth' })
    expect(colours()[8]).toBe(TEAM_COLORS[0])
  })
  it('skips colours that are taken, wherever they are; an explicit colour is kept', () => {
    createManualGroup({ name: 'A', color: TEAM_COLORS[0] })
    const r = createManualGroup({ name: 'B' })
    expect(r.ok && groups()[r.id].color).toBe(TEAM_COLORS[1])
    const c = createManualGroup({ name: 'C', color: '#112233' })
    expect(c.ok && groups()[c.id].color).toBe('#112233')
  })
})

describe('createManualGroup', () => {
  it('an empty group, with a fresh id appended to order', () => {
    const r = createManualGroup({ name: 'Alpha', color: '#112233' })
    expect(r.ok).toBe(true)
    if (!r.ok) return
    expect(order()).toEqual([r.id])
    expect(groups()[r.id]).toMatchObject({ name: 'Alpha', color: '#112233', members: [] })
  })

  it('starts with a member when given one; with no root it refuses before touching the store', () => {
    const r = createManualGroup({ name: 'A', member: { host: 'h1', root: 'r1' } })
    if (!r.ok) throw new Error(r.reason)
    expect(groups()[r.id].members).toEqual([{ host: 'h1', root: 'r1' }])
    const before = useManualGroupsStore.getState()
    expect(createManualGroup({ name: 'B', member: { host: 'h1', root: null } })).toEqual({ ok: false, reason: 'no-root' })
    expect(useManualGroupsStore.getState()).toBe(before)
  })

  it('starting a group from a session\'s tab clears that tab\'s own `group` (it is a member now); a refused create leaves it', () => {
    useTabStore.setState({ tabs: { t1: tab('t1', { group: manualGroupKey('aaaaaaaa'), groupRank: 'V' }) } })
    expect(createManualGroup({ name: '', member: { host: 'h1', root: 'r1' }, tabId: 't1' })).toEqual({ ok: false, reason: 'bad-name' })
    expect(useTabStore.getState().tabs.t1.group).toBe(manualGroupKey('aaaaaaaa'))
    const r = createManualGroup({ name: 'A', member: { host: 'h1', root: 'r1' }, tabId: 't1' })
    expect(r.ok).toBe(true)
    expect(Object.hasOwn(useTabStore.getState().tabs.t1, 'group')).toBe(false)
    expect(useTabStore.getState().tabs.t1.groupRank).toBe('V')
  })

  it('a bad name is refused and writes nothing', () => {
    const before = useManualGroupsStore.getState()
    expect(createManualGroup({ name: '  ' })).toEqual({ ok: false, reason: 'bad-name' })
    expect(useManualGroupsStore.getState()).toBe(before)
  })
})

describe('rename / recolour', () => {
  it('act on the store and say when the group is gone', () => {
    const r = createManualGroup({ name: 'A' })
    if (!r.ok) throw new Error(r.reason)
    expect(renameManualGroup(r.id, 'B')).toEqual({ ok: true })
    expect(recolorManualGroup(r.id, '#abcdef')).toEqual({ ok: true })
    expect(groups()[r.id]).toMatchObject({ name: 'B', color: '#abcdef' })
    expect(renameManualGroup('ffffffff', 'X')).toEqual({ ok: false, reason: 'group-gone' })
  })
})

describe('members', () => {
  it('put in a group takes the member out of its old group; no root = no-root; two hosts in one group', () => {
    const a = createManualGroup({ name: 'A' })
    const b = createManualGroup({ name: 'B' })
    if (!a.ok || !b.ok) throw new Error('setup')
    expect(putMemberInGroup({ host: 'h1', root: 'r1' }, a.id)).toEqual({ ok: true })
    expect(putMemberInGroup({ host: 'h2', root: 'r1' }, a.id)).toEqual({ ok: true })
    expect(groups()[a.id].members).toHaveLength(2)
    expect(putMemberInGroup({ host: 'h1', root: 'r1' }, b.id)).toEqual({ ok: true })
    expect(groups()[a.id].members).toEqual([{ host: 'h2', root: 'r1' }])
    expect(groups()[b.id].members).toEqual([{ host: 'h1', root: 'r1' }])
    expect(putMemberInGroup({ host: 'h1', root: null }, b.id)).toEqual({ ok: false, reason: 'no-root' })
  })

  it('a member put in a group stops being placed by its tab\'s own `group` (that tab loses it)', () => {
    const a = createManualGroup({ name: 'A' })
    if (!a.ok) throw new Error('setup')
    useTabStore.setState({ tabs: { t1: tab('t1', { group: 'ungrouped', groupRank: 'V' }) } })
    putMemberInGroup({ host: 'h1', root: 'r1' }, a.id, 't1')
    expect(Object.hasOwn(useTabStore.getState().tabs.t1, 'group')).toBe(false)
    expect(useTabStore.getState().tabs.t1.groupRank).toBe('V') // the rank is its place in the bar, not its placement
  })

  it('a refused put (full / gone group) leaves the tab\'s group alone', () => {
    useTabStore.setState({ tabs: { t1: tab('t1', { group: manualGroupKey('aaaaaaaa') }) } })
    expect(putMemberInGroup({ host: 'h1', root: 'r1' }, 'ffffffff', 't1')).toEqual({ ok: false, reason: 'group-gone' })
    expect(useTabStore.getState().tabs.t1.group).toBe(manualGroupKey('aaaaaaaa'))
  })

  it('taking out removes the member and clears the tab\'s group; a plain tab (no root) only loses its group', () => {
    const a = createManualGroup({ name: 'A', member: { host: 'h1', root: 'r1' } })
    if (!a.ok) throw new Error('setup')
    useTabStore.setState({ tabs: { t1: tab('t1', { group: manualGroupKey(a.id) }), t2: tab('t2', { group: manualGroupKey(a.id) }) } })
    expect(takeOutOfGroups({ host: 'h1', root: 'r1', tabId: 't1' })).toEqual({ ok: true })
    expect(groups()[a.id].members).toEqual([])
    expect(Object.hasOwn(useTabStore.getState().tabs.t1, 'group')).toBe(false)
    expect(takeOutOfGroups({ host: 'h1', root: null, tabId: 't2' })).toEqual({ ok: true })
    expect(Object.hasOwn(useTabStore.getState().tabs.t2, 'group')).toBe(false)
    expect(takeOutOfGroups({ host: 'h1', root: null })).toEqual({ ok: false, reason: 'no-root' })
  })
})

describe('deleteManualGroup', () => {
  it('removes the group and clears the `group` of the on-screen tabs naming it; other tabs are untouched', () => {
    const a = createManualGroup({ name: 'A', member: { host: 'h1', root: 'r1' } })
    const b = createManualGroup({ name: 'B' })
    if (!a.ok || !b.ok) throw new Error('setup')
    const other = tab('t3', { group: manualGroupKey(b.id), groupRank: 'k' })
    useTabStore.setState({ tabs: { t1: tab('t1', { group: manualGroupKey(a.id), groupRank: 'V' }), t2: tab('t2'), t3: other } })
    expect(deleteManualGroup(a.id)).toEqual({ ok: true })
    expect(order()).toEqual([b.id])
    expect(Object.hasOwn(useTabStore.getState().tabs.t1, 'group')).toBe(false)
    expect(useTabStore.getState().tabs.t1.groupRank).toBe('V')
    expect(useTabStore.getState().tabs.t3).toBe(other)
    expect(deleteManualGroup(a.id)).toEqual({ ok: false, reason: 'group-gone' })
  })
})

describe('defaultManualGroupName', () => {
  it('「<base> N」: the first N no group has', () => {
    createManualGroup({ name: 'Group 1' })
    createManualGroup({ name: 'Group 3' })
    expect(defaultManualGroupName('Group')).toBe('Group 2')
  })
})
