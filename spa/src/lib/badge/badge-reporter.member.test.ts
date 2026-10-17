// spa/src/lib/badge/badge-reporter.member.test.ts — member spec R2 on the live wiring: with the reporter running, a roster
// frame that makes an unread tabbed session a member re-sends the Dock report without it, and one that removes it
// re-sends it with it. Pure report tests stay green when the reporter does not listen to the roster; this one does not.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { useAgentStore } from '../../stores/useAgentStore'
import { useApprovalStore } from '../../stores/useApprovalStore'
import { useHookCardStore } from '../../stores/useHookCardStore'
import { useSessionStore } from '../../stores/useSessionStore'
import { useTabStore } from '../../stores/useTabStore'
import { useTeamRosterStore } from '../../stores/useTeamRosterStore'
import { createTab } from '../../types/tab'
import type { TeamRoster } from '../team/roster'
import { BADGE_DEBOUNCE_MS, startBadgeReporter } from './badge-reporter'

const set = vi.fn()
let stop: () => void = () => {}

const roster = (members: string[]): TeamRoster => ({
  id: 't1', host_id: 'd', created_at: 1, team_name: '', team_label: '',
  lead: { session_id: 'L', ref: '_L', address: 'a/L', live: true, tmux_session: 'lead' },
  members: members.map((n) => ({
    session_id: n, ref: `_${n}`, address: `a/${n}`, live: true, tmux_session: n, state: 'active', origin: 'spawned', joined_at: 1,
  })),
})
const tab = createTab({ kind: 'tmux-session', hostId: 'h1', sessionCode: 'aaaaaa', mode: 'terminal', cachedName: 'work', tmuxInstance: 'i1' })

beforeEach(() => {
  vi.useFakeTimers()
  set.mockReset()
  useAgentStore.setState({ unread: { 'h1:aaaaaa': true } })
  useApprovalStore.getState().reset()
  useHookCardStore.getState().reset()
  useTeamRosterStore.getState().reset()
  useSessionStore.setState({ sessions: { h1: [{ code: 'aaaaaa', name: 'work', cwd: '/', mode: 'terminal' }] } })
  useTabStore.setState({ tabs: { [tab.id]: tab }, tabOrder: [tab.id], activeTabId: null })
  Object.defineProperty(window, 'electronAPI', { value: { badge: { set } }, writable: true, configurable: true })
})
afterEach(() => {
  stop()
  stop = () => {}
  vi.useRealTimers()
  Reflect.deleteProperty(window, 'electronAPI')
})

describe('startBadgeReporter — team members (R2)', () => {
  it('a roster change that makes the session a member re-sends without it; one that removes it re-sends with it', () => {
    stop = startBadgeReporter()
    vi.advanceTimersByTime(BADGE_DEBOUNCE_MS)
    expect(set).toHaveBeenLastCalledWith({ sessions: ['h1:aaaaaa'], cards: [] })

    useTeamRosterStore.getState().apply('h1', [roster(['work'])])
    vi.advanceTimersByTime(BADGE_DEBOUNCE_MS)
    expect(set).toHaveBeenCalledTimes(2)
    expect(set).toHaveBeenLastCalledWith({ sessions: [], cards: [] })

    useTeamRosterStore.getState().apply('h1', [roster([])])
    vi.advanceTimersByTime(BADGE_DEBOUNCE_MS)
    expect(set).toHaveBeenCalledTimes(3)
    expect(set).toHaveBeenLastCalledWith({ sessions: ['h1:aaaaaa'], cards: [] })
  })

  it('R4: before the host\'s session list arrives the seat maps to nothing, so the unread still counts', () => {
    useSessionStore.setState({ sessions: {} })
    stop = startBadgeReporter()
    useTeamRosterStore.getState().apply('h1', [roster(['work'])])
    vi.advanceTimersByTime(BADGE_DEBOUNCE_MS)
    // the seat cannot be mapped to a key without the session list, so it is not a member yet: the unread counts
    expect(set).toHaveBeenLastCalledWith({ sessions: ['h1:aaaaaa'], cards: [] })

    useSessionStore.setState({ sessions: { h1: [{ code: 'aaaaaa', name: 'work', cwd: '/', mode: 'terminal' }] } })
    vi.advanceTimersByTime(BADGE_DEBOUNCE_MS)
    expect(set).toHaveBeenLastCalledWith({ sessions: [], cards: [] })
  })
})
