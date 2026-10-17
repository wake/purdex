// spa/src/hooks/useTabDisplay.member.test.ts — member spec R3 on a MOUNTED tab: its unread mark follows the roster both
// ways, and its status light does not change.
import { beforeEach, describe, expect, it } from 'vitest'
import { act, renderHook } from '@testing-library/react'
import { useAgentStore } from '../stores/useAgentStore'
import { useExecutionListStore } from '../stores/useExecutionListStore'
import { useExecutionStore } from '../stores/useExecutionStore'
import { useHostStore } from '../stores/useHostStore'
import { useI18nStore } from '../stores/useI18nStore'
import { useSessionStore } from '../stores/useSessionStore'
import { useTeamRosterStore } from '../stores/useTeamRosterStore'
import { useUISettingsStore } from '../stores/useUISettingsStore'
import { useWorkerTitlePrefetchStore } from '../stores/useWorkerTitlePrefetchStore'
import { useWorkspaceStore } from '../stores/useWorkspaceStore'
import type { TeamRoster } from '../lib/team/roster'
import type { PaneLayout, Tab } from '../types/tab'
import { useTabDisplay } from './useTabDisplay'

const tmux = (id: string, sessionCode: string): PaneLayout =>
  ({ type: 'leaf', pane: { id, content: { kind: 'tmux-session', hostId: 'h1', sessionCode, mode: 'terminal', cachedName: sessionCode, tmuxInstance: '' } } })
const split = (children: PaneLayout[]): PaneLayout =>
  ({ type: 'split', id: 's', direction: 'h', children, sizes: children.map(() => 100 / children.length) })
const tab = (layout: PaneLayout): Tab => ({ id: 't1', pinned: false, locked: false, createdAt: 0, layout })
const roster = (members: string[]): TeamRoster => ({
  id: 't1', host_id: 'd', created_at: 1, team_name: '', team_label: '',
  lead: { session_id: 'L', ref: '_L', address: 'a/L', live: true, tmux_session: 'lead' },
  members: members.map((n) => ({
    session_id: n, ref: `_${n}`, address: `a/${n}`, live: true, tmux_session: n, state: 'active', origin: 'spawned', joined_at: 1,
  })),
})

beforeEach(() => {
  useTeamRosterStore.getState().reset()
  useSessionStore.setState({
    sessions: { h1: [{ code: 'a', name: 'wa', cwd: '/', mode: 'terminal' }, { code: 'b', name: 'wb', cwd: '/', mode: 'terminal' }] },
    activeHostId: null, activeCode: null,
  })
  useWorkspaceStore.setState({ workspaces: [], activeWorkspaceId: null })
  useHostStore.setState({ runtime: {}, hostOrder: ['h1'] })
  useAgentStore.setState({ unread: { 'h1:a': true }, statuses: { 'h1:a': 'waiting' }, subagents: {}, agentTypes: {}, oscTitles: {}, lastEvents: {} })
  useUISettingsStore.setState({ tabIndicatorStyle: 'badge', ccIconVariant: 'bot', codexIconVariant: 'openai', dynamicTabName: false, stripAgentTitleMarker: true })
  useI18nStore.setState({ t: (k: string) => k })
  useExecutionStore.setState({ executions: {} })
  useExecutionListStore.setState({ byHost: {} })
  useWorkerTitlePrefetchStore.setState({ byKey: {} })
})

describe('useTabDisplay — team members (R3)', () => {
  it('a mounted tab hides its unread mark when its session becomes a member and shows it again when it leaves', () => {
    const t = tab(tmux('p1', 'a'))
    const { result } = renderHook(() => useTabDisplay(t))
    expect(result.current.isUnread).toBe(true)

    act(() => useTeamRosterStore.getState().apply('h1', [roster(['wa'])]))
    expect(result.current.isUnread).toBe(false)
    expect(result.current.agentStatus).toBe('waiting')

    act(() => useTeamRosterStore.getState().apply('h1', [roster([])]))
    expect(result.current.isUnread).toBe(true)
  })

  it('a split tab stays unread through its non-member pane', () => {
    useAgentStore.setState({ unread: { 'h1:a': true, 'h1:b': true } })
    const t = tab(split([tmux('p1', 'a'), tmux('p2', 'b')]))
    const { result } = renderHook(() => useTabDisplay(t))
    act(() => useTeamRosterStore.getState().apply('h1', [roster(['wa'])]))
    expect(result.current.isUnread).toBe(true)
    act(() => useTeamRosterStore.getState().apply('h1', [roster(['wa', 'wb'])]))
    expect(result.current.isUnread).toBe(false)
  })
})
