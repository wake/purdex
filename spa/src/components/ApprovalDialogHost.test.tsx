// spa/src/components/ApprovalDialogHost.test.tsx — the one app-level approval dialog (lead-team spec §6.3, §6.5, §9.4):
// it renders the oldest open request from `useApprovalStore`, 核准 / 拒絕 are one click each (U5b), it never dismisses
// on Escape, it queues a click while the host is not connected, and it closes with the "handled by" toast on a close
// from elsewhere or a 409. Only the daemon call is mocked. The WS branch is P3b's; a `closed` from elsewhere is played
// here as the branch will play it: `applyClosed` → `'elsewhere'` → `toastClosed`.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, act } from '@testing-library/react'
import { ApprovalDialogHost } from './ApprovalDialogHost'
import { approvalKey, useApprovalStore } from '../stores/useApprovalStore'
import { useHostStore } from '../stores/useHostStore'
import { useI18nStore } from '../stores/useI18nStore'
import { useUndoToast } from '../stores/useUndoToast'
import { ApprovalApiError, decideApproval } from '../lib/team/approval-api'
import { toastClosed } from '../lib/team/approval-decide'
import { __resetClientDescriptorForTests } from '../lib/team/client-label'
import type { Approval } from '../lib/team/types'

vi.mock('../lib/team/approval-api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../lib/team/approval-api')>()),
  decideApproval: vi.fn(),
}))
const mockedDecide = vi.mocked(decideApproval)

const H = 'h1'
const approval = (over: Partial<Approval> = {}): Approval => ({
  id: 'req-1', kind: 'lead', host_id: 'd1',
  origin: { session_id: 'S1', ref: '_40iueq', name: 'purdex-7c', pid: 4242, proc_start: 'p', cwd: '/w/purdex', tmux: 'purdex:@1.%2' },
  payload: { reason: '要平行跑三個 PR', max_members: 3, roots: ['/w/purdex'] },
  state: 'open', created_at: 1_000, deadline_at: 1_000 + 125_000, lease_until: 31_000,
  ...over,
})
const air26 = { kind: 'app' as const, label: 'Purdex.app @ air26' }

const dialog = () => screen.queryByTestId('approval-dialog')
const open = (a: Approval, hostId = H) => act(() => { useApprovalStore.getState().applyOpened(hostId, a) })
const setStatus = (status: 'connected' | 'reconnecting' | 'disconnected') => act(() => { useHostStore.getState().setRuntime(H, { status }) })
/** What P3b's WS branch does on `{op:'closed'}`: drop it from the store, toast only when someone else decided. */
const closedFromWire = (a: Approval, hostId = H) => act(() => {
  if (useApprovalStore.getState().applyClosed(hostId, a) === 'elsewhere') toastClosed(hostId, a)
})

beforeEach(() => {
  useI18nStore.getState().setLocale('zh-TW')
  useApprovalStore.getState().reset()
  useHostStore.setState({
    hosts: {
      [H]: { id: H, name: 'mlab', ip: '1.2.3.4', port: 7860, order: 0 },
      h2: { id: 'h2', name: 'air26', ip: '1.2.3.5', port: 7860, order: 1 },
    },
    hostOrder: [H, 'h2'],
    runtime: { [H]: { status: 'connected' }, h2: { status: 'connected' } },
    activeHostId: H,
  })
  useUndoToast.setState({ toast: null, notice: null })
  mockedDecide.mockReset()
  __resetClientDescriptorForTests()
  Object.defineProperty(window, 'electronAPI', { value: undefined, writable: true, configurable: true })
})
afterEach(() => {
  vi.useRealTimers()
  useHostStore.getState().reset()
})

