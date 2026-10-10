// The unattended panel's numbers (c9d9c7-56): a disabled − says why on hover, and a number can be typed (click, Enter / Esc / leaving the box).
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { act, fireEvent, render, screen, within } from '@testing-library/react'
import { UnattendedQuotaSection } from './UnattendedQuotaSection'
import { useHostStore } from '../stores/useHostStore'
import { useI18nStore } from '../stores/useI18nStore'
import { useTeamRosterStore } from '../stores/useTeamRosterStore'
import { useRelayQuotaStore } from '../lib/team/relay-quota'
import { setQuota } from '../lib/team/relay-quota-writer'
import { setMaxMembers, useMaxMembersStore } from '../lib/team/max-members'
import { useUnattendedStore } from '../stores/useUnattendedStore'
import type { RosterSession, TeamRoster } from '../lib/team/roster'
import type { SessionQuota } from '../lib/team/types'

vi.mock('../lib/team/relay-quota-writer', () => ({ setQuota: vi.fn() }))
vi.mock('../lib/team/max-members', async (orig) => ({ ...(await orig<typeof import('../lib/team/max-members')>()), setMaxMembers: vi.fn() }))
const mockedQuota = vi.mocked(setQuota)
const mockedCap = vi.mocked(setMaxMembers)

const A = 'host-a'
const q = (id: string, over: Partial<SessionQuota> = {}): SessionQuota => ({
  session_id: id, root_session_id: `root-${id}`, title: `T-${id}`, address: `mlab/${id}-xx`, is_lead: false, self_left: 0, member_pool_left: 0, rev: 1, ...over,
})
const sess = (id: string): RosterSession => ({ session_id: id, ref: `_${id}`, address: `mlab/${id}-xx`, live: true })
const team = (over: Partial<TeamRoster> = {}): TeamRoster => ({ id: 't-l1', host_id: 'd', created_at: 1, team_name: '', team_label: '', lead: sess('l1'), members: [], max_members: 3, in_use: 3, ...over })
const show = (rows: SessionQuota[]) => render(<UnattendedQuotaSection hosts={[{ hostId: A, rows }]} headings={false} />)
const row = (id: string) => screen.getAllByTestId('quota-row').find((r) => r.getAttribute('data-session') === id)!
const typeInto = (input: HTMLElement, v: string) => fireEvent.change(input, { target: { value: v } })

beforeEach(() => {
  useI18nStore.getState().setLocale('zh-TW')
  useHostStore.setState({ hosts: { [A]: { id: A, name: 'mlab', ip: '1', port: 1, token: 't', order: 0 } }, hostOrder: [A], activeHostId: A, runtime: {} })
  useTeamRosterStore.getState().reset()
  useTeamRosterStore.getState().apply(A, []) // a loaded (empty) roster: rows draw only once the host's roster is known
  useRelayQuotaStore.getState().reset()
  mockedQuota.mockReset()
  mockedCap.mockReset()
  useUnattendedStore.getState().reset()
  useMaxMembersStore.getState().reset()
})

describe('why a − is off', () => {
  it('the cap: members in use hold it up, and the hover says so', () => {
    useUnattendedStore.getState().setMaxMembersSupport(A, 'yes')
    useTeamRosterStore.getState().apply(A, [team({ max_members: 3, in_use: 3 })])
    show([q('l1', { is_lead: true })])
    const cap = within(row('l1')).getByTestId('cap-stepper')
    const minus = within(cap).getByRole('button', { name: '調低上限' })
    expect(minus).toBeDisabled()
    expect(minus.parentElement).toHaveAttribute('title', '目前有 3 位 member，要先讓 member 離開才能調低')
  })

  it('the cap at its floor of 1 with nobody in use says the minimum; at 8 the + says the maximum', () => {
    useUnattendedStore.getState().setMaxMembersSupport(A, 'yes')
    useTeamRosterStore.getState().apply(A, [team({ max_members: 1, in_use: 0 })])
    const { unmount } = show([q('l1', { is_lead: true })])
    expect(within(row('l1')).getByRole('button', { name: '調低上限' }).parentElement).toHaveAttribute('title', '上限最少是 1')
    unmount()
    useTeamRosterStore.getState().apply(A, [team({ max_members: 8, in_use: 2 })])
    show([q('l1', { is_lead: true })])
    expect(within(row('l1')).getByRole('button', { name: '調高上限' }).parentElement).toHaveAttribute('title', '上限最多是 8')
  })

  it('a quota at 0: − says 已經是 0; a − that works has no reason', () => {
    show([q('p1', { self_left: 0 }), q('p2', { self_left: 4 })])
    const minus1 = within(row('p1')).getByRole('button', { name: '減少' })
    expect(minus1).toBeDisabled()
    expect(minus1.parentElement).toHaveAttribute('title', '已經是 0')
    const minus2 = within(row('p2')).getByRole('button', { name: '減少' })
    expect(minus2).toBeEnabled()
    expect(minus2.parentElement).not.toHaveAttribute('title')
  })
})

