// spa/src/components/ApprovalDialogHost.leadName.test.tsx — the 「名字」 card of the lead dialog (name pool v2 spec §5.3, §9.7, NA-7): shown only when the payload carries a
// candidate list; a fresh list is read when the dialog opens (the stored one is the fallback); 「保留」 is first and preselected; `grant.lead_name` goes out only for a
// picked name; a 409 `name_taken` refreshes the list from its body, drops a pick that is gone and says so in one line. The real dialog and store; only the daemon calls are mocked.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, act, within } from '@testing-library/react'
import { ApprovalDialogHost } from './ApprovalDialogHost'
import { useApprovalStore } from '../stores/useApprovalStore'
import { useHostStore } from '../stores/useHostStore'
import { useI18nStore } from '../stores/useI18nStore'
import { useUndoToast } from '../stores/useUndoToast'
import { ApprovalApiError, decideApproval, fetchLeadNames } from '../lib/team/approval-api'
import { handleApprovalEvent } from '../lib/team/approval-ws'
import { __resetClientDescriptorForTests } from '../lib/team/client-label'
import type { Approval, LeadNameCandidate } from '../lib/team/types'

vi.mock('../lib/team/approval-api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../lib/team/approval-api')>()),
  decideApproval: vi.fn(),
  fetchLeadNames: vi.fn(),
}))
const mockedDecide = vi.mocked(decideApproval)
const mockedNames = vi.mocked(fetchLeadNames)

const H = 'h1'
const cand = (code: string, kind: LeadNameCandidate['kind'], name = code[0].toUpperCase() + code.slice(1)): LeadNameCandidate => ({ code, name, kind, last_at: 0 })
const KEEP: LeadNameCandidate = { code: '', name: '', kind: 'keep', last_at: 0 }
const LIST = [KEEP, cand('freya', 'former'), cand('kenji', 'lead'), cand('yuki', 'other'), cand('emma', 'other')]
const NONE = Symbol('no list')
const lead = (names: unknown = LIST): Approval => ({
  id: 'req-1', kind: 'lead', host_id: 'd1',
  origin: { session_id: 'S1', ref: '_40iueq', name: 'purdex-7c', pid: 4242, proc_start: 'p', cwd: '/w/purdex', tmux: 'purdex:@1.%2' },
  payload: { reason: 'r', max_members: 3, roots: ['/w/purdex'], ...(names === NONE ? {} : { lead_names: names }) },
  state: 'open', created_at: 1_000, deadline_at: 1_000 + 125_000, lease_until: 31_000,
})
const open = (a: Approval) => act(async () => { useApprovalStore.getState().applyOpened(H, a) })
const card = () => screen.queryByTestId('lead-name-card')
const pills = () => screen.getAllByTestId('lead-name-pill')
const pill = (code: string) => pills().find((p) => p.getAttribute('data-code') === code)!
const approve = async () => { await act(async () => { fireEvent.click(screen.getByTestId('approval-approve')) }) }
const sentGrant = () => (mockedDecide.mock.calls[0][2] as { grant?: Record<string, unknown> }).grant
const taken = (names?: unknown) => new ApprovalApiError(409, 'name_taken', '', null, { error: 'name_taken', ...(names === undefined ? {} : { lead_names: names }) })

beforeEach(() => {
  useI18nStore.getState().setLocale('zh-TW')
  useApprovalStore.getState().reset()
  useHostStore.setState({
    hosts: { [H]: { id: H, name: 'mlab', ip: '1.2.3.4', port: 7860, order: 0 } },
    hostOrder: [H],
    runtime: { [H]: { status: 'connected' } },
    activeHostId: H,
  })
  useUndoToast.setState({ toast: null, notice: null })
  mockedDecide.mockReset()
  mockedNames.mockReset()
  mockedNames.mockRejectedValue(new ApprovalApiError(0, 'network')) // the fresh read fails unless a test says otherwise: the stored list stands
  __resetClientDescriptorForTests()
  Object.defineProperty(window, 'electronAPI', { value: undefined, writable: true, configurable: true })
})
afterEach(() => { useHostStore.getState().reset() })

