// spa/src/stores/useTeamUiStore.test.ts — the person's per-team arrangement on this device (order, collapse, panel mode,
// ghost workspace): persisted under purdex-team-ui, never synced; pruned only by a roster frame that omits a team or by
// deleting the host.
import { describe, it, expect, beforeEach } from 'vitest'
import { useTeamUiStore } from './useTeamUiStore'
import { teamKeyOf } from '../lib/team/team-views'

const k = (host: string, team: string) => teamKeyOf(host, team)

beforeEach(() => {
  localStorage.clear()
  useTeamUiStore.setState({ memberOrder: {}, collapsed: {}, panelMode: {}, ghostWorkspace: {}, teamBeadHost: true })
})

describe('useTeamUiStore', () => {
  it('persists under purdex-team-ui and round-trips', () => {
    expect(useTeamUiStore.persist.getOptions().name).toBe('purdex-team-ui')
    const key = k('h1', 't1')
    useTeamUiStore.getState().setMemberOrder(key, ['b', 'a'])
    useTeamUiStore.getState().setCollapsed(key, true)
    useTeamUiStore.getState().setPanelMode(key, 'line')
    useTeamUiStore.getState().setGhostWorkspace(key, 'w9')
    const raw = JSON.parse(localStorage.getItem('purdex-team-ui')!)
    expect(raw.state).toEqual({
      memberOrder: { [key]: ['b', 'a'] }, collapsed: { [key]: true }, panelMode: { [key]: 'line' }, ghostWorkspace: { [key]: 'w9' },
      teamBeadHost: true,
    })
    const saved = localStorage.getItem('purdex-team-ui')!
    useTeamUiStore.setState({ memberOrder: {}, collapsed: {}, panelMode: {}, ghostWorkspace: {} }) // persists the empty state too
    localStorage.setItem('purdex-team-ui', saved)
    useTeamUiStore.persist.rehydrate()
    expect(useTeamUiStore.getState().memberOrder[key]).toEqual(['b', 'a'])
    expect(useTeamUiStore.getState().panelMode[key]).toBe('line')
  })

  it('the panel mode is absent (= full) until set, and "full" removes the entry', () => {
    const key = k('h1', 't1')
    expect(useTeamUiStore.getState().panelMode[key]).toBeUndefined()
    useTeamUiStore.getState().setPanelMode(key, 'line')
    useTeamUiStore.getState().setPanelMode(key, 'full')
    expect(key in useTeamUiStore.getState().panelMode).toBe(false)
  })

  it('collapsed false removes the entry; a ghost workspace can be cleared', () => {
    const key = k('h1', 't1')
    useTeamUiStore.getState().setCollapsed(key, true)
    useTeamUiStore.getState().setCollapsed(key, false)
    expect(key in useTeamUiStore.getState().collapsed).toBe(false)
    useTeamUiStore.getState().setGhostWorkspace(key, 'w1')
    useTeamUiStore.getState().setGhostWorkspace(key, null)
    expect(key in useTeamUiStore.getState().ghostWorkspace).toBe(false)
  })

  it('memberOrder keeps its identity through setCollapsed / setPanelMode / setGhostWorkspace', () => {
    const key = k('h1', 't1')
    useTeamUiStore.getState().setMemberOrder(key, ['a'])
    const before = useTeamUiStore.getState().memberOrder
    useTeamUiStore.getState().setCollapsed(key, true)
    useTeamUiStore.getState().setPanelMode(key, 'line')
    useTeamUiStore.getState().setGhostWorkspace(key, 'w1')
    expect(useTeamUiStore.getState().memberOrder).toBe(before)
  })

  it('setting the same order again changes nothing (no new identity)', () => {
    const key = k('h1', 't1')
    useTeamUiStore.getState().setMemberOrder(key, ['a', 'b'])
    const before = useTeamUiStore.getState().memberOrder
    useTeamUiStore.getState().setMemberOrder(key, ['a', 'b'])
    expect(useTeamUiStore.getState().memberOrder).toBe(before)
  })

  describe('forgetTeams(hostId, liveKeys)', () => {
    it('drops the host\'s teams that are not live, keeps the live ones and other hosts', () => {
      const live = k('h1', 'live'), gone = k('h1', 'gone'), other = k('h2', 'x')
      for (const key of [live, gone, other]) {
        useTeamUiStore.getState().setMemberOrder(key, ['a'])
        useTeamUiStore.getState().setCollapsed(key, true)
        useTeamUiStore.getState().setPanelMode(key, 'line')
        useTeamUiStore.getState().setGhostWorkspace(key, 'w')
      }
      useTeamUiStore.getState().forgetTeams('h1', [live])
      const s = useTeamUiStore.getState()
      for (const slice of [s.memberOrder, s.collapsed, s.panelMode, s.ghostWorkspace]) {
        expect(Object.keys(slice).sort()).toEqual([live, other].sort())
      }
    })
    it('leaves every slice untouched (same identity) when nothing is stale', () => {
      const key = k('h1', 't1')
      useTeamUiStore.getState().setMemberOrder(key, ['a'])
      useTeamUiStore.getState().setCollapsed(key, true)
      const before = useTeamUiStore.getState()
      useTeamUiStore.getState().forgetTeams('h1', [key])
      const after = useTeamUiStore.getState()
      expect(after.memberOrder).toBe(before.memberOrder)
      expect(after.collapsed).toBe(before.collapsed)
    })
    it('does not confuse a host id that is a prefix of another', () => {
      const a = k('h1', 't'), b = k('h10', 't')
      useTeamUiStore.getState().setCollapsed(a, true)
      useTeamUiStore.getState().setCollapsed(b, true)
      useTeamUiStore.getState().forgetTeams('h1', [])
      expect(Object.keys(useTeamUiStore.getState().collapsed)).toEqual([b])
    })
  })

  it('forgetHostTeams drops every team of that host and only that host', () => {
    const a = k('h1', 't1'), b = k('h1', 't2'), c = k('h2', 't1')
    for (const key of [a, b, c]) useTeamUiStore.getState().setMemberOrder(key, ['x'])
    useTeamUiStore.getState().forgetHostTeams('h1')
    expect(Object.keys(useTeamUiStore.getState().memberOrder)).toEqual([c])
  })

  it('snapshotHostTeams / restoreHostTeams carry a host\'s entries through a delete + undo', () => {
    const a = k('h1', 't1'), c = k('h2', 't1')
    useTeamUiStore.getState().setMemberOrder(a, ['x'])
    useTeamUiStore.getState().setCollapsed(a, true)
    useTeamUiStore.getState().setMemberOrder(c, ['y'])
    const snap = useTeamUiStore.getState().snapshotHostTeams('h1')
    useTeamUiStore.getState().forgetHostTeams('h1')
    useTeamUiStore.getState().restoreHostTeams(snap)
    const s = useTeamUiStore.getState()
    expect(s.memberOrder[a]).toEqual(['x'])
    expect(s.collapsed[a]).toBe(true)
    expect(s.memberOrder[c]).toEqual(['y'])
  })

  it('heals malformed persisted data to empty slices', () => {
    localStorage.setItem('purdex-team-ui', JSON.stringify({ state: { memberOrder: 'oops', collapsed: [1], panelMode: { k: 'weird' }, ghostWorkspace: { k: 5 } }, version: 0 }))
    useTeamUiStore.persist.rehydrate()
    const s = useTeamUiStore.getState()
    expect(s.memberOrder).toEqual({})
    expect(s.collapsed).toEqual({})
    expect(s.panelMode).toEqual({})
    expect(s.ghostWorkspace).toEqual({})
  })
})

