// spa/src/components/ApprovalDialogHost.teamLabel.test.tsx — team label spec D-L4 / D-L10 (dialog half): the lead dialog
// shows an editable 「標籤（短名）」 under 「Team 名稱」 only when the payload has `team_label`, prefilled with the requested
// one; while it is empty the placeholder shows the label the daemon will derive from the (edited) name, or 「（無）」;
// it is checked by the label rule (weight ≤ 10, a Chinese character weighs 2) with a counter, and 核准 is disabled over
// it; `grant.team_label` goes out trimmed and only when the field was shown. The real dialog host and store; only the
// daemon call is mocked.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, act } from '@testing-library/react'
import { ApprovalDialogHost } from './ApprovalDialogHost'
import { useApprovalStore } from '../stores/useApprovalStore'
import { useHostStore } from '../stores/useHostStore'
import { useI18nStore } from '../stores/useI18nStore'
import { useUndoToast } from '../stores/useUndoToast'
import { decideApproval } from '../lib/team/approval-api'
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
  ...lead(), kind: 'self_relay', payload: { op_id: 'op-1', used_percentage: 72, window: 1_000_000, team_label: 'x' },
})
const open = (a: Approval) => act(() => { useApprovalStore.getState().applyOpened(H, a) })
const label = () => screen.getByTestId('approval-team-label') as HTMLInputElement
const queryLabel = () => screen.queryByTestId('approval-team-label')
const nameField = () => screen.getByTestId('approval-team-name') as HTMLInputElement
const errorEl = () => screen.queryByTestId('approval-team-label-error')
const widthEl = () => screen.getByTestId('approval-team-label-width')
const typeLabel = (value: string) => fireEvent.change(label(), { target: { value } })
const typeName = (value: string) => fireEvent.change(nameField(), { target: { value } })
const approve = async () => { await act(async () => { fireEvent.click(screen.getByTestId('approval-approve')) }) }
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

