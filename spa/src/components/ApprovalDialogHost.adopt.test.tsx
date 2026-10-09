// spa/src/components/ApprovalDialogHost.adopt.test.tsx — the `adopt` card (adopt plan PL-2a, spec D-U24-2): the lead
// asks to take a session in; the card names the lead and the target, has NO grant inputs (not the lead card's name /
// label / member limit / roots), approves and denies in one click with no grant, minimizes into the pill like every
// kind, and a decision here goes back to the requester (the lead's tab). The real dialog host and store; only the
// daemon call and the navigation are mocked.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, act } from '@testing-library/react'
import { ApprovalDialogHost } from './ApprovalDialogHost'
import { useApprovalStore } from '../stores/useApprovalStore'
import { useHostStore } from '../stores/useHostStore'
import { useI18nStore } from '../stores/useI18nStore'
import { useUndoToast } from '../stores/useUndoToast'
import { decideApproval } from '../lib/team/approval-api'
import { gotoRequester } from '../lib/team/approval-goto'
import { __resetClientDescriptorForTests } from '../lib/team/client-label'
import type { Approval } from '../lib/team/types'

vi.mock('../lib/team/approval-api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../lib/team/approval-api')>()),
  decideApproval: vi.fn(),
}))
vi.mock('../lib/team/approval-goto', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../lib/team/approval-goto')>()),
  gotoRequester: vi.fn(() => 'none'),
}))
const mockedDecide = vi.mocked(decideApproval)
const mockedGoto = vi.mocked(gotoRequester)

const H = 'h1'
const adopt = (payload: Record<string, unknown> = {}): Approval => ({
  id: 'req-1', kind: 'adopt', host_id: 'd1',
  origin: { session_id: 'S-LEAD', ref: '_40iueq', name: 'purdex-7c', pid: 4242, proc_start: 'p', cwd: '/w/purdex', tmux: 'purdex:@1.%2' },
  payload: {
    team_id: '8f2c0f8e-3b1a', lead_session_id: 'S-LEAD', target_ref: '_tgt001', target_session_id: 'S-TGT', title: '寫文件的那個',
    target_name: 'doc-writer', target_address: 'mlab/doc-writer [tgt001]', target_cwd: '/w/docs', target_tmux: 'docs:@3.%7', ...payload,
  },
  state: 'open', created_at: 1_000, deadline_at: 1_000 + 125_000, lease_until: 31_000,
})
const open = (a: Approval) => act(() => { useApprovalStore.getState().applyOpened(H, a) })
const approveBtn = () => screen.getByTestId('approval-approve') as HTMLButtonElement
const click = async (id: string) => { await act(async () => { fireEvent.click(screen.getByTestId(id)) }) }

beforeEach(() => {
  useI18nStore.getState().setLocale('zh-TW')
  useApprovalStore.getState().reset()
  useHostStore.setState({
    hosts: { [H]: { id: H, name: 'mlab', ip: '1.2.3.4', port: 7860, order: 0 } },
    hostOrder: [H], runtime: { [H]: { status: 'connected' } }, activeHostId: H,
  })
  useUndoToast.setState({ toast: null, notice: null })
  mockedDecide.mockReset()
  mockedGoto.mockReset()
  mockedGoto.mockReturnValue('none')
  __resetClientDescriptorForTests()
  Object.defineProperty(window, 'electronAPI', { value: undefined, writable: true, configurable: true })
})
afterEach(() => { useHostStore.getState().reset() })

