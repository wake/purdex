// spa/src/components/UnattendedQuotaSection.test.tsx — the 「接力額度」 section of the unattended panel (plan RQ-A Task 6): the
// real component over the real quota / roster / host stores; only the writer is mocked.
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { fireEvent, render, screen, within } from '@testing-library/react'
import { act } from 'react'
import { UnattendedQuotaSection, type QuotaHostData } from './UnattendedQuotaSection'
import { useHostStore } from '../stores/useHostStore'
import { useI18nStore } from '../stores/useI18nStore'
import { useTeamRosterStore } from '../stores/useTeamRosterStore'
import { useRelayQuotaStore } from '../lib/team/relay-quota'
import { setQuota } from '../lib/team/relay-quota-writer'
import type { RosterSession, TeamRoster } from '../lib/team/roster'
import type { SessionQuota } from '../lib/team/types'

vi.mock('../lib/team/relay-quota-writer', () => ({ setQuota: vi.fn() }))
const mockedSet = vi.mocked(setQuota)

const A = 'host-a'
const B = 'host-b'

const q = (id: string, over: Partial<SessionQuota> = {}): SessionQuota => ({
  session_id: id, root_session_id: `root-${id}`, title: `T-${id}`, address: `mlab/${id}-xx`, is_lead: false,
  self_left: 0, member_pool_left: 0, rev: 1, ...over,
})
const sess = (id: string): RosterSession => ({ session_id: id, ref: `_${id}`, address: `mlab/${id}-xx`, live: true })
const team = (lead: string, members: string[]): TeamRoster => ({
  id: `t-${lead}`, host_id: 'd', created_at: 1, team_name: '', team_label: '', lead: sess(lead),
  members: members.map((m, i) => ({ ...sess(m), state: 'active', origin: 'spawned', joined_at: i + 1 })),
})

function show(hosts: QuotaHostData[], headings = false) {
  return render(<UnattendedQuotaSection hosts={hosts} headings={headings} />)
}
const rowsText = () => screen.getAllByTestId('quota-row').map((r) => r.textContent ?? '')
const rowFor = (id: string) => screen.getAllByTestId('quota-row').find((r) => r.getAttribute('data-session') === id)!

beforeEach(() => {
  useI18nStore.getState().setLocale('zh-TW')
  useHostStore.setState({
    hosts: { [A]: { id: A, name: 'mlab', ip: '1', port: 1, token: 't', order: 0 }, [B]: { id: B, name: 'air26', ip: '2', port: 2, token: 't', order: 1 } },
    hostOrder: [A, B], activeHostId: A, runtime: {},
  })
  useTeamRosterStore.getState().reset()
  useRelayQuotaStore.getState().reset()
  mockedSet.mockReset()
})

describe('which sessions get a row', () => {
  it('a member of any loaded roster has none (same host, and a lead on another host)', () => {
    useTeamRosterStore.getState().apply(A, [team('L1', ['M1'])])
    useTeamRosterStore.getState().apply(B, [team('L2', ['M2'])])
    show([
      { hostId: A, rows: [q('L1', { is_lead: true }), q('M1'), q('P1'), q('M2')] }, // M2 is a member of a lead on host B
      { hostId: B, rows: [q('L2', { is_lead: true }), q('M2')] },
    ])
    const ids = screen.getAllByTestId('quota-row').map((r) => r.getAttribute('data-session'))
    expect(ids.sort()).toEqual(['L1', 'L2', 'P1'])
  })

  it('while the own host\'s roster has not arrived: one muted line, no rows (unknown is not "not a member")', () => {
    useTeamRosterStore.getState().apply(B, [team('L2', [])])
    show([{ hostId: A, rows: [q('P1')] }, { hostId: B, rows: [q('P2')] }], true)
    expect(screen.getByTestId('quota-loading-team')).toHaveTextContent('讀取 team 狀態中…')
    expect(screen.getAllByTestId('quota-row').map((r) => r.getAttribute('data-session'))).toEqual(['P2'])
  })

  it('leads first, then the rest; each group by title, then address', () => {
    useTeamRosterStore.getState().apply(A, [])
    show([{ hostId: A, rows: [
      q('p2', { title: 'B plain' }), q('l2', { title: 'Z lead', is_lead: true }), q('p1', { title: 'A plain' }),
      q('l1', { title: 'A lead', is_lead: true }), q('p3', { title: 'A plain', address: 'mlab/zz' }),
    ] }])
    expect(screen.getAllByTestId('quota-row').map((r) => r.getAttribute('data-session'))).toEqual(['l1', 'l2', 'p1', 'p3', 'p2'])
  })

  it('the name comes from the title, else the name part of the address', () => {
    useTeamRosterStore.getState().apply(A, [])
    const { title: _t, ...noTitle } = q('x', { address: 'mlab/worker-7-ab' })
    show([{ hostId: A, rows: [noTitle as SessionQuota] }])
    expect(rowsText()[0]).toContain('worker-7-ab')
    expect(rowsText()[0]).not.toContain('mlab/')
  })
})