describe('ApprovalDialogHost', () => {
  it('renders nothing until a request is open', () => {
    render(<ApprovalDialogHost />)
    expect(dialog()).toBeNull()
  })

  it('shows host, session, address, cwd, tmux and reason; the countdown ticks toward deadline_at', () => {
    vi.useFakeTimers({ now: 1_000 })
    render(<ApprovalDialogHost />)
    open(approval())
    expect(dialog()).toBeInTheDocument()
    expect(screen.getByTestId('approval-host').textContent).toBe('mlab')
    expect(screen.getByTestId('approval-session').textContent).toBe('purdex-7c')
    expect(screen.getByTestId('approval-address').textContent).toBe('mlab/purdex-7c [40iueq]')
    expect(screen.getByTestId('approval-cwd').textContent).toBe('/w/purdex')
    expect(screen.getByTestId('approval-tmux').textContent).toBe('purdex:@1.%2')
    expect(screen.getByTestId('approval-reason').textContent).toBe('要平行跑三個 PR')
    expect(screen.getByTestId('approval-countdown').textContent).toBe('2:05')
    act(() => { vi.advanceTimersByTime(60_000) })
    expect(screen.getByTestId('approval-countdown').textContent).toBe('1:05')
    act(() => { vi.advanceTimersByTime(120_000) })
    expect(screen.getByTestId('approval-countdown').textContent).toBe('0:00')
    expect(screen.getByTestId('approval-approve')).not.toBeDisabled()
  })

  it('a session without a routable name shows the ref as address; a title (when the wire carries one) wins as the session label', () => {
    render(<ApprovalDialogHost />)
    open(approval({ origin: { ...approval().origin, name: '', title: '修 #1450 的側欄' } }))
    expect(screen.getByTestId('approval-session').textContent).toBe('修 #1450 的側欄')
    expect(screen.getByTestId('approval-address').textContent).toBe('mlab/_40iueq')
    expect(screen.getByTestId('approval-tmux').textContent).toBe('purdex:@1.%2')
  })

  it('the daemon\'s own address wins over the locally built one', () => {
    render(<ApprovalDialogHost />)
    open(approval({ origin: { ...approval().origin, address: 'mlab/purdex-7c' } }))
    expect(screen.getByTestId('approval-address').textContent).toBe('mlab/purdex-7c')
  })

  it('核准 is one click: POSTs approve with the payload\'s grant and the app client, then closes without a toast', async () => {
    mockedDecide.mockResolvedValueOnce(approval({ state: 'approved', decided_by: { kind: 'app', label: 'Purdex.app' }, grant: { max_members: 3, roots: ['/w/purdex'] } }))
    render(<ApprovalDialogHost />)
    open(approval())
    await act(async () => { fireEvent.click(screen.getByTestId('approval-approve')) })
    expect(mockedDecide).toHaveBeenCalledTimes(1)
    expect(mockedDecide.mock.calls[0]).toEqual([H, 'req-1', {
      decision: 'approve',
      grant: { max_members: 3, roots: ['/w/purdex'] },
      client: { kind: 'app', label: 'Purdex.app' },
    }])
    expect(dialog()).toBeNull()
    expect(useApprovalStore.getState().entries).toEqual({})
    expect(useUndoToast.getState().toast).toBeNull()
  })

  it('the grant is editable: max members and roots (one per line) go out as typed', async () => {
    mockedDecide.mockResolvedValueOnce(approval({ state: 'approved' }))
    render(<ApprovalDialogHost />)
    open(approval())
    fireEvent.change(screen.getByTestId('approval-max-members'), { target: { value: '5' } })
    fireEvent.change(screen.getByTestId('approval-roots'), { target: { value: '/w/purdex\n\n  /w/ploom  \n' } })
    await act(async () => { fireEvent.click(screen.getByTestId('approval-approve')) })
    expect(mockedDecide.mock.calls[0][2]).toMatchObject({ decision: 'approve', grant: { max_members: 5, roots: ['/w/purdex', '/w/ploom'] } })
  })

  it('an invalid grant disables 核准 only (0, 9, 2.5, blank roots); 拒絕 stays live', () => {
    render(<ApprovalDialogHost />)
    open(approval())
    for (const bad of ['0', '9', '2.5', '']) {
      fireEvent.change(screen.getByTestId('approval-max-members'), { target: { value: bad } })
      expect(screen.getByTestId('approval-approve')).toBeDisabled()
      expect(screen.getByTestId('approval-max-members-error')).toBeInTheDocument()
      expect(screen.getByTestId('approval-deny')).not.toBeDisabled()
    }
    fireEvent.change(screen.getByTestId('approval-max-members'), { target: { value: '8' } })
    expect(screen.getByTestId('approval-approve')).not.toBeDisabled()
    fireEvent.change(screen.getByTestId('approval-roots'), { target: { value: ' \n' } })
    expect(screen.getByTestId('approval-approve')).toBeDisabled()
    expect(screen.getByTestId('approval-roots-error')).toBeInTheDocument()
    expect(mockedDecide).not.toHaveBeenCalled()
  })

  it('拒絕 is one click: POSTs deny with no grant, then closes', async () => {
    mockedDecide.mockResolvedValueOnce(approval({ state: 'denied' }))
    render(<ApprovalDialogHost />)
    open(approval())
    await act(async () => { fireEvent.click(screen.getByTestId('approval-deny')) })
    expect(mockedDecide.mock.calls[0][2]).toEqual({ decision: 'deny', client: { kind: 'app', label: 'Purdex.app' } })
    expect(dialog()).toBeNull()
  })

  it('a double click sends once', async () => {
    let resolve!: (a: Approval) => void
    mockedDecide.mockReturnValueOnce(new Promise<Approval>((r) => { resolve = r }))
    render(<ApprovalDialogHost />)
    open(approval())
    await act(async () => {
      fireEvent.click(screen.getByTestId('approval-deny'))
      fireEvent.click(screen.getByTestId('approval-deny'))
    })
    expect(mockedDecide).toHaveBeenCalledTimes(1)
    await act(async () => { resolve(approval({ state: 'denied' })) })
    expect(dialog()).toBeNull()
  })

  it('several requests queue, oldest first, across hosts; the next one shows after the first closes', async () => {
    mockedDecide.mockResolvedValueOnce(approval({ id: 'old', state: 'denied' }))
    render(<ApprovalDialogHost />)
    open(approval({ id: 'newer', created_at: 5_000 }))
    open(approval({ id: 'old', created_at: 2_000, origin: { ...approval().origin, name: 'nexen-c1' } }), 'h2')
    expect(screen.getByTestId('approval-session').textContent).toBe('nexen-c1')
    expect(screen.getByTestId('approval-host').textContent).toBe('air26')
    expect(screen.getByTestId('approval-more').textContent).toBe('還有 1 個申請排隊中')
    await act(async () => { fireEvent.click(screen.getByTestId('approval-deny')) })
    expect(mockedDecide.mock.calls[0].slice(0, 2)).toEqual(['h2', 'old'])
    expect(screen.getByTestId('approval-session').textContent).toBe('purdex-7c')
    expect(screen.queryByTestId('approval-more')).toBeNull()
  })

  it('Escape and a backdrop click do not dismiss it', () => {
    render(<ApprovalDialogHost />)
    open(approval())
    fireEvent.keyDown(document, { key: 'Escape' })
    expect(dialog()).toBeInTheDocument()
    fireEvent.click(screen.getByTestId('approval-dialog'))
    expect(dialog()).toBeInTheDocument()
    expect(mockedDecide).not.toHaveBeenCalled()
  })

  it('takes focus onto the panel when it opens; Tab stays inside', () => {
    render(<ApprovalDialogHost />)
    open(approval())
    expect(document.activeElement).toBe(screen.getByTestId('approval-panel'))
    fireEvent.keyDown(document, { key: 'Tab' })
    expect(document.activeElement).toBe(screen.getByTestId('approval-max-members'))
    // Wraps at either end instead of leaving the dialog.
    fireEvent.keyDown(document, { key: 'Tab', shiftKey: true })
    expect(document.activeElement).toBe(screen.getByTestId('approval-approve'))
    fireEvent.keyDown(document, { key: 'Tab' })
    expect(document.activeElement).toBe(screen.getByTestId('approval-max-members'))
  })

  describe('while the host is not connected (spec §9.4)', () => {
    it('shows `daemon 重啟中…`, dims the buttons, and a click is queued instead of sent', () => {
      render(<ApprovalDialogHost />)
      open(approval())
      setStatus('reconnecting')
      expect(screen.getByTestId('approval-disconnected').textContent).toBe('daemon 重啟中…')
      expect(screen.getByTestId('approval-deny').getAttribute('aria-disabled')).toBe('true')
      expect(screen.getByTestId('approval-approve').getAttribute('aria-disabled')).toBe('true')
      fireEvent.click(screen.getByTestId('approval-deny'))
      expect(mockedDecide).not.toHaveBeenCalled()
      expect(useApprovalStore.getState().queued[approvalKey(H, 'req-1')]).toMatchObject({ hostId: H, decision: 'deny', approval: { id: 'req-1' } })
      expect(dialog()).toBeInTheDocument()
      expect(screen.getByTestId('approval-queued').textContent).toBe('已記下「拒絕」，恢復連線後送出')
      expect(screen.getByTestId('approval-deny')).toBeDisabled()
      expect(screen.getByTestId('approval-approve')).toBeDisabled()
    })

    it('a queued approve carries the edited grant', () => {
      render(<ApprovalDialogHost />)
      open(approval())
      fireEvent.change(screen.getByTestId('approval-max-members'), { target: { value: '2' } })
      setStatus('disconnected')
      fireEvent.click(screen.getByTestId('approval-approve'))
      expect(useApprovalStore.getState().queued[approvalKey(H, 'req-1')]).toMatchObject({ decision: 'approve', grant: { max_members: 2, roots: ['/w/purdex'] } })
      expect(screen.getByTestId('approval-queued').textContent).toBe('已記下「核准」，恢復連線後送出')
    })

    it('the banner goes away when the host is connected again; the queued decision stays locked in', () => {
      render(<ApprovalDialogHost />)
      open(approval())
      setStatus('reconnecting')
      fireEvent.click(screen.getByTestId('approval-deny'))
      setStatus('connected')
      expect(screen.queryByTestId('approval-disconnected')).toBeNull()
      expect(screen.getByTestId('approval-queued')).toBeInTheDocument()
      expect(screen.getByTestId('approval-deny')).toBeDisabled()
    })

    it('a send that fails on the network (the socket dropped mid-click) is queued the same way', async () => {
      mockedDecide.mockRejectedValueOnce(new ApprovalApiError(0, 'network', 'Failed to fetch'))
      render(<ApprovalDialogHost />)
      open(approval())
      await act(async () => { fireEvent.click(screen.getByTestId('approval-approve')) })
      expect(dialog()).toBeInTheDocument()
      expect(useApprovalStore.getState().queued[approvalKey(H, 'req-1')]).toMatchObject({ decision: 'approve' })
      expect(screen.getByTestId('approval-queued')).toBeInTheDocument()
      expect(useApprovalStore.getState().decidedHere).toEqual({})
    })

    it('a send that fails because the host was removed drops the request: nothing queued, no toast', async () => {
      mockedDecide.mockRejectedValueOnce(new ApprovalApiError(0, 'host_removed'))
      render(<ApprovalDialogHost />)
      open(approval())
      await act(async () => { fireEvent.click(screen.getByTestId('approval-deny')) })
      expect(dialog()).toBeNull()
      expect(useApprovalStore.getState().queued).toEqual({})
      expect(useApprovalStore.getState().decidedHere).toEqual({})
      expect(useUndoToast.getState().toast).toBeNull()
    })
  })

  describe('closed elsewhere (U6)', () => {
    it('a `closed` from another client closes it with the toast', () => {
      render(<ApprovalDialogHost />)
      open(approval())
      closedFromWire(approval({ state: 'approved', decided_by: air26, decided_at: 9_000 }))
      expect(dialog()).toBeNull()
      expect(useUndoToast.getState().toast?.message).toBe('mlab：purdex-7c 的 lead 申請 已由 Purdex.app @ air26 核准')
    })

    it('a `closed` that lands while our own decision is in flight is ours: the dialog closes, no toast', async () => {
      let resolve!: (a: Approval) => void
      mockedDecide.mockReturnValueOnce(new Promise<Approval>((r) => { resolve = r }))
      render(<ApprovalDialogHost />)
      open(approval())
      await act(async () => { fireEvent.click(screen.getByTestId('approval-approve')) })
      expect(mockedDecide).toHaveBeenCalledTimes(1)
      // The daemon's broadcast outruns our HTTP answer.
      closedFromWire(approval({ state: 'approved', decided_by: { kind: 'app', label: 'Purdex.app' }, decided_at: 9_000 }))
      expect(dialog()).toBeNull()
      expect(useUndoToast.getState().toast).toBeNull()
      await act(async () => { resolve(approval({ state: 'approved' })) })
      expect(useUndoToast.getState().toast).toBeNull()
      expect(useApprovalStore.getState().decidedHere).toEqual({})
    })

    it('a 409 already_decided closes it with the same toast', async () => {
      mockedDecide.mockRejectedValueOnce(new ApprovalApiError(409, 'already_decided', '', approval({ state: 'denied', decided_by: air26 })))
      render(<ApprovalDialogHost />)
      open(approval())
      await act(async () => { fireEvent.click(screen.getByTestId('approval-approve')) })
      expect(dialog()).toBeNull()
      expect(useApprovalStore.getState().entries).toEqual({})
      expect(useUndoToast.getState().toast?.message).toBe('mlab：purdex-7c 的 lead 申請 已由 Purdex.app @ air26 拒絕')
    })

    it('a 409 whose decided_by is this very app (the first answer was lost) closes it silently', async () => {
      mockedDecide.mockRejectedValueOnce(new ApprovalApiError(409, 'already_decided', '', approval({ state: 'approved', decided_by: { kind: 'app', label: 'Purdex.app' } })))
      render(<ApprovalDialogHost />)
      open(approval())
      await act(async () => { fireEvent.click(screen.getByTestId('approval-approve')) })
      expect(dialog()).toBeNull()
      expect(useUndoToast.getState().toast).toBeNull()
    })
  })

  it('a 404 closes it with the failure toast; another error toasts and re-enables the buttons', async () => {
    mockedDecide.mockRejectedValueOnce(new ApprovalApiError(400, 'bad_request', 'roots must be absolute'))
    render(<ApprovalDialogHost />)
    open(approval())
    await act(async () => { fireEvent.click(screen.getByTestId('approval-approve')) })
    expect(dialog()).toBeInTheDocument()
    expect(screen.getByTestId('approval-approve')).not.toBeDisabled()
    expect(useUndoToast.getState().toast?.message).toBe('送出決定失敗（bad_request）')
    expect(useApprovalStore.getState().decidedHere).toEqual({})
    mockedDecide.mockRejectedValueOnce(new ApprovalApiError(404, 'not_found'))
    await act(async () => { fireEvent.click(screen.getByTestId('approval-deny')) })
    expect(dialog()).toBeNull()
    expect(useUndoToast.getState().toast?.message).toBe('送出決定失敗（not_found）')
  })

  it('the client label is `Purdex.app @ <hostname>` from localDaemonStatus, read once', async () => {
    const localDaemonStatus = vi.fn(async () => ({ hostname: 'mlab' }))
    Object.defineProperty(window, 'electronAPI', { value: { localDaemonStatus }, writable: true, configurable: true })
    // The daemon answers with the same request's row, closed.
    mockedDecide.mockImplementation(async (_host, id) => approval({ id, state: 'denied' }))
    render(<ApprovalDialogHost />)
    open(approval({ id: 'a' }))
    await act(async () => { fireEvent.click(screen.getByTestId('approval-deny')) })
    open(approval({ id: 'b', created_at: 2_000 }))
    await act(async () => { fireEvent.click(screen.getByTestId('approval-deny')) })
    expect(mockedDecide.mock.calls[0][2]).toMatchObject({ client: { kind: 'app', label: 'Purdex.app @ mlab' } })
    expect(mockedDecide.mock.calls[1][2]).toMatchObject({ client: { kind: 'app', label: 'Purdex.app @ mlab' } })
    expect(localDaemonStatus).toHaveBeenCalledTimes(1)
  })
})