describe('the lead name card', () => {
  it.each([
    ['no list (a daemon without lead names)', NONE],
    ['an empty list (no persona exists)', []],
  ])('is not there for %s, and the grant has no lead_name', async (_n, names) => {
    mockedDecide.mockResolvedValueOnce({ ...lead(), state: 'approved' })
    render(<ApprovalDialogHost />)
    await open(lead(names))
    expect(card()).toBeNull()
    if (names === NONE) expect(mockedNames).not.toHaveBeenCalled() // an empty stored list is still re-read: a persona may exist by now
    await approve()
    expect(Object.hasOwn(sentGrant()!, 'lead_name')).toBe(false)
  })

  it('shows 「保留目前的名字（X）」 first and preselected, then the groups; the rest sits behind 「更多…（N 位）」', async () => {
    render(<ApprovalDialogHost />)
    await open(lead())
    expect(pills()[0]).toHaveTextContent('保留目前的名字（purdex-7c）')
    expect(pills()[0]).toHaveAttribute('aria-checked', 'true')
    expect(within(screen.getByTestId('lead-name-former')).getByText('當過 lead')).toBeInTheDocument()
    expect(within(screen.getByTestId('lead-name-former')).getByText('Freya')).toBeInTheDocument()
    expect(within(screen.getByTestId('lead-name-lead')).getByText('預設 lead')).toBeInTheDocument()
    expect(within(screen.getByTestId('lead-name-lead')).getByText('Kenji')).toBeInTheDocument()
    expect(screen.queryByTestId('lead-name-other')).toBeNull()
    fireEvent.click(screen.getByTestId('lead-name-more'))
    expect(screen.getByTestId('lead-name-more')).toHaveTextContent('收起')
    expect(within(screen.getByTestId('lead-name-other')).getAllByTestId('lead-name-pill').map((p) => p.getAttribute('data-code'))).toEqual(['yuki', 'emma'])
    expect(screen.queryByTestId('lead-name-taken')).toBeNull()
  })

  it('the folded button counts the rest', async () => {
    render(<ApprovalDialogHost />)
    await open(lead())
    expect(screen.getByTestId('lead-name-more')).toHaveTextContent('更多…（2 位）')
  })

  it('reads a fresh list when it opens, and that list replaces the stored one', async () => {
    mockedNames.mockReset()
    mockedNames.mockResolvedValue([KEEP, cand('hiro', 'former')])
    render(<ApprovalDialogHost />)
    await open(lead())
    expect(mockedNames).toHaveBeenCalledWith(H, 'req-1')
    expect(screen.getByText('Hiro')).toBeInTheDocument()
    expect(screen.queryByText('Freya')).toBeNull()
  })

  it('keeps the stored list when the fresh read fails', async () => {
    render(<ApprovalDialogHost />)
    await open(lead())
    expect(mockedNames).toHaveBeenCalledTimes(1)
    expect(screen.getByText('Freya')).toBeInTheDocument()
  })

  it('a picked name goes out as grant.lead_name; picking 「保留」 again sends none', async () => {
    mockedDecide.mockResolvedValue({ ...lead(), state: 'approved' })
    render(<ApprovalDialogHost />)
    await open(lead())
    fireEvent.click(pill('freya'))
    expect(pill('freya')).toHaveAttribute('aria-checked', 'true')
    expect(pills()[0]).toHaveAttribute('aria-checked', 'false')
    fireEvent.click(pills()[0])
    await approve()
    expect(Object.hasOwn(sentGrant()!, 'lead_name')).toBe(false)
  })

  it('sends the picked code', async () => {
    mockedDecide.mockResolvedValueOnce({ ...lead(), state: 'approved' })
    render(<ApprovalDialogHost />)
    await open(lead())
    fireEvent.click(pill('kenji'))
    await approve()
    expect(sentGrant()).toMatchObject({ lead_name: 'kenji' })
  })
})