describe('the row', () => {
  beforeEach(() => useTeamRosterStore.getState().apply(A, []))

  it('a plain session has one stepper, a lead has two (auto relay, member pool)', () => {
    show([{ hostId: A, rows: [q('p1', { self_left: 3 }), q('l1', { is_lead: true, self_left: 4, member_pool_left: 2 })] }])
    expect(within(rowFor('p1')).getAllByTestId('quota-value')).toHaveLength(1)
    expect(within(rowFor('l1')).getAllByTestId('quota-value').map((v) => v.textContent)).toEqual(['4', '2'])
    expect(within(rowFor('l1')).getByText('自動接力')).toBeInTheDocument()
    expect(within(rowFor('l1')).getByText('member')).toBeInTheDocument()
  })

  it('the buttons write absolute values for the row\'s root and field', () => {
    show([{ hostId: A, rows: [q('l1', { is_lead: true, self_left: 4, member_pool_left: 2 })] }])
    const row = rowFor('l1')
    fireEvent.click(within(row).getAllByRole('button', { name: '增加' })[0])
    expect(mockedSet).toHaveBeenLastCalledWith({ hostId: A, sessionId: 'l1', root: 'root-l1', label: 'T-l1' }, 'self_left', 5)
    fireEvent.click(within(row).getAllByRole('button', { name: '減少' })[1])
    expect(mockedSet).toHaveBeenLastCalledWith({ hostId: A, sessionId: 'l1', root: 'root-l1', label: 'T-l1' }, 'member_pool_left', 1)
  })

  it('the limits disable the button that would pass them (0 and 99)', () => {
    show([{ hostId: A, rows: [q('p0', { self_left: 0 }), q('p99', { self_left: 99 })] }])
    expect(within(rowFor('p0')).getByRole('button', { name: '減少' })).toBeDisabled()
    expect(within(rowFor('p0')).getByRole('button', { name: '增加' })).toBeEnabled()
    expect(within(rowFor('p99')).getByRole('button', { name: '增加' })).toBeDisabled()
    expect(within(rowFor('p99')).getByRole('button', { name: '減少' })).toBeEnabled()
  })

  it('shows the desired value while a write is pending, the confirmed value otherwise', () => {
    show([{ hostId: A, rows: [q('p1', { self_left: 3 })] }])
    expect(within(rowFor('p1')).getByTestId('quota-value')).toHaveTextContent('3')
    act(() => { useRelayQuotaStore.getState().setWrite(A, 'root-p1', 'self_left', { desired: 8 }) })
    expect(within(rowFor('p1')).getByTestId('quota-value')).toHaveTextContent('8')
    act(() => {
      useRelayQuotaStore.getState().clearWrite(A, 'root-p1', 'self_left')
      useRelayQuotaStore.getState().applyEvent(A, { op: 'changed', root_session_id: 'root-p1', self_left: 6, member_pool_left: 0, rev: 5 })
    })
    expect(within(rowFor('p1')).getByTestId('quota-value')).toHaveTextContent('6')
  })

  it('two rows of one root show the same numbers, and a click on either writes the same key', () => {
    show([{ hostId: A, rows: [q('a', { root_session_id: 'chain', self_left: 2 }), q('b', { root_session_id: 'chain', self_left: 2 })] }])
    act(() => { useRelayQuotaStore.getState().setWrite(A, 'chain', 'self_left', { desired: 7 }) })
    expect(within(rowFor('a')).getByTestId('quota-value')).toHaveTextContent('7')
    expect(within(rowFor('b')).getByTestId('quota-value')).toHaveTextContent('7')
    fireEvent.click(within(rowFor('b')).getByRole('button', { name: '增加' }))
    expect(mockedSet).toHaveBeenLastCalledWith(expect.objectContaining({ root: 'chain', sessionId: 'b' }), 'self_left', 8)
  })
})

