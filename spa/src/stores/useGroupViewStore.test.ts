// spa/src/stores/useGroupViewStore.test.ts — what the device remembers of the task-group view (task groups spec §6.2, §6.3): writes that change nothing keep the state's identity, arrivals are sanitised, and the host rewrite
// moves only the host of a team key.
import { beforeEach, describe, expect, it, vi } from 'vitest'

const registerSpy = vi.hoisted(() => vi.fn())

vi.mock('../lib/storage/sync', () => ({
  syncManager: { register: registerSpy, notify: vi.fn(), destroy: vi.fn() },
  createSyncManager: vi.fn(),
}))

import { rewriteGroupViewHosts, sanitizeGroupView, useGroupViewStore, type GroupViewData } from './useGroupViewStore'
import { manualGroupKey, teamGroupKey, UNGROUPED } from '../lib/task-groups/group-key'
import { STORAGE_KEYS } from '../lib/storage'

const TEAM_A = teamGroupKey('hostA\u0000t1')
const TEAM_B = teamGroupKey('hostB\u0000t1')
const MANUAL = manualGroupKey('aaaaaaaa')
const teamKeyOf = (host: string, id: string) => `${host}\u0000${id}`
const get = () => useGroupViewStore.getState()

beforeEach(() => {
  localStorage.clear()
  useGroupViewStore.setState({ selectedGroup: null, lastActiveTab: {}, expanded: {}, lastByView: { workspace: null, group: null } })
})

describe('useGroupViewStore', () => {
  it('is registered with syncManager under its storage key, so this device\'s windows read each other', () => {
    expect(registerSpy).toHaveBeenCalledWith(STORAGE_KEYS.GROUP_VIEW, useGroupViewStore)
  })

  it('recordGroupActive writes the selection, the group\'s last tab and the view\'s last group in one update; the same call again changes nothing', () => {
    let writes = 0
    const unsub = useGroupViewStore.subscribe(() => { writes += 1 })
    get().recordGroupActive(MANUAL, 't1')
    expect([get().selectedGroup, get().lastActiveTab, get().lastByView.group]).toEqual([MANUAL, { [MANUAL]: 't1' }, MANUAL])
    expect(writes).toBe(1)
    get().recordGroupActive(MANUAL, 't1')
    expect(writes).toBe(1)
    unsub()
  })

  it('another tab of the same group only moves that group\'s last tab', () => {
    get().recordGroupActive(MANUAL, 't1')
    get().recordGroupActive(TEAM_A, 't9')
    get().recordGroupActive(MANUAL, 't2')
    expect(get().lastActiveTab).toEqual({ [MANUAL]: 't2', [TEAM_A]: 't9' })
    expect(get().selectedGroup).toBe(MANUAL)
  })

  it('recordWorkspace moves the workspace view\'s last selection and leaves the group view\'s alone', () => {
    get().recordGroupActive(MANUAL, 't1')
    get().recordWorkspace('w2')
    expect(get().lastByView).toEqual({ workspace: 'w2', group: MANUAL })
    const before = get().lastByView
    get().recordWorkspace('w2')
    expect(get().lastByView).toBe(before)
  })

  it('selectGroup selects with no tab (an empty group) and the view\'s last group follows; null clears only the selection', () => {
    get().selectGroup(UNGROUPED)
    expect([get().selectedGroup, get().lastByView.group]).toEqual([UNGROUPED, UNGROUPED])
    get().selectGroup(null)
    expect([get().selectedGroup, get().lastByView.group]).toEqual([null, UNGROUPED])
  })

  it('setExpanded keeps the state when the value is the same; forgetGroups drops what it says yes to everywhere', () => {
    get().setExpanded(TEAM_A, false)
    const before = get().expanded
    get().setExpanded(TEAM_A, false)
    expect(get().expanded).toBe(before)
    get().recordGroupActive(TEAM_A, 't1')
    get().recordGroupActive(MANUAL, 't2')
    get().forgetGroups((k) => k === TEAM_A)
    expect(get().expanded).toEqual({})
    expect(get().lastActiveTab).toEqual({ [MANUAL]: 't2' })
    expect(get().selectedGroup).toBe(MANUAL)
    get().forgetGroups((k) => k === MANUAL)
    expect([get().selectedGroup, get().lastByView.group]).toEqual([null, null])
  })

  it('forgetTeams forgets one host\'s team keys that are not live; forgetManual forgets manual keys whose id is not live', () => {
    const live = teamGroupKey('hostA\u0000t1')
    const dead = teamGroupKey('hostA\u0000t2')
    const other = teamGroupKey('hostB\u0000t2')
    useGroupViewStore.setState({ selectedGroup: dead, lastActiveTab: { [live]: 'a', [dead]: 'b', [other]: 'c', [MANUAL]: 'd' }, expanded: { [dead]: false }, lastByView: { workspace: null, group: null } })
    get().forgetTeams('hostA', [teamKeyOf('hostA', 't1')])
    expect(get().lastActiveTab).toEqual({ [live]: 'a', [other]: 'c', [MANUAL]: 'd' })
    expect([get().selectedGroup, get().expanded]).toEqual([null, {}])
    const before = get().lastActiveTab
    get().forgetTeams('hostA', [teamKeyOf('hostA', 't1')])
    expect(get().lastActiveTab).toBe(before) // nothing more to forget: the state keeps its identity
    get().forgetManual([])
    expect(get().lastActiveTab).toEqual({ [live]: 'a', [other]: 'c' })
  })

  it('persists under its own key, and a rehydrate sanitises what storage holds', async () => {
    get().recordGroupActive(MANUAL, 't1')
    expect(JSON.parse(localStorage.getItem(STORAGE_KEYS.GROUP_VIEW)!).state.selectedGroup).toBe(MANUAL)
    localStorage.setItem(STORAGE_KEYS.GROUP_VIEW, JSON.stringify({ state: { selectedGroup: 'junk', lastActiveTab: { [MANUAL]: 5, bad: 't', [TEAM_A]: 't3' }, expanded: { [MANUAL]: 'yes' }, lastByView: { workspace: '', group: MANUAL } }, version: 1 }))
    await useGroupViewStore.persist.rehydrate()
    expect(get().selectedGroup).toBeNull()
    expect(get().lastActiveTab).toEqual({ [TEAM_A]: 't3' })
    expect(get().expanded).toEqual({})
    expect(get().lastByView).toEqual({ workspace: null, group: MANUAL })
  })
})

