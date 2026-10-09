// spa/src/components/team/TeamDisplayProvider.index.test.tsx — the team index is built once per input change (plan TI-1a,
// review #6): a render that changes none of its inputs does not rebuild it, a tab / roster / session change does once.
import { describe, it, expect, beforeEach, vi } from 'vitest'
import { act, render } from '@testing-library/react'
import { TeamDisplayProvider } from './TeamDisplayProvider'
import * as teamIndex from '../../lib/team/team-index'
import { useTeamRosterStore } from '../../stores/useTeamRosterStore'
import { useTabStore } from '../../stores/useTabStore'
import { useSessionStore } from '../../stores/useSessionStore'
import { useTeamUiStore } from '../../stores/useTeamUiStore'
import type { Tab } from '../../types/tab'

vi.mock('../../lib/team/team-index', async (importOriginal) => {
  const real = await importOriginal<typeof import('../../lib/team/team-index')>()
  return { ...real, buildTeamIndex: vi.fn(real.buildTeamIndex) }
})

const tab = (id: string, name: string): Tab => ({
  id, pinned: false, locked: false, createdAt: 0,
  layout: { type: 'leaf', pane: { id: `p-${id}`, content: { kind: 'tmux-session', hostId: 'h1', sessionCode: `c-${name}`, mode: 'terminal', cachedName: name, tmuxInstance: 'i' } } },
})
const roster = (label = '') => ({
  id: 't1', host_id: 'd', created_at: 1, team_name: '', team_label: label,
  lead: { session_id: 'L', ref: '_L', address: 'a/b', live: true, tmux_session: 'lead-tm' }, members: [],
})

const builds = () => vi.mocked(teamIndex.buildTeamIndex).mock.calls.length

beforeEach(() => {
  vi.mocked(teamIndex.buildTeamIndex).mockClear()
  useTeamRosterStore.getState().reset()
  useTeamUiStore.setState({ memberOrder: {}, collapsed: {}, panelMode: {}, ghostWorkspace: {}, teamBeadHost: true })
  useTabStore.setState({ tabs: { lead: tab('lead', 'lead-tm') }, tabOrder: ['lead'], activeTabId: null })
  useSessionStore.setState({ sessions: {} })
})

describe('TeamDisplayProvider builds the team index once per input change', () => {
  it('not again for a write that changes none of its inputs; once for a tab change; once for a roster change', () => {
    render(<TeamDisplayProvider><span /></TeamDisplayProvider>)
    const initial = builds()
    expect(initial).toBeGreaterThanOrEqual(1)

    act(() => useTabStore.setState({ activeTabId: 'lead' })) // the active tab is not an input
    act(() => useTeamUiStore.getState().setCollapsed('h1\u0000t1', true)) // nor is a collapse
    expect(builds()).toBe(initial)

    act(() => useTabStore.setState({ tabs: { ...useTabStore.getState().tabs, other: tab('other', 'x-tm') } }))
    expect(builds()).toBe(initial + 1)

    act(() => useTeamRosterStore.getState().apply('h1', [roster('A')]))
    expect(builds()).toBe(initial + 2)
  })
})