describe('a decision made offline', () => {
  it('whose name was taken meanwhile: the resend is refused, the list of the refusal replaces the one on the card, the pick is dropped and the dialog says so', async () => {
    render(<ApprovalDialogHost />)
    await open(lead())
    act(() => { useHostStore.setState((s) => ({ runtime: { ...s.runtime, [H]: { status: 'disconnected' } } })) })
    fireEvent.click(pill('freya'))
    await approve()
    expect(Object.keys(useApprovalStore.getState().queued)).toHaveLength(1)
    expect(mockedDecide).not.toHaveBeenCalled()
    mockedDecide.mockRejectedValueOnce(taken([KEEP, cand('kenji', 'lead')]))
    act(() => { useHostStore.setState((s) => ({ runtime: { ...s.runtime, [H]: { status: 'connected' } } })) })
    await act(async () => { handleApprovalEvent(H, { op: 'snapshot', approvals: [lead()] }) })
    expect(mockedDecide).toHaveBeenCalledTimes(1)
    expect((mockedDecide.mock.calls[0][2] as { grant?: Record<string, unknown> }).grant).toMatchObject({ lead_name: 'freya' })
    expect(useUndoToast.getState().toast).not.toBeNull()
    expect(screen.queryByText('Freya')).toBeNull()
    expect(pills()[0]).toHaveAttribute('aria-checked', 'true')
    expect(screen.getByTestId('lead-name-taken')).toBeInTheDocument()
    expect(Object.keys(useApprovalStore.getState().queued)).toHaveLength(0)
  })
})

describe('name_taken', () => {
  it('refreshes the list from the 409 body, drops the pick, says so in one line, and keeps the dialog open without a failure toast', async () => {
    mockedDecide.mockRejectedValueOnce(taken([KEEP, cand('kenji', 'lead')]))
    render(<ApprovalDialogHost />)
    await open(lead())
    fireEvent.click(pill('freya'))
    await approve()
    expect(screen.getByTestId('approval-dialog')).toBeInTheDocument()
    expect(screen.getByTestId('lead-name-taken')).toHaveTextContent('這個名字剛被別人用了')
    expect(screen.queryByText('Freya')).toBeNull()
    expect(pills()[0]).toHaveAttribute('aria-checked', 'true')
    expect(useUndoToast.getState().toast).toBeNull()
    expect(screen.getByTestId('approval-approve')).not.toBeDisabled()
  })

  it('keeps a pick the refreshed list still has, and the notice goes away once another name is picked', async () => {
    mockedDecide.mockRejectedValueOnce(taken([KEEP, cand('freya', 'former'), cand('kenji', 'lead')]))
    render(<ApprovalDialogHost />)
    await open(lead())
    fireEvent.click(pill('kenji'))
    await approve()
    expect(pill('kenji')).toHaveAttribute('aria-checked', 'true')
    expect(screen.getByTestId('lead-name-taken')).toBeInTheDocument()
    fireEvent.click(pill('freya'))
    expect(screen.queryByTestId('lead-name-taken')).toBeNull()
  })

  it('reads the list itself when the 409 body came without one', async () => {
    mockedDecide.mockRejectedValueOnce(taken())
    render(<ApprovalDialogHost />)
    await open(lead())
    mockedNames.mockReset()
    mockedNames.mockResolvedValue([KEEP, cand('hiro', 'former')])
    fireEvent.click(pill('freya'))
    await approve()
    expect(mockedNames).toHaveBeenCalledTimes(1)
    expect(screen.getByText('Hiro')).toBeInTheDocument()
    expect(screen.getByTestId('lead-name-taken')).toBeInTheDocument()
  })

  it('an open-time read that comes back after a name_taken list does not overwrite it', async () => {
    let late!: (v: LeadNameCandidate[]) => void
    mockedNames.mockReset()
    mockedNames.mockReturnValue(new Promise<LeadNameCandidate[]>((r) => { late = r }))
    mockedDecide.mockRejectedValueOnce(taken([KEEP, cand('kenji', 'lead')]))
    render(<ApprovalDialogHost />)
    await open(lead())
    fireEvent.click(pill('freya'))
    await approve()
    expect(screen.queryByText('Freya')).toBeNull()
    await act(async () => { late(LIST) }) // the stale list still has Freya
    expect(screen.queryByText('Freya')).toBeNull()
    expect(screen.getByText('Kenji')).toBeInTheDocument()
  })

  it('a pick sitting in the folded group keeps that group open after a refresh', async () => {
    mockedDecide.mockRejectedValueOnce(taken([KEEP, cand('freya', 'former'), cand('yuki', 'other'), cand('emma', 'other')]))
    render(<ApprovalDialogHost />)
    await open(lead())
    fireEvent.click(screen.getByTestId('lead-name-more'))
    fireEvent.click(pill('yuki'))
    await approve()
    expect(screen.getByTestId('lead-name-other')).toBeInTheDocument()
    expect(pill('yuki')).toHaveAttribute('aria-checked', 'true')
  })
})