describe('sanitizeGroupView', () => {
  it('anything that is not an object gives the empty data', () => {
    for (const raw of [null, undefined, 5, 'x', []]) expect(sanitizeGroupView(raw)).toEqual({ selectedGroup: null, lastActiveTab: {}, expanded: {}, lastByView: { workspace: null, group: null } })
  })
})

describe('rewriteGroupViewHosts', () => {
  const data = (over: Partial<GroupViewData> = {}): GroupViewData => ({ selectedGroup: TEAM_A, lastActiveTab: { [TEAM_A]: 't1', [MANUAL]: 't2' }, expanded: { [TEAM_A]: false }, lastByView: { workspace: 'w1', group: TEAM_A }, ...over })
  const map = (id: string) => (id === 'hostA' ? 'wire9' : id)

  it('moves the host of every team key — the selection, both records and the view\'s last group — and nothing else', () => {
    const next = rewriteGroupViewHosts(data(), map)
    const moved = teamGroupKey('wire9\u0000t1')
    expect(next).toEqual({ selectedGroup: moved, lastActiveTab: { [moved]: 't1', [MANUAL]: 't2' }, expanded: { [moved]: false }, lastByView: { workspace: 'w1', group: moved } })
  })

  it('is the same state back when no team key moves', () => {
    const d = data({ selectedGroup: TEAM_B, lastActiveTab: { [TEAM_B]: 't1', [MANUAL]: 't2' }, expanded: {}, lastByView: { workspace: null, group: MANUAL } })
    expect(rewriteGroupViewHosts(d, map)).toBe(d)
  })

  it('two keys the mapping makes equal leave one: the first stays', () => {
    const merged = teamGroupKey('wire9\u0000t1')
    const d = data({ selectedGroup: null, lastActiveTab: { [merged]: 'keep', [TEAM_A]: 'drop' }, expanded: {}, lastByView: { workspace: null, group: null } })
    expect(rewriteGroupViewHosts(d, map).lastActiveTab).toEqual({ [merged]: 'keep' })
  })
})
