// spa/src/components/team/usePersonalGroup.test.tsx — the title bar's group hook is not woken by other tabs (TG-5c-2, 1f's nit): only the full form, which prints the group's session count, listens to every tab.
import { describe, it, expect, beforeEach } from 'vitest'
import { act, renderHook } from '@testing-library/react'
import { usePersonalGroup } from './usePersonalPanel'
import { useTabStore } from '../../stores/useTabStore'
import { useManualGroupsStore } from '../../stores/useManualGroupsStore'
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
})
