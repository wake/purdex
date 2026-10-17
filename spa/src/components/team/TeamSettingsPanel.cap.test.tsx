// spa/src/components/team/TeamSettingsPanel.cap.test.tsx — 「成員上限」 in the team settings (journal v5 §3.9): the cap stepper
// moved here from the unattended panel (`PUT /api/team/max-members`) with its rules and texts unchanged — − stops at the
// members in use (and never below 1), + at 8, one request per team at a time, no optimistic number. These are the
// unattended panel's cap tests (UnattendedQuotaSection.test.tsx / .input.test.tsx), moved with it. Real stores; only the
// write is mocked.
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { act, fireEvent, render, screen, within } from '@testing-library/react'
import { TeamSettingsBody } from './TeamSettingsPanel'
import { useHostStore } from '../../stores/useHostStore'
import { useI18nStore } from '../../stores/useI18nStore'
import { useTeamRosterStore } from '../../stores/useTeamRosterStore'
import { useUnattendedStore } from '../../stores/useUnattendedStore'
import { useRelayQuotaStore } from '../../lib/team/relay-quota'
import { setMaxMembers, teamKey, useMaxMembersStore } from '../../lib/team/max-members'
import type { RosterSession, TeamRoster } from '../../lib/team/roster'

vi.mock('../../lib/team/max-members', async (orig) => ({ ...(await orig<typeof import('../../lib/team/max-members')>()), setMaxMembers: vi.fn() }))
const mockedCap = vi.mocked(setMaxMembers)

const A = 'host-a'
const B = 'host-b'
const sess = (id: string): RosterSession => ({ session_id: id, ref: `_${id}`, address: `mlab/${id}-xx`, live: true })
const capTeam = (lead: string, over: Partial<TeamRoster> = {}): TeamRoster => ({
  id: `t-${lead}`, host_id: 'd', created_at: 1, team_name: '', team_label: '', lead: sess(lead), members: [], max_members: 3, in_use: 1, ...over,
})
const show = (lead = 'l1', hostId = A) => render(<TeamSettingsBody hostId={hostId} teamId={`t-${lead}`} name={`Team ${lead}`} />)
const supported = (hostId = A) => useUnattendedStore.getState().setMaxMembersSupport(hostId, 'yes')
const cap = () => screen.getByTestId('cap-stepper')
const capIn = (el: HTMLElement) => within(el).getByTestId('cap-stepper')
const typeInto = (input: HTMLElement, v: string) => fireEvent.change(input, { target: { value: v } })
const openBox = () => { fireEvent.click(screen.getByTestId('cap-value')); return screen.getByTestId('cap-value-input') }

beforeEach(() => {
  useI18nStore.getState().setLocale('zh-TW')
  useHostStore.setState({
    hosts: { [A]: { id: A, name: 'mlab', ip: '1', port: 1, token: 't', order: 0 }, [B]: { id: B, name: 'air26', ip: '2', port: 2, token: 't', order: 1 } },
    hostOrder: [A, B], activeHostId: A, runtime: {},
  })
  useTeamRosterStore.getState().reset()
  useRelayQuotaStore.getState().reset()
  useUnattendedStore.getState().reset()
  useMaxMembersStore.getState().reset()
  mockedCap.mockReset()
})

