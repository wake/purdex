// spa/src/components/ApprovalDialogHost.teamName.test.tsx — team name spec D-N2 / D-N3 / D-N10 (dialog half): the lead
// dialog shows an editable 「Team 名稱」 only when the payload has `team_name` (a daemon that knows names), prefilled with
// the requested one; `grant.team_name` goes out trimmed and only when the field was shown; a name the daemon would refuse
// (> 64 bytes, anything outside Go's unicode.IsPrint) disables 核准. The real dialog host and store; only the daemon call
// is mocked.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, act } from '@testing-library/react'
import { ApprovalDialogHost } from './ApprovalDialogHost'
import { useApprovalStore } from '../stores/useApprovalStore'
import { useHostStore } from '../stores/useHostStore'
import { useI18nStore } from '../stores/useI18nStore'
import { useUndoToast } from '../stores/useUndoToast'
import { ApprovalApiError, decideApproval } from '../lib/team/approval-api'
import { __resetClientDescriptorForTests } from '../lib/team/client-label'
import type { Approval } from '../lib/team/types'

vi.mock('../lib/team/approval-api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../lib/team/approval-api')>()),
  decideApproval: vi.fn(),
}))
const mockedDecide = vi.mocked(decideApproval)

const H = 'h1'
const lead = (payload: Record<string, unknown> = {}): Approval => ({
  id: 'req-1', kind: 'lead', host_id: 'd1',
  origin: { session_id: 'S1', ref: '_40iueq', name: 'purdex-7c', pid: 4242, proc_start: 'p', cwd: '/w/purdex', tmux: 'purdex:@1.%2' },
  payload: { reason: 'r', max_members: 3, roots: ['/w/purdex'], ...payload },
  state: 'open', created_at: 1_000, deadline_at: 1_000 + 125_000, lease_until: 31_000,
})
const selfRelay = (): Approval => ({
  ...lead(), kind: 'self_relay', payload: { op_id: 'op-1', used_percentage: 72, window: 1_000_000, team_name: 'x' },
})
const open = (a: Approval) => act(() => { useApprovalStore.getState().applyOpened(H, a) })
const field = () => screen.getByTestId('approval-team-name') as HTMLInputElement
const queryField = () => screen.queryByTestId('approval-team-name')
const errorEl = () => screen.queryByTestId('approval-team-name-error')
const type = (value: string) => fireEvent.change(field(), { target: { value } })
const approve = async () => { await act(async () => { fireEvent.click(screen.getByTestId('approval-approve')) }) }
/** The grant of the one decide call. */
const sentGrant = () => (mockedDecide.mock.calls[0][2] as { grant?: Record<string, unknown> }).grant

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
  __resetClientDescriptorForTests()
  Object.defineProperty(window, 'electronAPI', { value: undefined, writable: true, configurable: true })
})
afterEach(() => { useHostStore.getState().reset() })