describe('typing a number', () => {
  const open = (id: string, testId: string) => {
    fireEvent.click(within(row(id)).getAllByTestId(testId)[0])
    return within(row(id)).getAllByTestId(`${testId}-input`)[0]
  }

  it('a click opens the box with the number; Enter sends the typed value as an absolute number', () => {
    show([q('p1', { self_left: 2 })])
    const input = open('p1', 'quota-value') as HTMLInputElement
    expect(input.value).toBe('2')
    typeInto(input, '7')
    fireEvent.keyDown(input, { key: 'Enter' })
    expect(mockedQuota).toHaveBeenCalledTimes(1)
    expect(mockedQuota.mock.calls[0][1]).toBe('self_left')
    expect(mockedQuota.mock.calls[0][2]).toBe(7)
    expect(within(row('p1')).queryByTestId('quota-value-input')).toBeNull()
  })

  it('Esc cancels without sending, and leaving the box confirms', () => {
    show([q('p1', { self_left: 2 })])
    const input = open('p1', 'quota-value')
    typeInto(input, '9')
    fireEvent.keyDown(input, { key: 'Escape' })
    expect(mockedQuota).not.toHaveBeenCalled()
    expect(within(row('p1')).queryByTestId('quota-value-input')).toBeNull()
    const again = open('p1', 'quota-value')
    typeInto(again, '5')
    fireEvent.blur(again)
    expect(mockedQuota).toHaveBeenCalledTimes(1)
    expect(mockedQuota.mock.calls[0][2]).toBe(5)
  })

  it('full-width digits are accepted as the digits they are', () => {
    show([q('p1', { self_left: 2 })])
    const input = open('p1', 'quota-value')
    typeInto(input, '１２')
    fireEvent.keyDown(input, { key: 'Enter' })
    expect(mockedQuota.mock.calls[0][2]).toBe(12)
  })

  it('Enter while an IME is composing does not confirm', () => {
    show([q('p1', { self_left: 2 })])
    const input = open('p1', 'quota-value')
    typeInto(input, '4')
    fireEvent.keyDown(input, { key: 'Enter', isComposing: true })
    fireEvent.keyDown(input, { key: 'Escape', isComposing: true })
    expect(mockedQuota).not.toHaveBeenCalled()
    expect(within(row('p1')).getByTestId('quota-value-input')).toBeInTheDocument()
  })

  it('the same number sends nothing', () => {
    show([q('p1', { self_left: 2 })])
    const input = open('p1', 'quota-value')
    fireEvent.keyDown(input, { key: 'Enter' })
    expect(mockedQuota).not.toHaveBeenCalled()
  })

  it('not a whole number, or above 99: the reason beside the box, nothing sent, the box stays', () => {
    show([q('p1', { self_left: 2 })])
    const input = open('p1', 'quota-value')
    typeInto(input, 'abc')
    fireEvent.keyDown(input, { key: 'Enter' })
    expect(within(row('p1')).getByTestId('quota-value-error')).toHaveTextContent('請輸入整數')
    typeInto(input, '120')
    fireEvent.keyDown(input, { key: 'Enter' })
    expect(within(row('p1')).getByTestId('quota-value-error')).toHaveTextContent('可輸入 0–99')
    expect(mockedQuota).not.toHaveBeenCalled()
    expect(within(row('p1')).getByTestId('quota-value-input')).toBeInTheDocument()
    typeInto(input, '12')
    expect(within(row('p1')).queryByTestId('quota-value-error')).toBeNull()
  })

  it('the cap: below the members in use says so and sends nothing; above 8 says the range; a good number goes', () => {
    useUnattendedStore.getState().setMaxMembersSupport(A, 'yes')
    useTeamRosterStore.getState().apply(A, [team({ max_members: 5, in_use: 3 })])
    show([q('l1', { is_lead: true })])
    const input = open('l1', 'cap-value')
    typeInto(input, '2')
    fireEvent.keyDown(input, { key: 'Enter' })
    expect(within(row('l1')).getByTestId('cap-value-error')).toHaveTextContent('目前有 3 個 member，上限不能低於 3')
    typeInto(input, '9')
    fireEvent.keyDown(input, { key: 'Enter' })
    expect(within(row('l1')).getByTestId('cap-value-error')).toHaveTextContent('可輸入 1–8')
    expect(mockedCap).not.toHaveBeenCalled()
    typeInto(input, '4')
    fireEvent.keyDown(input, { key: 'Enter' })
    expect(mockedCap).toHaveBeenCalledTimes(1)
    expect(mockedCap.mock.calls[0][1]).toBe(4)
  })

  it('a box left open when a cap request goes out sends nothing when it is confirmed', () => {
    useUnattendedStore.getState().setMaxMembersSupport(A, 'yes')
    useTeamRosterStore.getState().apply(A, [team({ max_members: 5, in_use: 1 })])
    show([q('l1', { is_lead: true })])
    const input = open('l1', 'cap-value')
    typeInto(input, '4')
    act(() => { useMaxMembersStore.getState().begin('host-a\u0000t-l1', { token: 1, identity: 'x' }) })
    fireEvent.keyDown(input, { key: 'Enter' })
    expect(mockedCap).not.toHaveBeenCalled()
    expect(within(row('l1')).queryByTestId('cap-value-input')).toBeNull()
  })

  it('a disabled − also carries its reason on the button itself (a keyboard or screen-reader user reaches it)', () => {
    show([q('p1', { self_left: 0 })])
    expect(within(row('p1')).getByRole('button', { name: '減少' })).toHaveAttribute('title', '已經是 0')
  })

  it('the number cannot be opened while a cap request is out', () => {
    useUnattendedStore.getState().setMaxMembersSupport(A, 'yes')
    useTeamRosterStore.getState().apply(A, [team({ max_members: 5, in_use: 1 })])
    useMaxMembersStore.getState().begin('host-a\u0000t-l1', { token: 1, identity: 'x' })
    show([q('l1', { is_lead: true })])
    expect(within(row('l1')).getByTestId('cap-value')).toBeDisabled()
  })
})