describe('the cap section', () => {
  it('shows 上限 with the roster\'s number, and the members in use beside it', () => {
    supported()
    useTeamRosterStore.getState().apply(A, [capTeam('l1', { in_use: 2 })])
    show()
    const section = screen.getByTestId('team-settings-cap')
    expect(section.firstElementChild).toHaveTextContent('成員上限')
    expect(within(capIn(section)).getByText('上限')).toBeInTheDocument()
    expect(within(capIn(section)).getByTestId('cap-value')).toHaveTextContent('3')
    expect(within(section).getByTestId('team-settings-in-use')).toHaveTextContent('目前有 2 個 member')
  })

  it.each([
    ['the host does not list team.max_members.v1', () => useUnattendedStore.getState().setMaxMembersSupport(A, 'no')],
    ['the probe has not answered', () => {}],
    ['only the other host lists it', () => useUnattendedStore.getState().setMaxMembersSupport(B, 'yes')],
  ])('is not there when %s', (_n, setup) => {
    setup()
    useTeamRosterStore.getState().apply(A, [capTeam('l1')])
    show()
    expect(screen.queryByTestId('team-settings-cap')).toBeNull()
    expect(screen.queryByTestId('cap-stepper')).toBeNull()
  })

  it('is not there for a team without a cap (an older daemon), nor for a cap without its usage', () => {
    supported()
    useTeamRosterStore.getState().apply(A, [{ ...capTeam('l1'), max_members: undefined, in_use: undefined }])
    const { unmount } = show()
    expect(screen.queryByTestId('cap-stepper')).toBeNull()
    unmount()
    const t = capTeam('l1')
    delete t.in_use
    useTeamRosterStore.getState().apply(A, [t]) // − could not know where to stop
    show()
    expect(screen.queryByTestId('cap-stepper')).toBeNull()
  })

  it('each team\'s settings show its own cap (found by the team, not by position)', () => {
    supported()
    useTeamRosterStore.getState().apply(A, [capTeam('l2', { max_members: 6 }), capTeam('l1', { max_members: 2 })])
    const one = show('l1').container
    const two = show('l2').container
    expect(within(capIn(one)).getByTestId('cap-value')).toHaveTextContent('2')
    expect(within(capIn(two)).getByTestId('cap-value')).toHaveTextContent('6')
  })

  it('a click sends the absolute value, current ± 1, for that team, named by the team', () => {
    supported()
    useTeamRosterStore.getState().apply(A, [capTeam('l1', { max_members: 4, in_use: 1 })])
    show()
    fireEvent.click(within(cap()).getByRole('button', { name: '調高上限' }))
    expect(mockedCap).toHaveBeenLastCalledWith({ hostId: A, teamId: 't-l1', label: 'Team l1' }, 5)
    fireEvent.click(within(cap()).getByRole('button', { name: '調低上限' }))
    expect(mockedCap).toHaveBeenLastCalledWith({ hostId: A, teamId: 't-l1', label: 'Team l1' }, 3)
  })

  it.each([
    ['− stops at the members in use', { max_members: 3, in_use: 3 }, true, false],
    ['− is enabled above them', { max_members: 4, in_use: 3 }, false, false],
    ['− never goes below 1 even with nobody in use', { max_members: 1, in_use: 0 }, true, false],
    ['+ stops at 8', { max_members: 8, in_use: 2 }, false, true],
    ['+ is enabled at 7', { max_members: 7, in_use: 2 }, false, false],
  ])('%s', (_n, over, minusDisabled, plusDisabled) => {
    supported()
    useTeamRosterStore.getState().apply(A, [capTeam('l1', over)])
    show()
    expect(within(cap()).getByRole('button', { name: '調低上限' }).hasAttribute('disabled')).toBe(minusDisabled)
    expect(within(cap()).getByRole('button', { name: '調高上限' }).hasAttribute('disabled')).toBe(plusDisabled)
  })

  it('both buttons are disabled while that team\'s request is out, and free again after', () => {
    supported()
    useTeamRosterStore.getState().apply(A, [capTeam('l1', { max_members: 4, in_use: 1 }), capTeam('l2', { max_members: 4, in_use: 1 })])
    const one = show('l1').container
    const two = show('l2').container
    act(() => { useMaxMembersStore.getState().begin(teamKey(A, 't-l1'), { token: 1, identity: 'x' }) })
    expect(within(capIn(one)).getByRole('button', { name: '調高上限' })).toBeDisabled()
    expect(within(capIn(one)).getByRole('button', { name: '調低上限' })).toBeDisabled()
    expect(within(capIn(two)).getByRole('button', { name: '調高上限' })).toBeEnabled() // another team is not held
    act(() => { useMaxMembersStore.getState().end(teamKey(A, 't-l1'), 1) })
    expect(within(capIn(one)).getByRole('button', { name: '調高上限' })).toBeEnabled()
  })

  it('shows what the roster says and follows its events: no optimistic number', () => {
    supported()
    useTeamRosterStore.getState().apply(A, [capTeam('l1', { max_members: 3 })])
    show()
    fireEvent.click(within(cap()).getByRole('button', { name: '調高上限' }))
    expect(within(cap()).getByTestId('cap-value')).toHaveTextContent('3') // the click changed nothing on screen
    act(() => { useTeamRosterStore.getState().apply(A, [capTeam('l1', { max_members: 4 })]) })
    expect(within(cap()).getByTestId('cap-value')).toHaveTextContent('4')
  })

  it('names the members in use on hover', () => {
    supported()
    useTeamRosterStore.getState().apply(A, [capTeam('l1', { in_use: 2 })])
    show()
    expect(cap()).toHaveAttribute('title', '目前有 2 個 member')
  })
})