describe('ApprovalDialogHost — the adopt card', () => {
  it('names the lead and the target, and has none of the lead card’s inputs', () => {
    render(<ApprovalDialogHost />)
    open(adopt())
    const dialog = screen.getByTestId('approval-dialog')
    expect(dialog.dataset.kind).toBe('adopt')
    expect(screen.getByRole('heading').textContent).toBe('mlab：purdex-7c 想把 寫文件的那個 納入 team')
    expect(screen.getByTestId('approval-session').textContent).toBe('purdex-7c')
    expect(screen.getByTestId('approval-adopt-target').textContent).toBe('寫文件的那個')
    expect(screen.getByTestId('approval-adopt-address').textContent).toBe('mlab/doc-writer [tgt001]')
    expect(screen.getByTestId('approval-adopt-cwd').textContent).toBe('/w/docs')
    expect(screen.getByTestId('approval-adopt-tmux').textContent).toBe('docs:@3.%7')
    expect(screen.getByTestId('approval-adopt-team').textContent).toBe('8f2c0f8e-3b1a')
    expect(screen.getByTestId('approval-adopt-note').textContent).toContain('自我接力關閉')
    for (const id of ['approval-max-members', 'approval-roots', 'approval-team-name', 'approval-team-label', 'approval-no-more-asking', 'approval-reason', 'approval-usage']) {
      expect(screen.queryByTestId(id), id).toBeNull()
    }
    expect(approveBtn().disabled).toBe(false)
  })

  it('the target is called by its title, else its name, else its ref', () => {
    render(<ApprovalDialogHost />)
    open(adopt({ title: '', target_name: 'doc-writer' }))
    expect(screen.getByTestId('approval-adopt-target').textContent).toBe('doc-writer')
    act(() => { useApprovalStore.getState().reset() })
    open(adopt({ title: '', target_name: '' }))
    expect(screen.getByTestId('approval-adopt-target').textContent).toBe('_tgt001')
  })

  it('a payload with missing fields still renders (dashes, no throw)', () => {
    render(<ApprovalDialogHost />)
    open({ ...adopt(), payload: { team_id: 'T' } })
    expect(screen.getByTestId('approval-adopt-cwd').textContent).toBe('—')
    expect(screen.getByTestId('approval-adopt-tmux').textContent).toBe('—')
  })

  // Mutation gate: render the grant inputs / send a grant for adopt → red.
  it('approve and deny are one click each and send no grant', async () => {
    mockedDecide.mockResolvedValue({ ...adopt(), state: 'approved' })
    render(<ApprovalDialogHost />)
    open(adopt())
    await click('approval-approve')
    expect(mockedDecide).toHaveBeenCalledTimes(1)
    expect(mockedDecide.mock.calls[0][1]).toBe('req-1')
    const body = mockedDecide.mock.calls[0][2] as { decision: string; grant?: unknown }
    expect(body.decision).toBe('approve')
    expect(body.grant).toBeUndefined()

    mockedDecide.mockClear()
    mockedDecide.mockResolvedValue({ ...adopt(), state: 'denied' })
    open({ ...adopt(), id: 'req-2' })
    await click('approval-deny')
    expect((mockedDecide.mock.calls[0][2] as { decision: string }).decision).toBe('deny')
  })

  it('minimizing keeps the dialog mounted and the pill counts an adopt', () => {
    render(<ApprovalDialogHost />)
    open(adopt())
    fireEvent.click(screen.getByTestId('approval-minimize'))
    expect(screen.getByTestId('approval-dialog').hidden).toBe(true)
    expect(screen.getByTestId('approval-pill').textContent).toMatch(/^待核准 1/)
  })

  it('a decision made here goes back to the lead’s tab (the requester)', async () => {
    mockedDecide.mockResolvedValue({ ...adopt(), state: 'approved' })
    render(<ApprovalDialogHost />)
    open(adopt())
    await click('approval-approve')
    expect(mockedGoto).toHaveBeenCalledTimes(1)
    expect(mockedGoto.mock.calls[0][1].origin.session_id).toBe('S-LEAD')
  })
})

// codex attack: the title and name are written by the target session, so the card shows the daemon's ref and
// session id beside them, wraps and clips every string, and isolates direction.
// Mutation gate: drop the ref / session rows or the clip → red.
describe('ApprovalDialogHost — the adopt card cannot be spoofed or blown up by its payload', () => {
  it('a title that imitates the lead does not hide the target’s ref and session id', () => {
    render(<ApprovalDialogHost />)
    open(adopt({ title: 'purdex-7c', target_name: 'purdex-7c' }))
    expect(screen.getByTestId('approval-adopt-target').textContent).toBe('purdex-7c')
    expect(screen.getByTestId('approval-adopt-ref').textContent).toBe('_tgt001')
    expect(screen.getByTestId('approval-adopt-session').textContent).toBe('S-TGT')
  })

  it('every long string is clipped and may wrap; direction is isolated per field', () => {
    render(<ApprovalDialogHost />)
    const long = 'x'.repeat(5000)
    open(adopt({ title: long, target_address: long, target_cwd: long, target_tmux: long, target_ref: long, target_session_id: long, team_id: long }))
    for (const [id, max] of [['approval-adopt-target', 81], ['approval-adopt-address', 201], ['approval-adopt-cwd', 201], ['approval-adopt-tmux', 201], ['approval-adopt-ref', 41], ['approval-adopt-session', 65], ['approval-adopt-team', 65]] as const) {
      const el = screen.getByTestId(id)
      expect(el.textContent!.length, id).toBeLessThanOrEqual(max)
      expect(el.textContent!.endsWith('…'), id).toBe(true)
      expect(el.className, id).toContain('break-all')
    }
    for (const id of ['approval-adopt-target', 'approval-adopt-address', 'approval-adopt-cwd', 'approval-adopt-tmux']) {
      expect(screen.getByTestId(id).getAttribute('dir'), id).toBe('auto')
    }
    expect(screen.getByRole('heading').textContent!.length).toBeLessThan(200)
  })
})