describe('ApprovalDialogHost team label (D-L10)', () => {
  it('a payload without team_label (a daemon that predates labels) shows no field and the grant has no team_label key', async () => {
    mockedDecide.mockResolvedValueOnce({ ...lead(), state: 'approved' })
    render(<ApprovalDialogHost />)
    open(lead({ team_name: 'build' }))
    expect(queryLabel()).toBeNull()
    await approve()
    expect(Object.hasOwn(sentGrant()!, 'team_label')).toBe(false)
  })

  it('a self relay never shows the field', () => {
    render(<ApprovalDialogHost />)
    open(selfRelay())
    expect(queryLabel()).toBeNull()
  })

  it('a payload with team_label shows the field, labelled, prefilled, under the name', () => {
    render(<ApprovalDialogHost />)
    open(lead({ team_name: '資源租約與派工回報', team_label: '資源線' }))
    expect(label().value).toBe('資源線')
    expect(screen.getByLabelText('標籤（短名）')).toBe(label())
    expect(nameField().compareDocumentPosition(label()) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
    expect(widthEl().textContent).toBe('6/10')
    expect(errorEl()).toBeNull()
  })

  it('the placeholder is the label the daemon will derive from the name, and follows the edited name', () => {
    render(<ApprovalDialogHost />)
    open(lead({ team_name: 'A 線：資源租約', team_label: '' }))
    expect(label().placeholder).toBe('A 線')
    typeName('介面線／右邊')
    expect(label().placeholder).toBe('介面線')
    typeName('資源租約與派工回報') // no separator and too wide: the daemon takes nothing
    expect(label().placeholder).toBe('（無）')
    typeName('lease-p1') // a hyphen inside a word is no separator
    expect(label().placeholder).toBe('lease-p1')
    typeName('resource-lease') // 14 wide: never cut at the hyphen
    expect(label().placeholder).toBe('（無）')
  })

  it('approving untouched sends the requested label; an empty request sends ""', async () => {
    mockedDecide.mockResolvedValueOnce({ ...lead(), state: 'approved' })
    render(<ApprovalDialogHost />)
    open(lead({ team_name: 'x', team_label: '資源線' }))
    await approve()
    expect(sentGrant()).toMatchObject({ team_name: 'x', team_label: '資源線' })
  })

  it('an empty requested label sends "" (the daemon derives)', async () => {
    mockedDecide.mockResolvedValueOnce({ ...lead(), state: 'approved' })
    render(<ApprovalDialogHost />)
    open(lead({ team_name: 'A 線：x', team_label: '' }))
    await approve()
    expect(sentGrant()).toMatchObject({ team_label: '' })
    expect(Object.hasOwn(sentGrant()!, 'team_label')).toBe(true)
  })

  it('an edited label goes out trimmed; clearing it sends ""', async () => {
    mockedDecide.mockResolvedValueOnce({ ...lead(), state: 'approved' })
    render(<ApprovalDialogHost />)
    open(lead({ team_name: 'x', team_label: '資源線' }))
    typeLabel('  A 線  ')
    await approve()
    expect(sentGrant()).toMatchObject({ team_label: 'A 線' })
  })

  it('clearing the label sends ""', async () => {
    mockedDecide.mockResolvedValueOnce({ ...lead(), state: 'approved' })
    render(<ApprovalDialogHost />)
    open(lead({ team_name: 'x', team_label: '資源線' }))
    typeLabel('')
    await approve()
    expect(Object.hasOwn(sentGrant()!, 'team_label')).toBe(true)
    expect(sentGrant()!.team_label).toBe('')
  })

  it('weight 10 passes and 11 does not (a Chinese character weighs 2, not 1)', () => {
    render(<ApprovalDialogHost />)
    open(lead({ team_name: '', team_label: '' }))
    typeLabel('資源租約派') // 5 characters, weight 10
    expect(errorEl()).toBeNull()
    expect(widthEl().textContent).toBe('10/10')
    expect(screen.getByTestId('approval-approve')).not.toBeDisabled()
    typeLabel('資源租約派工') // 6 characters, weight 12
    expect(widthEl().textContent).toBe('12/10')
    expect(errorEl()?.textContent).toBe('標籤最多 10 格（一個中文字算 2 格，約 5 個字）')
    expect(screen.getByTestId('approval-approve')).toBeDisabled()
    typeLabel('0123456789') // ten ASCII
    expect(errorEl()).toBeNull()
    typeLabel('01234567890') // eleven
    expect(screen.getByTestId('approval-approve')).toBeDisabled()
  })

  it('an invisible label and a control character are refused with their own message', () => {
    render(<ApprovalDialogHost />)
    open(lead({ team_name: '', team_label: '' }))
    typeLabel('️')
    expect(errorEl()?.textContent).toBe('標籤要有至少一個看得見的字元')
    expect(screen.getByTestId('approval-approve')).toBeDisabled()
    typeLabel('a\u0007b')
    expect(errorEl()?.textContent).toBe('標籤最多 64 bytes，且只能有可顯示字元')
    expect(screen.getByTestId('approval-approve')).toBeDisabled()
  })

  it('the counter is described to the field, with the error added when there is one', () => {
    render(<ApprovalDialogHost />)
    open(lead({ team_name: '', team_label: '' }))
    const counter = widthEl()
    expect(counter.id).not.toBe('')
    expect(label().getAttribute('aria-describedby')).toBe(counter.id)
    typeLabel('01234567890')
    const described = (label().getAttribute('aria-describedby') ?? '').split(' ')
    expect(described).toContain(counter.id)
    expect(described).toContain(errorEl()!.id)
  })

  it('the field alone: a payload with a label but no name still shows it', () => {
    render(<ApprovalDialogHost />)
    open(lead({ team_label: 'A' }))
    expect(label().value).toBe('A')
    expect(screen.queryByTestId('approval-team-name')).toBeNull()
  })
})
