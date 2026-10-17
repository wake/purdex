// spa/src/components/team/TeamPanel.memberLines.test.tsx — the 2nd and 3rd line of a member row (JV-2, journal-v5 spec §3.1 U3):
// the member's current task and state, and its last report to the leader, from the LEADER's journal host. Gated by
// `journal.members.v1`; without it both lines are absent. Real stores; only the HTTP read is faked.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { act, cleanup, render, screen, within } from '@testing-library/react'

const getJournalMembers = vi.fn()
vi.mock('../../lib/journal/api', () => ({ getJournalMembers: (...a: unknown[]) => getJournalMembers(...a) }))
vi.mock('./TeamSeatIcon', () => ({
  TeamSeatIcon: () => <span data-testid="seat-icon" />,
  TeamSeatHostBadge: () => <span data-testid="seat-host" />,
}))

import { TeamPanelArea } from './TeamPanelArea'
import { TeamDisplayProvider } from './TeamDisplayProvider'
import { MEMBER_REFRESH_DEBOUNCE_MS, ageText, memberLastLine, memberTaskLine } from './member-lines'
import { HOST, member as rosterMember, resetTeamStores, seedScene } from '../../lib/team/__tests__/team-fixture'
import { seedUnit, setJournalHost } from '../../lib/journal/__tests__/journal-fixture'
import { useTeamRosterStore } from '../../stores/useTeamRosterStore'
import type { JournalMember } from '../../lib/journal/types'
import { membersKey, useJournalMembersStore } from '../../stores/useJournalMembersStore'
import { useJournalStore } from '../../stores/useJournalStore'
import { useShownHostsStore } from '../../stores/useShownHostsStore'
import { useI18nStore } from '../../stores/useI18nStore'

const NOW = 1_800_000_000_000
const member = (over: Partial<JournalMember> = {}): JournalMember => ({
  ref: '', sessionId: 'A', title: 'A',
  task: { number: '12', title: 'wire the login', state: 'in_progress' },
  last: { at: NOW - 3 * 60_000, kind: 'done', text: 'checks passed' },
  ...over,
})
const hold = (members: JournalMember[]) => act(() => { useJournalMembersStore.setState({ byLead: { [membersKey(HOST, 'L')]: members } }) })
const support = (yes: boolean) => act(() => { useJournalMembersStore.getState().setSupport(HOST, yes) })
const row = (sessionId: string) => screen.getAllByTestId('team-panel-row').find((r) => r.getAttribute('data-session-id') === sessionId)!
const mount = () => render(<TeamDisplayProvider><TeamPanelArea /></TeamDisplayProvider>)

beforeEach(() => {
  vi.useFakeTimers({ toFake: ['Date', 'setTimeout', 'clearTimeout', 'setInterval', 'clearInterval'] })
  vi.setSystemTime(NOW)
  cleanup()
  localStorage.clear()
  resetTeamStores()
  useJournalMembersStore.getState().reset()
  useJournalStore.getState().reset()
  getJournalMembers.mockReset()
  getJournalMembers.mockResolvedValue({ kind: 'ok', value: [] })
  useShownHostsStore.setState({ ids: [HOST] })
  useI18nStore.getState().setLocale('zh-TW')
  seedScene({ members: [['A', 'a-tm'], ['B', 'b-tm']], tabs: [['lead', 'lead-tm'], ['ma', 'a-tm']], workspaces: [{ id: 'w1', tabs: ['lead', 'ma'] }], activeTabId: 'lead' })
})
afterEach(() => { cleanup(); vi.useRealTimers() })

describe('member lines in the full panel', () => {
  it('a supported host: line 2 is the current task and its state, line 3 the last report with its age', () => {
    support(true)
    hold([member()])
    mount()
    const a = within(row('A'))
    expect(a.getByTestId('team-panel-member-task').textContent).toBe('#12 wire the login・進行中')
    expect(a.getByTestId('team-panel-member-last').textContent).toBe('3 分前 完成：checks passed')
  })

  it('a pending task reads 待辦; a team task number shows its seq', () => {
    support(true)
    hold([member({ task: { number: '7bcf08-12', title: 'next', state: 'pending' } })])
    mount()
    expect(within(row('A')).getByTestId('team-panel-member-task').textContent).toBe('#12 next・待辦')
  })

  it('without the capability both lines are absent, even with members held', () => {
    hold([member()])
    mount()
    expect(screen.queryByTestId('team-panel-member-task')).toBeNull()
    expect(screen.queryByTestId('team-panel-member-last')).toBeNull()
    support(false)
    expect(screen.queryByTestId('team-panel-member-task')).toBeNull()
    expect(getJournalMembers).not.toHaveBeenCalled()
  })

  it('a missing part is left out; a member with neither is a one-line row; a member not in the feed has none', () => {
    support(true)
    hold([member({ last: null }), member({ sessionId: 'B', task: null, last: null })])
    mount()
    expect(within(row('A')).queryByTestId('team-panel-member-last')).toBeNull()
    expect(within(row('A')).getByTestId('team-panel-member-task')).toBeTruthy()
    expect(within(row('B')).queryByTestId('team-panel-member-task')).toBeNull()
    expect(within(row('B')).queryByTestId('team-panel-member-last')).toBeNull()
  })

  it('the lead row never has the lines (they are the members\')', () => {
    support(true)
    hold([member({ sessionId: 'L' })])
    mount()
    expect(within(row('L')).queryByTestId('team-panel-member-task')).toBeNull()
  })

  it('reads the LEADER\'s host once the panel shows, and again after the host says the journal changed', async () => {
    seedUnit(HOST, 'L', { kind: 'team', id: 'tm1', label: 'T' })
    support(true)
    mount()
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    expect(getJournalMembers).toHaveBeenCalledTimes(1)
    expect(getJournalMembers).toHaveBeenCalledWith(HOST, 'L')
    act(() => useJournalStore.getState().markStale(HOST, 'team:tm1', 'entries'))
    await act(async () => { await vi.advanceTimersByTimeAsync(MEMBER_REFRESH_DEBOUNCE_MS - 1) })
    expect(getJournalMembers).toHaveBeenCalledTimes(1)
    await act(async () => { await vi.advanceTimersByTimeAsync(1) })
    expect(getJournalMembers).toHaveBeenCalledTimes(2)
  })

  it('shows the lines the read brought in', async () => {
    support(true)
    getJournalMembers.mockResolvedValue({ kind: 'ok', value: [member()] })
    mount()
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    expect(within(row('A')).getByTestId('team-panel-member-task')).toBeTruthy()
  })
})