describe('what the section says instead of rows', () => {
  beforeEach(() => useTeamRosterStore.getState().apply(A, []))

  it('「讀不到額度」 for a host whose quotas could not be read (null or malformed)', () => {
    show([{ hostId: A, failed: true }])
    expect(screen.getByTestId('quota-unreadable')).toHaveTextContent('mlab：讀不到額度')
    expect(screen.queryByTestId('quota-row')).toBeNull()
  })

  it('「沒有可設定的 session」 for [] and when only members were there', () => {
    show([{ hostId: A, rows: [] }])
    expect(screen.getByTestId('quota-none')).toHaveTextContent('沒有可設定的 session')
  })

  it('members only: also none', () => {
    useTeamRosterStore.getState().apply(A, [team('L1', ['M1'])])
    show([{ hostId: A, rows: [q('M1')] }])
    expect(screen.getByTestId('quota-none')).toBeInTheDocument()
  })

  it('the title and the one-line explanation are there', () => {
    show([{ hostId: A, rows: [q('p1')] }])
    expect(screen.getByTestId('quota-title')).toHaveTextContent('接力額度')
    expect(screen.getByTestId('quota-explain')).toHaveTextContent('無人值守時，額度 ≥ 1 才會自動接力，每次扣 1；0 就等你核准。整條接力鏈共用。')
  })

  it('headings by host only when asked (more than one host has a section)', () => {
    useTeamRosterStore.getState().apply(B, [])
    show([{ hostId: A, rows: [q('p1')] }, { hostId: B, rows: [q('p2')] }], true)
    expect(screen.getAllByTestId('quota-host-heading').map((h) => h.textContent)).toEqual(['mlab', 'air26'])
  })

  it('no hosts to show: nothing at all', () => {
    const { container } = show([])
    expect(container).toBeEmptyDOMElement()
  })
})

// 88's ruling (2026-10-09): aligned columns - name | 自動接力 | member (blank for a non-lead) - so every row's 自動接力
// lines up, a lead is still one line, and a long name is cut with its address as the hint.
describe('layout: aligned columns', () => {
  beforeEach(() => useTeamRosterStore.getState().apply(A, []))

  it('every row has the same three cells: the name, the auto-relay stepper, then the member stepper or an empty cell', () => {
    show([{ hostId: A, rows: [q('l1', { is_lead: true }), q('p1')] }])
    for (const id of ['l1', 'p1']) expect(rowFor(id).children).toHaveLength(3)
    const lead = Array.from(rowFor('l1').children)
    expect(lead[1].getAttribute('data-field')).toBe('self_left')
    expect(lead[2].getAttribute('data-field')).toBe('member_pool_left')
    const plain = Array.from(rowFor('p1').children)
    expect(plain[1].getAttribute('data-field')).toBe('self_left')
    expect(plain[2].getAttribute('data-testid')).toBe('quota-pool-cell-empty')
  })

  it('the rows are one grid (so the columns line up across rows) and the rows themselves add no box', () => {
    show([{ hostId: A, rows: [q('l1', { is_lead: true }), q('p1')] }])
    const grid = rowFor('p1').parentElement!
    expect(grid.className).toContain('grid')
    expect(grid.className).toMatch(/grid-cols-\[minmax\(0,1fr\)_auto_auto\]/)
    expect(rowFor('p1').className).toContain('contents')
  })

  it('a long name is truncated with the address as its hint', () => {
    show([{ hostId: A, rows: [q('p1', { title: 'a very long session title that cannot fit in one line of the panel at all', address: 'mlab/p1-xx' })] }])
    const name = rowFor('p1').children[0] as HTMLElement
    expect(name.className).toContain('truncate')
    expect(name.className).toContain('min-w-0')
    expect(name).toHaveAttribute('title', 'mlab/p1-xx')
  })
})

