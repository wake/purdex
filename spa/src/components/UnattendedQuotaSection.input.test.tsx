// The unattended panel's numbers (c9d9c7-56): a disabled − says why on hover, and a number can be typed (click, Enter / Esc / leaving the box).
// The cap's cases moved with the cap to the team settings (team/TeamSettingsPanel.cap.test.tsx, journal v5 §3.9).
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { fireEvent, render, screen, within } from '@testing-library/react'
import { UnattendedQuotaSection } from './UnattendedQuotaSection'
import { useHostStore } from '../stores/useHostStore'
import { useI18nStore } from '../stores/useI18nStore'
import { useTeamRosterStore } from '../stores/useTeamRosterStore'
import { useRelayQuotaStore } from '../lib/team/relay-quota'
import { setQuota } from '../lib/team/relay-quota-writer'
import { useUnattendedStore } from '../stores/useUnattendedStore'
import type { SessionQuota } from '../lib/team/types'

vi.mock('../lib/team/relay-quota-writer', () => ({ setQuota: vi.fn() }))
const mockedQuota = vi.mocked(setQuota)

const A = 'host-a'
const q = (id: string, over: Partial<SessionQuota> = {}): SessionQuota => ({
  session_id: id, root_session_id: `root-${id}`, title: `T-${id}`, address: `mlab/${id}-xx`, is_lead: false, self_left: 0, member_pool_left: 0, rev: 1, ...over,
})
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
  useUnattendedStore.getState().reset()
})

describe('why a − is off', () => {
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

  it('a disabled − also carries its reason on the button itself (a keyboard or screen-reader user reaches it)', () => {
    show([q('p1', { self_left: 0 })])
    expect(within(row('p1')).getByRole('button', { name: '減少' })).toHaveAttribute('title', '已經是 0')
  })
})