describe('the line texts', () => {
  const t = (key: string, params?: Record<string, string | number>) => useI18nStore.getState().t(key, params)
  it('age: just now, minutes, hours, days; a future time reads as just now', () => {
    expect(ageText(t, NOW - 20_000, NOW)).toBe('剛剛')
    expect(ageText(t, NOW - 59 * 60_000, NOW)).toBe('59 分前')
    expect(ageText(t, NOW - 60 * 60_000, NOW)).toBe('1 小時前')
    expect(ageText(t, NOW - 49 * 3_600_000, NOW)).toBe('2 天前')
    expect(ageText(t, NOW + 60_000, NOW)).toBe('剛剛')
  })
  it('an unknown report kind reads 回報', () => {
    expect(memberLastLine(t, { at: NOW - 60_000, kind: 'mystery', text: 'x' }, NOW)).toBe('1 分前 回報：x')
    expect(memberLastLine(t, { at: NOW - 60_000, kind: '', text: 'x' }, NOW)).toBe('1 分前 回報：x')
  })
  it('the task line in English', () => {
    useI18nStore.getState().setLocale('en')
    expect(memberTaskLine(t, { number: '3', title: 'T', state: 'pending' })).toBe('#3 T · to do')
  })
})

describe('JV-2d codex R5', () => {
  const settle = async (ms = 0) => { await act(async () => { await vi.advanceTimersByTimeAsync(ms) }) }
  const patchMembers = (f: (members: ReturnType<typeof rosterMember>[]) => void) => act(() => {
    const roster = structuredClone(useTeamRosterStore.getState().byHost[HOST])
    f(roster[0].members as ReturnType<typeof rosterMember>[])
    useTeamRosterStore.setState({ byHost: { [HOST]: roster } })
  })

  it('a member joining re-reads, and the new member shows its lines', async () => {
    support(true)
    getJournalMembers.mockResolvedValue({ kind: 'ok', value: [member()] })
    mount()
    await settle()
    expect(getJournalMembers).toHaveBeenCalledTimes(1)
    getJournalMembers.mockResolvedValue({ kind: 'ok', value: [member(), member({ sessionId: 'C' })] })
    patchMembers((ms) => { ms.push(rosterMember('C', 3, 'c-tm')) })
    await settle(MEMBER_REFRESH_DEBOUNCE_MS)
    expect(getJournalMembers).toHaveBeenCalledTimes(2)
    expect(within(row('C')).getByTestId('team-panel-member-task')).toBeTruthy()
  })

  it('a member handing off to a new session re-reads', async () => {
    support(true)
    getJournalMembers.mockResolvedValue({ kind: 'ok', value: [member()] })
    mount()
    await settle()
    expect(getJournalMembers).toHaveBeenCalledTimes(1)
    getJournalMembers.mockResolvedValue({ kind: 'ok', value: [member({ sessionId: 'A2' })] })
    patchMembers((ms) => { ms[0] = { ...rosterMember('A2', 1, 'a-tm') } })
    await settle(MEMBER_REFRESH_DEBOUNCE_MS)
    expect(getJournalMembers).toHaveBeenCalledTimes(2)
    expect(within(row('A2')).getByTestId('team-panel-member-task')).toBeTruthy()
  })

  it('the age moves on by itself: a minute later the text says one minute more', async () => {
    support(true)
    hold([member()])
    mount()
    expect(within(row('A')).getByTestId('team-panel-member-last').textContent).toBe('3 分前 完成：checks passed')
    await settle(60_000)
    expect(within(row('A')).getByTestId('team-panel-member-last').textContent).toBe('4 分前 完成：checks passed')
  })

  it('only this leader\'s journal.changed re-reads: another unit or another host asks nothing', async () => {
    const UNIT = { kind: 'team' as const, id: 'tm1', label: 'T' }
    setJournalHost(HOST, true)
    seedUnit(HOST, 'L', UNIT)
    seedUnit(HOST, 'other', { kind: 'solo', id: 'o1', label: 'o' })
    seedUnit('h2', 'L', UNIT)
    support(true)
    mount()
    await settle()
    expect(getJournalMembers).toHaveBeenCalledTimes(1)
    act(() => {
      useJournalStore.getState().markStale(HOST, 'solo:o1', 'entries') // another unit of this host
      useJournalStore.getState().markStale('h2', 'team:tm1', 'entries') // the same unit id on another host
    })
    await settle(MEMBER_REFRESH_DEBOUNCE_MS * 2)
    expect(getJournalMembers).toHaveBeenCalledTimes(1)
    act(() => useJournalStore.getState().markStale(HOST, 'team:tm1', 'entries'))
    await settle(MEMBER_REFRESH_DEBOUNCE_MS)
    expect(getJournalMembers).toHaveBeenCalledTimes(2)
  })
})