describe('the bead host-icon setting (spec P7, device-local)', () => {
  it('defaults on, persists in purdex-team-ui, and is not part of any synced store', () => {
    expect(useTeamUiStore.getState().teamBeadHost).toBe(true)
    useTeamUiStore.getState().setTeamBeadHost(false)
    expect(JSON.parse(localStorage.getItem('purdex-team-ui')!).state.teamBeadHost).toBe(false)
    const saved = localStorage.getItem('purdex-team-ui')!
    useTeamUiStore.setState({ teamBeadHost: true })
    localStorage.setItem('purdex-team-ui', saved)
    useTeamUiStore.persist.rehydrate()
    expect(useTeamUiStore.getState().teamBeadHost).toBe(false)
  })
  it('a non-boolean persisted value heals to the default', () => {
    localStorage.setItem('purdex-team-ui', JSON.stringify({ state: { teamBeadHost: 'no' }, version: 0 }))
    useTeamUiStore.persist.rehydrate()
    expect(useTeamUiStore.getState().teamBeadHost).toBe(true)
  })
  it('setting the same value again does not notify', () => {
    let calls = 0
    const unsub = useTeamUiStore.subscribe(() => { calls++ })
    useTeamUiStore.getState().setTeamBeadHost(true)
    unsub()
    expect(calls).toBe(0)
  })
})
