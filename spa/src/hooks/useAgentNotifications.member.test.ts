// spa/src/hooks/useAgentNotifications.member.test.ts — member spec R1 on the live wiring: the hook subscribes once, so
// "is this session a member" is read at each event. A session that becomes a member after mount is quiet, one that
// leaves the team after mount notifies again; a member's error still notifies.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { renderHook } from '@testing-library/react'
import { act } from 'react'
import { useAgentNotifications } from './useAgentNotifications'
import { __resetDebounceStateForTests } from '../lib/notification-gate'
import { STORAGE_KEYS } from '../lib/storage'
import { useAgentStore } from '../stores/useAgentStore'
import type { NormalizedEvent } from '../stores/useAgentStore'
import { useNotificationSettingsStore } from '../stores/useNotificationSettingsStore'
import { useSessionStore } from '../stores/useSessionStore'
import { useTabStore } from '../stores/useTabStore'
import { useTeamRosterStore } from '../stores/useTeamRosterStore'
import type { TeamRoster } from '../lib/team/roster'

const H = 'host-a'
const CODE = 'aaaaaa'
const CK = `${H}:${CODE}`

const roster = (members: string[]): TeamRoster => ({
  id: 't1', host_id: 'd', created_at: 1, team_name: '', team_label: '',
  lead: { session_id: 'L', ref: '_L', address: 'a/L', live: true, tmux_session: 'lead' },
  members: members.map((n) => ({
    session_id: n, ref: `_${n}`, address: `a/${n}`, live: true, tmux_session: n, state: 'active', origin: 'spawned', joined_at: 1,
  })),
})

let ts = 1
const ev = (over: Partial<NormalizedEvent>): NormalizedEvent => ({
  agent_type: 'cc', status: 'waiting', raw_event_name: 'PdxPermissionRequest', broadcast_ts: ++ts, detail: { tool_name: 'Bash' }, ...over,
})
const send = (event: NormalizedEvent) => act(() => useAgentStore.getState().handleNormalizedEvent(H, CODE, event))

describe('useAgentNotifications — a member is quiet (R1), read at the event', () => {
  let showNotification: ReturnType<typeof vi.fn>

  beforeEach(() => {
    ts = 1
    __resetDebounceStateForTests()
    localStorage.setItem(STORAGE_KEYS.NOTIFICATION_SEEN, JSON.stringify({ [CK]: 1 }))
    useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null, visitHistory: [] })
    useAgentStore.setState({ lastEvents: {}, statuses: { [CK]: 'running' }, unread: {}, subagents: {}, models: {}, agentTypes: {} })
    useNotificationSettingsStore.setState({ agents: {} })
    useNotificationSettingsStore.getState().setNotifyWithoutTab('cc', true)
    useSessionStore.setState({ sessions: { [H]: [{ code: CODE, name: 'worker', cwd: '/', mode: 'terminal' }] }, activeHostId: null, activeCode: null })
    useTeamRosterStore.getState().reset()
    showNotification = vi.fn()
    Object.defineProperty(window, 'electronAPI', { value: { showNotification }, writable: true, configurable: true })
  })
  afterEach(() => {
    Object.defineProperty(window, 'electronAPI', { value: undefined, writable: true, configurable: true })
  })

  it('a session that becomes a member after mount gets no waiting notification; leaving the team brings it back', () => {
    renderHook(() => useAgentNotifications())

    send(ev({}))
    expect(showNotification).toHaveBeenCalledTimes(1)

    act(() => useTeamRosterStore.getState().apply(H, [roster(['worker'])]))
    send(ev({}))
    send(ev({ status: 'idle', raw_event_name: 'PdxStop', detail: {} }))
    expect(showNotification).toHaveBeenCalledTimes(1)

    act(() => useTeamRosterStore.getState().apply(H, [roster([])]))
    send(ev({}))
    expect(showNotification).toHaveBeenCalledTimes(2)
  })

  it('a member\'s error still notifies', () => {
    renderHook(() => useAgentNotifications())
    act(() => useTeamRosterStore.getState().apply(H, [roster(['worker'])]))
    send(ev({ status: 'error', raw_event_name: 'PdxStopFailure', detail: { error: 'boom' } }))
    expect(showNotification).toHaveBeenCalledTimes(1)
  })

  it('R4: a seat whose session is not listed is not a member (waiting still notifies)', () => {
    renderHook(() => useAgentNotifications())
    act(() => useTeamRosterStore.getState().apply(H, [roster(['ghost'])]))
    send(ev({}))
    expect(showNotification).toHaveBeenCalledTimes(1)
  })
})