describe('why a − is off', () => {
  it('members in use hold it up, and the hover says so', () => {
    supported()
    useTeamRosterStore.getState().apply(A, [capTeam('l1', { max_members: 3, in_use: 3 })])
    show()
    const minus = within(cap()).getByRole('button', { name: '調低上限' })
    expect(minus).toBeDisabled()
    expect(minus.parentElement).toHaveAttribute('title', '目前有 3 位 member，要先讓 member 離開才能調低')
  })

  it('at its floor of 1 with nobody in use it says the minimum; at 8 the + says the maximum', () => {
    supported()
    useTeamRosterStore.getState().apply(A, [capTeam('l1', { max_members: 1, in_use: 0 })])
    const { unmount } = show()
    expect(within(cap()).getByRole('button', { name: '調低上限' }).parentElement).toHaveAttribute('title', '上限最少是 1')
    unmount()
    useTeamRosterStore.getState().apply(A, [capTeam('l1', { max_members: 8, in_use: 2 })])
    show()
    expect(within(cap()).getByRole('button', { name: '調高上限' }).parentElement).toHaveAttribute('title', '上限最多是 8')
  })
})

describe('typing a cap', () => {
  it('below the members in use says so and sends nothing; above 8 says the range; a good number goes', () => {
    supported()
    useTeamRosterStore.getState().apply(A, [capTeam('l1', { max_members: 5, in_use: 3 })])
    show()
    const input = openBox()
    typeInto(input, '2')
    fireEvent.keyDown(input, { key: 'Enter' })
    expect(screen.getByTestId('cap-value-error')).toHaveTextContent('目前有 3 個 member，上限不能低於 3')
    typeInto(input, '9')
    fireEvent.keyDown(input, { key: 'Enter' })
    expect(screen.getByTestId('cap-value-error')).toHaveTextContent('可輸入 1–8')
    expect(mockedCap).not.toHaveBeenCalled()
    typeInto(input, '4')
    fireEvent.keyDown(input, { key: 'Enter' })
    expect(mockedCap).toHaveBeenCalledTimes(1)
    expect(mockedCap.mock.calls[0][1]).toBe(4)
  })

  it('a box left open when a cap request goes out sends nothing when it is confirmed', () => {
    supported()
    useTeamRosterStore.getState().apply(A, [capTeam('l1', { max_members: 5, in_use: 1 })])
    show()
    const input = openBox()
    typeInto(input, '4')
    act(() => { useMaxMembersStore.getState().begin(teamKey(A, 't-l1'), { token: 1, identity: 'x' }) })
    fireEvent.keyDown(input, { key: 'Enter' })
    expect(mockedCap).not.toHaveBeenCalled()
    expect(screen.queryByTestId('cap-value-input')).toBeNull()
  })

  it('the number cannot be opened while a cap request is out', () => {
    supported()
    useTeamRosterStore.getState().apply(A, [capTeam('l1', { max_members: 5, in_use: 1 })])
    useMaxMembersStore.getState().begin(teamKey(A, 't-l1'), { token: 1, identity: 'x' })
    show()
    expect(screen.getByTestId('cap-value')).toBeDisabled()
  })
})