describe('ApprovalDialogHost team name (D-N10)', () => {
  it('a payload without team_name (a daemon that predates names) shows no field and the grant has no team_name key', async () => {
    mockedDecide.mockResolvedValueOnce({ ...lead(), state: 'approved' })
    render(<ApprovalDialogHost />)
    open(lead())
    expect(queryField()).toBeNull()
    await approve()
    expect(sentGrant()).toEqual({ max_members: 3, roots: ['/w/purdex'] })
    expect(Object.hasOwn(sentGrant()!, 'team_name')).toBe(false)
  })

  it('a payload with team_name shows the field, labelled and prefilled', () => {
    render(<ApprovalDialogHost />)
    open(lead({ team_name: '驗收 team' }))
    expect(field().value).toBe('驗收 team')
    expect(screen.getByLabelText('Team 名稱')).toBe(field())
    expect(field().placeholder).toBe('未命名')
    expect(errorEl()).toBeNull()
  })

  it('an empty requested name still shows the field (empty) and approving sends ""', async () => {
    mockedDecide.mockResolvedValueOnce({ ...lead(), state: 'approved' })
    render(<ApprovalDialogHost />)
    open(lead({ team_name: '' }))
    expect(field().value).toBe('')
    await approve()
    expect(sentGrant()).toEqual({ max_members: 3, roots: ['/w/purdex'], team_name: '' })
  })

  it('approving untouched sends the requested name', async () => {
    mockedDecide.mockResolvedValueOnce({ ...lead(), state: 'approved' })
    render(<ApprovalDialogHost />)
    open(lead({ team_name: 'build' }))
    await approve()
    expect(sentGrant()).toEqual({ max_members: 3, roots: ['/w/purdex'], team_name: 'build' })
  })

  it('an edited name goes out trimmed', async () => {
    mockedDecide.mockResolvedValueOnce({ ...lead(), state: 'approved' })
    render(<ApprovalDialogHost />)
    open(lead({ team_name: 'build' }))
    type('  重構 team  ')
    expect(errorEl()).toBeNull()
    await approve()
    expect(sentGrant()).toMatchObject({ team_name: '重構 team' })
  })

  it('clearing the field sends "" (the person removes the name; absent would keep it)', async () => {
    mockedDecide.mockResolvedValueOnce({ ...lead(), state: 'approved' })
    render(<ApprovalDialogHost />)
    open(lead({ team_name: 'build' }))
    type('')
    await approve()
    expect(sentGrant()).toMatchObject({ team_name: '' })
    expect(Object.hasOwn(sentGrant()!, 'team_name')).toBe(true)
  })

  it('a name of only spaces is trimmed to "" and sent as such', async () => {
    mockedDecide.mockResolvedValueOnce({ ...lead(), state: 'approved' })
    render(<ApprovalDialogHost />)
    open(lead({ team_name: 'build' }))
    type('   ')
    expect(errorEl()).toBeNull()
    await approve()
    expect(sentGrant()).toMatchObject({ team_name: '' })
  })

  it('64 bytes pass, 65 do not (bytes, not characters)', () => {
    render(<ApprovalDialogHost />)
    open(lead({ team_name: '' }))
    type('a'.repeat(64))
    expect(errorEl()).toBeNull()
    expect(screen.getByTestId('approval-approve')).not.toBeDisabled()
    type('a'.repeat(65))
    expect(errorEl()?.textContent).toBe('最多 64 bytes，且只能有可顯示字元')
    expect(screen.getByTestId('approval-approve')).toBeDisabled()
    // 21 CJK characters are 63 bytes, 22 are 66.
    type('字'.repeat(21))
    expect(errorEl()).toBeNull()
    type('字'.repeat(22))
    expect(errorEl()).not.toBeNull()
    expect(screen.getByTestId('approval-approve')).toBeDisabled()
  })

  it('the 64-byte limit applies to the trimmed name', () => {
    render(<ApprovalDialogHost />)
    open(lead({ team_name: '' }))
    type(`  ${'a'.repeat(64)}  `)
    expect(errorEl()).toBeNull()
    expect(screen.getByTestId('approval-approve')).not.toBeDisabled()
  })

  it.each([
    ['a control character (BEL)', 'a\u0007b'],
    ['a tab', 'a\tb'],
    ['a zero-width space (U+200B)', 'a​b'],
    ['a trailing zero-width space (the daemon does not trim it either)', 'ab​'],
    ['an ideographic space (U+3000) inside the name', '重構　team'],
    ['a no-break space (U+00A0) inside the name', 'a b'],
    ['a line separator (U+2028)', 'a b'],
  ])('%s disables 核准 and shows the error', (_label, name) => {
    render(<ApprovalDialogHost />)
    open(lead({ team_name: 'ok' }))
    type(name)
    expect(errorEl()).not.toBeNull()
    expect(field()).toHaveAttribute('aria-invalid', 'true')
    expect(field()).toHaveAccessibleDescription('最多 64 bytes，且只能有可顯示字元')
    expect(screen.getByTestId('approval-approve')).toBeDisabled()
    expect(screen.getByTestId('approval-deny')).not.toBeDisabled()
    type('ok')
    expect(errorEl()).toBeNull()
    expect(screen.getByTestId('approval-approve')).not.toBeDisabled()
  })

  it.each([
    ['CJK with an ASCII space', '重構 team'],
    ['punctuation and symbols', 'build/test: v1.2 (α) #3 +€ ©'],
    ['a combining mark', 'é'],
    ['an emoji', 'ship 🚀'],
  ])('%s is accepted', (_label, name) => {
    render(<ApprovalDialogHost />)
    open(lead({ team_name: '' }))
    type(name)
    expect(errorEl()).toBeNull()
    expect(screen.getByTestId('approval-approve')).not.toBeDisabled()
  })

  it('the label, placeholder and error read in English too', () => {
    useI18nStore.getState().setLocale('en')
    render(<ApprovalDialogHost />)
    open(lead({ team_name: '' }))
    expect(screen.getByLabelText('Team name')).toBe(field())
    expect(field().placeholder).toBe('Unnamed')
    type('a'.repeat(65))
    expect(errorEl()?.textContent).toBe('At most 64 bytes, and only printable characters')
  })

  it('an invalid name sends nothing', async () => {
    render(<ApprovalDialogHost />)
    open(lead({ team_name: '' }))
    type('a\u0007')
    await approve()
    expect(mockedDecide).not.toHaveBeenCalled()
  })

  it('a daemon 400 on decide takes the existing error path: toast, dialog stays, buttons live, the typed name kept', async () => {
    mockedDecide.mockRejectedValueOnce(new ApprovalApiError(400, 'bad_request', 'team_name: not printable'))
    render(<ApprovalDialogHost />)
    open(lead({ team_name: 'build' }))
    type('renamed')
    await approve()
    expect(screen.getByTestId('approval-dialog')).toBeInTheDocument()
    expect(useUndoToast.getState().toast?.message).toBe('送出決定失敗（bad_request: team_name: not printable）')
    expect(screen.getByTestId('approval-approve')).not.toBeDisabled()
    expect(field().value).toBe('renamed')
  })

  it('a self relay shows no name field even when its payload carries a team_name', () => {
    render(<ApprovalDialogHost />)
    open(selfRelay())
    expect(screen.getByTestId('approval-dialog').dataset.kind).toBe('self_relay')
    expect(queryField()).toBeNull()
  })

  it('a self relay approves with no grant at all', async () => {
    mockedDecide.mockResolvedValueOnce({ ...selfRelay(), state: 'approved' })
    render(<ApprovalDialogHost />)
    open(selfRelay())
    await approve()
    expect(sentGrant()).toBeUndefined()
  })

  it('a decision queued while the host is down carries the edited name', () => {
    render(<ApprovalDialogHost />)
    open(lead({ team_name: 'build' }))
    act(() => { useHostStore.getState().setRuntime(H, { status: 'reconnecting' }) })
    type('during outage')
    fireEvent.click(screen.getByTestId('approval-approve'))
    expect(Object.values(useApprovalStore.getState().queued)[0]).toMatchObject({ decision: 'approve', grant: { team_name: 'during outage' } })
  })
})
