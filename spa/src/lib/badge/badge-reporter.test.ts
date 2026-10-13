// spa/src/lib/badge/badge-reporter.test.ts — the window side of the Dock badge (app-badge spec §3.2, §4.1): the report is
// sent when it changes (compared after sorting), debounced 300 ms, and only inside Electron.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { useAgentStore } from '../../stores/useAgentStore'
import { useApprovalStore } from '../../stores/useApprovalStore'
import { useHookCardStore } from '../../stores/useHookCardStore'
import { useSessionStore } from '../../stores/useSessionStore'
import { useTabStore } from '../../stores/useTabStore'
import { createTab } from '../../types/tab'
import type { Approval } from '../team/types'
import { BADGE_DEBOUNCE_MS, startBadgeReporter } from './badge-reporter'

const set = vi.fn()
let stop: () => void = () => {}

const tmuxTab = (code: string) => createTab({
  kind: 'tmux-session', hostId: 'h1', sessionCode: code, mode: 'terminal', cachedName: code, tmuxInstance: 'i1',
})
const lead = (id: string, tmux = ''): Approval => ({
  id, kind: 'lead', host_id: 'd1',
  origin: { session_id: `S-${id}`, ref: '_x', name: 'n', pid: 1, proc_start: 'p', cwd: '/w', tmux },
  payload: { reason: 'r', max_members: 3, roots: ['/w'] }, state: 'open', created_at: 1, deadline_at: 2, lease_until: 3,
})

function withElectron(badge: unknown) {
  Object.defineProperty(window, 'electronAPI', { value: badge === undefined ? {} : { badge }, writable: true, configurable: true })
}

beforeEach(() => {
  vi.useFakeTimers()
  set.mockReset()
  useAgentStore.setState({ unread: {} })
  useApprovalStore.getState().reset()
  useHookCardStore.getState().reset()
  useSessionStore.setState({ sessions: { h1: [{ code: 'aaaaaa', name: 'work', cwd: '/', mode: 'terminal' }] } })
  useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null })
  withElectron({ set })
})
afterEach(() => {
  stop()
  stop = () => {}
  vi.useRealTimers()
  Reflect.deleteProperty(window, 'electronAPI')
})

describe('startBadgeReporter', () => {
  it('sends the first report after the debounce, even when it is empty (main drops a reloaded window\'s report)', () => {
    stop = startBadgeReporter()
    expect(set).not.toHaveBeenCalled()
    vi.advanceTimersByTime(BADGE_DEBOUNCE_MS)
    expect(set).toHaveBeenCalledExactlyOnceWith({ sessions: [], cards: [] })
  })

  it('the debounce is 300 ms', () => {
    expect(BADGE_DEBOUNCE_MS).toBe(300)
  })

  it('a burst of changes sends once, the final report', () => {
    stop = startBadgeReporter()
    vi.advanceTimersByTime(BADGE_DEBOUNCE_MS)
    set.mockClear()
    const t = tmuxTab('aaaaaa')
    useTabStore.setState({ tabs: { [t.id]: t }, tabOrder: [t.id] })
    useAgentStore.setState({ unread: { 'h1:aaaaaa': true } })
    vi.advanceTimersByTime(BADGE_DEBOUNCE_MS - 1)
    useApprovalStore.getState().applyOpened('h1', lead('c1'))
    vi.advanceTimersByTime(BADGE_DEBOUNCE_MS - 1)
    expect(set).not.toHaveBeenCalled()
    vi.advanceTimersByTime(1)
    expect(set).toHaveBeenCalledExactlyOnceWith({
      sessions: ['h1:aaaaaa'],
      cards: [{ id: 'h1\u0000c1', fallback: 'c:h1:S-c1' }],
    })
  })

  it('a change that leaves the report equal sends nothing', () => {
    stop = startBadgeReporter()
    vi.advanceTimersByTime(BADGE_DEBOUNCE_MS)
    set.mockClear()
    useAgentStore.setState({ unread: { 'h1:nope': true } }) // no tab shows it
    vi.advanceTimersByTime(BADGE_DEBOUNCE_MS)
    expect(set).not.toHaveBeenCalled()
  })

  it('the same cards in another order are the same report (compared after sorting)', () => {
    stop = startBadgeReporter()
    useApprovalStore.getState().applyOpened('h1', lead('b'))
    useApprovalStore.getState().applyOpened('h1', lead('a'))
    vi.advanceTimersByTime(BADGE_DEBOUNCE_MS)
    expect(set).toHaveBeenCalledTimes(1)
    useApprovalStore.getState().reset()
    useApprovalStore.getState().applyOpened('h1', lead('a'))
    useApprovalStore.getState().applyOpened('h1', lead('b'))
    vi.advanceTimersByTime(BADGE_DEBOUNCE_MS)
    expect(set).toHaveBeenCalledTimes(1)
  })

  it('hook cards and a changed session list are reported', () => {
    stop = startBadgeReporter()
    vi.advanceTimersByTime(BADGE_DEBOUNCE_MS)
    set.mockClear()
    useHookCardStore.getState().applyOpened('h1', { ...lead('k1', 'work:@1.%1'), kind: 'hook_ask', payload: {} })
    vi.advanceTimersByTime(BADGE_DEBOUNCE_MS)
    expect(set).toHaveBeenLastCalledWith({
      sessions: ['h1:aaaaaa'], cards: [{ id: 'h1\u0000k1', session: 'h1:aaaaaa', fallback: 'c:h1:S-k1' }],
    })
    useSessionStore.setState({ sessions: { h1: [] } }) // the session is gone from the list: the card is unresolved
    vi.advanceTimersByTime(BADGE_DEBOUNCE_MS)
    expect(set).toHaveBeenLastCalledWith({ sessions: [], cards: [{ id: 'h1\u0000k1', fallback: 'c:h1:S-k1' }] })
  })

  it('a card closing sends the shorter report', () => {
    stop = startBadgeReporter()
    useApprovalStore.getState().applyOpened('h1', lead('c1'))
    vi.advanceTimersByTime(BADGE_DEBOUNCE_MS)
    useApprovalStore.getState().applyClosed('h1', { ...lead('c1'), state: 'approved' })
    vi.advanceTimersByTime(BADGE_DEBOUNCE_MS)
    expect(set).toHaveBeenLastCalledWith({ sessions: [], cards: [] })
  })

  it('stop cancels a pending send and ends the subscriptions', () => {
    stop = startBadgeReporter()
    stop()
    vi.advanceTimersByTime(BADGE_DEBOUNCE_MS)
    useAgentStore.setState({ unread: { 'h1:aaaaaa': true } })
    useApprovalStore.getState().applyOpened('h1', lead('c1'))
    vi.advanceTimersByTime(BADGE_DEBOUNCE_MS)
    expect(set).not.toHaveBeenCalled()
  })

  it('outside Electron it does nothing', () => {
    Reflect.deleteProperty(window, 'electronAPI')
    stop = startBadgeReporter()
    useApprovalStore.getState().applyOpened('h1', lead('c1'))
    expect(() => vi.advanceTimersByTime(BADGE_DEBOUNCE_MS)).not.toThrow()
  })

  it('an Electron build without the badge bridge (an older app on a newer SPA) does nothing', () => {
    withElectron(undefined)
    stop = startBadgeReporter()
    useApprovalStore.getState().applyOpened('h1', lead('c1'))
    expect(() => vi.advanceTimersByTime(BADGE_DEBOUNCE_MS)).not.toThrow()
    expect(vi.getTimerCount()).toBe(0)
  })
})
