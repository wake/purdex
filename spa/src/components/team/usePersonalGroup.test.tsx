// spa/src/components/team/usePersonalGroup.test.tsx — the title bar's group hook is not woken by other tabs (TG-5c-2, 1f's nit): only the full form, which prints the group's session count, listens to every tab.
import { describe, it, expect, beforeEach } from 'vitest'
import { act, renderHook } from '@testing-library/react'
import { usePersonalGroup } from './usePersonalPanel'
import { useTabStore } from '../../stores/useTabStore'
import { useManualGroupsStore } from '../../stores/useManualGroupsStore'
import { useTeamRosterStore } from '../../stores/useTeamRosterStore'
import { sess } from '../../lib/team/__tests__/team-fixture'
import { useRootCacheStore } from '../../lib/task-groups/root-cache'
import type { PersonalTarget } from '../../lib/task-groups/personal'
import type { Tab } from '../../types/tab'

const tab = (id: string, agent: string): Tab => ({
  id, pinned: false, locked: false, createdAt: 0,
  layout: { type: 'leaf', pane: { id: `p-${id}`, content: { kind: 'tmux-session', hostId: 'h1', sessionCode: `c-${id}`, mode: 'terminal', cachedName: id, tmuxInstance: 'i', rebuild: { agent: { type: 'cc', sessionId: agent } } } } },
}) as never
const target: PersonalTarget = { tabId: 't1', hostId: 'h1', sessionCode: 'c-t1', agentSessionId: 'A1', agentType: 'cc', ended: false }

beforeEach(() => {
  useTabStore.setState({ tabs: { t1: tab('t1', 'A1') }, tabOrder: ['t1'], activeTabId: 't1' } as never)
  useManualGroupsStore.setState({ order: ['aaaaaaaa'], groups: { aaaaaaaa: { name: 'G', members: [{ host: 'h1', root: 'R' }], createdAt: 1 } } })
  useRootCacheStore.setState({ roots: { h1: { A1: 'R', A2: 'R' } }, addresses: {}, titles: {} })
})

describe('usePersonalGroup', () => {
  it('without the count, another tab appearing does not render the hook again', () => {
    let renders = 0
    renderHook(() => { renders++; return usePersonalGroup(target, false) })
    const before = renders
    act(() => { useTabStore.setState((s) => ({ tabs: { ...s.tabs, t2: tab('t2', 'A2') } })) })
    expect(renders).toBe(before)
  })
  it('with the count, it follows the other tabs of the group', () => {
    const { result } = renderHook(() => usePersonalGroup(target, true))
    expect(result.current.group).toMatchObject({ kind: 'manual', count: 1 })
    act(() => { useTabStore.setState((s) => ({ tabs: { ...s.tabs, t2: tab('t2', 'A2') } })) })
    expect(result.current.group).toMatchObject({ kind: 'manual', count: 2 })
  })
  it('a plain tab filed under a team follows the team\'s name, and roster ticks that change nothing it shows do not render it', () => {
    const KEY = 'h1\u0000T1'
    const roster = (name: string, ctx: number) => ({ byHost: { h1: [{ id: 'T1', team_name: name, team_label: '', host_id: 'daemon', created_at: 1, lead: { ...sess('L', 'lead-tm'), context: { used_percentage: ctx } }, members: [] }] } }) as never
    useTeamRosterStore.setState(roster('Alpha', 1))
    useTabStore.setState((s) => ({ tabs: { ...s.tabs, t1: { ...s.tabs.t1, group: `team\u0000${KEY}` } } }) as never)
    useRootCacheStore.setState({ roots: {}, addresses: {}, titles: {} })
    let renders = 0
    const { result } = renderHook(() => { renders++; return usePersonalGroup(target, false) })
    const before = renders
    act(() => { useTeamRosterStore.setState(roster('Alpha', 2)) }) // a context reading ticked
    expect(renders).toBe(before)
    act(() => { useTeamRosterStore.setState(roster('Beta', 2)) })
    expect(renders).toBeGreaterThan(before)
    expect(result.current.group).toMatchObject({ kind: 'team', name: 'Beta' })
    act(() => { useTeamRosterStore.setState({ byHost: { h1: [] } } as never) }) // the team goes: the tab is just ungrouped (existence is part of what is watched; team colour is the team id's, not the roster's)
    expect(result.current.group).toEqual({ kind: 'ungrouped' })
  })
})
