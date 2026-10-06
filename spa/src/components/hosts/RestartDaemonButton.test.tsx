import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, act } from '@testing-library/react'
import { RestartDaemonButton } from './RestartDaemonButton'
import { useDaemonRestartStore } from '../../stores/useDaemonRestartStore'
import { useI18nStore } from '../../stores/useI18nStore'
import * as restartLib from '../../lib/daemon-restart'
import { useApprovalStore } from '../../stores/useApprovalStore'
import type { Approval } from '../../lib/team/types'
import * as approvalApi from '../../lib/team/approval-api'

vi.mock('../../lib/daemon-restart', async (orig) => ({ ...(await orig<typeof import('../../lib/daemon-restart')>()), countRunningWorkers: vi.fn() }))
vi.mock('../../lib/team/approval-api', () => ({ fetchInflight: vi.fn() }))

const restart = vi.fn(async () => {})
beforeEach(() => {
  useI18nStore.getState().setLocale('zh-TW')
  restart.mockClear()
  useDaemonRestartStore.setState({ restarting: {}, settled: {}, restart })
  vi.mocked(restartLib.countRunningWorkers).mockReset()
  // Default: the inflight call fails (an older daemon, or one mid-restart); tests that want a daemon answer override it.
  vi.mocked(approvalApi.fetchInflight).mockReset()
  vi.mocked(approvalApi.fetchInflight).mockRejectedValue(new Error('inflight unavailable'))
  useApprovalStore.getState().reset()
})

async function openConfirm(workers: number | null, onActiveChange?: (active: boolean) => void) {
  vi.mocked(restartLib.countRunningWorkers).mockResolvedValueOnce(workers)
  const view = render(<RestartDaemonButton hostId="h1" onActiveChange={onActiveChange} />)
  await act(async () => { fireEvent.click(screen.getByTestId('restart-daemon')) })
  await screen.findByTestId('restart-daemon-confirm-dialog')
  return view
}

/** The next count stays pending; the returned function resolves it inside act. */
function pendingCount() {
  let resolve!: (n: number | null) => void
  vi.mocked(restartLib.countRunningWorkers).mockReturnValueOnce(new Promise((r) => { resolve = r }))
  return (n: number | null) => act(async () => { resolve(n) })
}

const dialog = () => screen.queryByTestId('restart-daemon-confirm-dialog')

describe('RestartDaemonButton', () => {
  it('confirm names running workers when there are some', async () => {
    // Dropping the running-worker count turns this red (mutation deliverable).
    await openConfirm(2)
    expect(screen.getByTestId('restart-daemon-workers').textContent).toBe('2 個 worker 正在執行，重啟會中斷它們這一輪（之後可以繼續對話）')
    expect(screen.getByText('這台主機上的終端連線會中斷幾秒，之後自動接回；tmux session 不受影響。')).toBeTruthy()
  })

  it('no worker line with zero running workers', async () => {
    await openConfirm(0)
    expect(screen.queryByTestId('restart-daemon-workers')).toBeNull()
  })

  it('unknown worker count → the cautious line', async () => {
    await openConfirm(null)
    expect(screen.getByTestId('restart-daemon-workers').textContent).toContain('無法確認是否有 worker 正在執行')
  })

  it('confirm → store.restart(hostId, name), dialog closes', async () => {
    await openConfirm(0)
    await act(async () => { fireEvent.click(screen.getByTestId('restart-daemon-confirm-confirm')) })
    expect(restart).toHaveBeenCalledWith('h1', expect.any(String))
    expect(screen.queryByTestId('restart-daemon-confirm-dialog')).toBeNull()
  })

  it('cancel → no restart', async () => {
    await openConfirm(0)
    fireEvent.click(screen.getByTestId('restart-daemon-confirm-cancel'))
    expect(restart).not.toHaveBeenCalled()
  })

  it('custom className keeps the counting dimming classes', () => {
    render(<RestartDaemonButton hostId="h1" className="my-custom btn-x" />)
    const cls = screen.getByTestId('restart-daemon').className
    expect(cls).toContain('my-custom')
    expect(cls).toContain('btn-x')
    expect(cls).toContain('aria-disabled:opacity-50')
    expect(cls).toContain('aria-disabled:cursor-default')
  })

  it('custom className still gets the inline-flex layout', () => {
    render(<RestartDaemonButton hostId="h1" className="my-custom" />)
    expect(screen.getByTestId('restart-daemon').className).toContain('inline-flex')
  })

  it('disabled prop: real disabled attribute, click does not count workers', () => {
    render(<RestartDaemonButton hostId="h1" disabled />)
    const btn = screen.getByTestId('restart-daemon') as HTMLButtonElement
    expect(btn.disabled).toBe(true)
    fireEvent.click(btn)
    expect(restartLib.countRunningWorkers).not.toHaveBeenCalled()
  })

  it('while the host restarts: spinner text, disabled', () => {
    useDaemonRestartStore.setState({ restarting: { h1: true } })
    render(<RestartDaemonButton hostId="h1" />)
    const btn = screen.getByTestId('restart-daemon') as HTMLButtonElement
    expect(btn.disabled).toBe(true)
    expect(btn.textContent).toContain('重啟中…')
  })

  it('another host restarting does not disable this one', () => {
    useDaemonRestartStore.setState({ restarting: { h2: true } })
    render(<RestartDaemonButton hostId="h1" />)
    expect((screen.getByTestId('restart-daemon') as HTMLButtonElement).disabled).toBe(false)
  })

  it('custom label', () => {
    render(<RestartDaemonButton hostId="h1" label="立即重啟" testId="nex-restart-now" />)
    expect(screen.getByTestId('nex-restart-now').textContent).toBe('立即重啟')
  })

  it('while counting: keeps focus, aria-busy not disabled, Cancel returns focus to the button', async () => {
    let resolve!: (n: number) => void
    vi.mocked(restartLib.countRunningWorkers).mockReturnValueOnce(new Promise((r) => { resolve = r }))
    render(<RestartDaemonButton hostId="h1" />)
    const btn = screen.getByTestId('restart-daemon') as HTMLButtonElement
    btn.focus()
    fireEvent.click(btn)
    expect(document.activeElement).toBe(btn)
    expect(btn.disabled).toBe(false)
    expect(btn.getAttribute('aria-busy')).toBe('true')
    expect(btn.getAttribute('aria-disabled')).toBe('true')
    await act(async () => { resolve(0) })
    await screen.findByTestId('restart-daemon-confirm-dialog')
    fireEvent.click(screen.getByTestId('restart-daemon-confirm-cancel'))
    expect(screen.queryByTestId('restart-daemon-confirm-dialog')).toBeNull()
    expect(document.activeElement).toBe(btn)
    expect(btn.getAttribute('aria-busy')).toBeNull()
  })

  it('double click while counting counts once and opens one dialog', async () => {
    let resolve!: (n: number) => void
    vi.mocked(restartLib.countRunningWorkers).mockReturnValueOnce(new Promise((r) => { resolve = r }))
    render(<RestartDaemonButton hostId="h1" />)
    const btn = screen.getByTestId('restart-daemon')
    fireEvent.click(btn)
    fireEvent.click(btn)
    expect(restartLib.countRunningWorkers).toHaveBeenCalledTimes(1)
    await act(async () => { resolve(1) })
    expect(screen.getAllByTestId('restart-daemon-confirm-dialog')).toHaveLength(1)
  })

  it('restarting spinner icon is aria-hidden', () => {
    useDaemonRestartStore.setState({ restarting: { h1: true } })
    render(<RestartDaemonButton hostId="h1" />)
    expect(screen.getByTestId('restart-daemon').querySelector('svg')?.getAttribute('aria-hidden')).toBe('true')
  })
})

// PR #1579 review (R1-1, R1-2, A-1, A-2, critic C1): a count or an open dialog is valid only for the same host, while
// that host is not restarting and has not finished a restart since the click, and while the caller has not locked.
describe('RestartDaemonButton - a stale count or dialog is cancelled', () => {
  it('host changes mid-count: the old count opens no dialog; clicking again counts for the new host', async () => {
    const settle = pendingCount()
    const view = render(<RestartDaemonButton hostId="h1" />)
    fireEvent.click(screen.getByTestId('restart-daemon'))
    view.rerender(<RestartDaemonButton hostId="h2" />)
    await settle(2)
    expect(dialog()).toBeNull()
    vi.mocked(restartLib.countRunningWorkers).mockResolvedValueOnce(0)
    await act(async () => { fireEvent.click(screen.getByTestId('restart-daemon')) })
    expect(restartLib.countRunningWorkers).toHaveBeenCalledTimes(2)
    expect(restartLib.countRunningWorkers).toHaveBeenLastCalledWith('h2')
    await screen.findByTestId('restart-daemon-confirm-dialog')
    await act(async () => { fireEvent.click(screen.getByTestId('restart-daemon-confirm-confirm')) })
    expect(restart).toHaveBeenCalledTimes(1)
    expect(restart).toHaveBeenCalledWith('h2', expect.any(String))
  })

  it('host changes mid-count: the count is dropped at once, the new host can be counted before the old one settles', async () => {
    const settleOld = pendingCount()
    const view = render(<RestartDaemonButton hostId="h1" />)
    fireEvent.click(screen.getByTestId('restart-daemon'))
    expect(screen.getByTestId('restart-daemon').getAttribute('aria-busy')).toBe('true')
    view.rerender(<RestartDaemonButton hostId="h2" />)
    expect(screen.getByTestId('restart-daemon').getAttribute('aria-busy')).toBeNull()
    vi.mocked(restartLib.countRunningWorkers).mockResolvedValueOnce(0)
    await act(async () => { fireEvent.click(screen.getByTestId('restart-daemon')) })
    expect(restartLib.countRunningWorkers).toHaveBeenLastCalledWith('h2')
    await screen.findByTestId('restart-daemon-confirm-dialog')
    await settleOld(5)
    // The late h1 result neither replaced the h2 dialog's count nor opened anything of its own.
    expect(screen.queryByTestId('restart-daemon-workers')).toBeNull()
  })

  it('ABA: h1 -> h2 -> h1 mid-count still drops the original count', async () => {
    const settle = pendingCount()
    const view = render(<RestartDaemonButton hostId="h1" />)
    fireEvent.click(screen.getByTestId('restart-daemon'))
    view.rerender(<RestartDaemonButton hostId="h2" />)
    view.rerender(<RestartDaemonButton hostId="h1" />)
    await settle(2)
    expect(dialog()).toBeNull()
    expect(screen.getByTestId('restart-daemon').getAttribute('aria-busy')).toBeNull()
  })

  it('host changes while the dialog is open: the dialog is gone and does not come back', async () => {
    const view = await openConfirm(2)
    view.rerender(<RestartDaemonButton hostId="h2" />)
    expect(dialog()).toBeNull()
    expect(screen.queryByTestId('restart-daemon-confirm-confirm')).toBeNull()
    view.rerender(<RestartDaemonButton hostId="h1" />)
    expect(dialog()).toBeNull()
    expect(restart).not.toHaveBeenCalled()
  })

  it('another entry point restarts the host mid-count: no dialog, not even once the restart ends', async () => {
    const settle = pendingCount()
    render(<RestartDaemonButton hostId="h1" />)
    fireEvent.click(screen.getByTestId('restart-daemon'))
    act(() => useDaemonRestartStore.setState({ restarting: { h1: true } }))
    await settle(2)
    expect(dialog()).toBeNull()
    act(() => useDaemonRestartStore.setState({ restarting: {}, settled: { h1: 1 } }))
    expect(dialog()).toBeNull()
    expect(restart).not.toHaveBeenCalled()
  })

  it('another entry point restarts the host while the dialog is open: it closes and does not reappear', async () => {
    await openConfirm(2)
    act(() => useDaemonRestartStore.setState({ restarting: { h1: true } }))
    expect(dialog()).toBeNull()
    act(() => useDaemonRestartStore.setState({ restarting: {}, settled: { h1: 1 } }))
    expect(dialog()).toBeNull()
    expect(restart).not.toHaveBeenCalled()
  })

  it('a restart elsewhere that started AND finished mid-count (settled bumped, restarting empty) drops the count (C1)', async () => {
    const settle = pendingCount()
    render(<RestartDaemonButton hostId="h1" />)
    fireEvent.click(screen.getByTestId('restart-daemon'))
    act(() => useDaemonRestartStore.setState({ settled: { h1: 1 } }))
    await settle(2)
    expect(dialog()).toBeNull()
    expect(restart).not.toHaveBeenCalled()
  })

  it('a restart elsewhere settling while the dialog is open closes it (C1)', async () => {
    await openConfirm(2)
    act(() => useDaemonRestartStore.setState({ settled: { h1: 1 } }))
    expect(dialog()).toBeNull()
    expect(restart).not.toHaveBeenCalled()
  })

  it('a settled bump on another host leaves this dialog alone', async () => {
    await openConfirm(2)
    act(() => useDaemonRestartStore.setState({ settled: { h2: 1 } }))
    expect(dialog()).not.toBeNull()
  })

  it('the caller locks mid-count: no dialog, not even once it unlocks', async () => {
    const settle = pendingCount()
    const view = render(<RestartDaemonButton hostId="h1" />)
    fireEvent.click(screen.getByTestId('restart-daemon'))
    view.rerender(<RestartDaemonButton hostId="h1" disabled />)
    await settle(2)
    expect(dialog()).toBeNull()
    view.rerender(<RestartDaemonButton hostId="h1" />)
    expect(dialog()).toBeNull()
    expect(restart).not.toHaveBeenCalled()
  })

  it('the caller locks while the dialog is open: it closes and does not reappear on unlock', async () => {
    const view = await openConfirm(2)
    view.rerender(<RestartDaemonButton hostId="h1" disabled />)
    expect(dialog()).toBeNull()
    view.rerender(<RestartDaemonButton hostId="h1" />)
    expect(dialog()).toBeNull()
    expect(restart).not.toHaveBeenCalled()
  })

  it('Confirm re-reads the store: a restart begun elsewhere since the last render is not doubled', async () => {
    await openConfirm(0)
    const confirmBtn = screen.getByTestId('restart-daemon-confirm-confirm')
    // One act: the click lands on the dialog as last rendered, before the store change re-renders it.
    act(() => {
      useDaemonRestartStore.setState({ restarting: { h1: true } })
      fireEvent.click(confirmBtn)
    })
    expect(restart).not.toHaveBeenCalled()
    expect(dialog()).toBeNull()
  })

  it('Confirm re-reads the generation: a restart that finished elsewhere since the last render is not repeated (C1)', async () => {
    await openConfirm(0)
    const confirmBtn = screen.getByTestId('restart-daemon-confirm-confirm')
    act(() => {
      useDaemonRestartStore.setState({ settled: { h1: 1 } })
      fireEvent.click(confirmBtn)
    })
    expect(restart).not.toHaveBeenCalled()
    expect(dialog()).toBeNull()
  })

  it('happy path unchanged: confirm calls restart(h1, name) exactly once', async () => {
    await openConfirm(1)
    await act(async () => { fireEvent.click(screen.getByTestId('restart-daemon-confirm-confirm')) })
    expect(restart).toHaveBeenCalledTimes(1)
    expect(restart).toHaveBeenCalledWith('h1', expect.any(String))
  })
})

// Critic C2: the caller hears while a flow (count or dialog) is active, so it can lock its other controls.
describe('RestartDaemonButton - onActiveChange', () => {
  it('true when counting starts, no flip while the dialog opens, false after Cancel', async () => {
    const onActive = vi.fn()
    const settle = pendingCount()
    render(<RestartDaemonButton hostId="h1" onActiveChange={onActive} />)
    expect(onActive).not.toHaveBeenCalled()
    fireEvent.click(screen.getByTestId('restart-daemon'))
    expect(onActive.mock.calls).toEqual([[true]])
    await settle(0)
    await screen.findByTestId('restart-daemon-confirm-dialog')
    expect(onActive.mock.calls).toEqual([[true]])
    fireEvent.click(screen.getByTestId('restart-daemon-confirm-cancel'))
    expect(onActive.mock.calls).toEqual([[true], [false]])
  })

  it('false after Confirm', async () => {
    const onActive = vi.fn()
    await openConfirm(0, onActive)
    await act(async () => { fireEvent.click(screen.getByTestId('restart-daemon-confirm-confirm')) })
    expect(onActive.mock.calls).toEqual([[true], [false]])
  })

  it('false once a stale count is dropped', async () => {
    const onActive = vi.fn()
    const settle = pendingCount()
    const view = render(<RestartDaemonButton hostId="h1" onActiveChange={onActive} />)
    fireEvent.click(screen.getByTestId('restart-daemon'))
    view.rerender(<RestartDaemonButton hostId="h2" onActiveChange={onActive} />)
    await settle(2)
    expect(onActive.mock.calls).toEqual([[true], [false]])
  })

  it('false immediately when the host changes mid-count, before the old count settles', () => {
    const onActiveChange = vi.fn()
    pendingCount()
    const view = render(<RestartDaemonButton hostId="h1" onActiveChange={onActiveChange} />)
    fireEvent.click(screen.getByTestId('restart-daemon'))
    expect(onActiveChange.mock.calls).toEqual([[true]])
    view.rerender(<RestartDaemonButton hostId="h2" onActiveChange={onActiveChange} />)
    expect(onActiveChange.mock.calls).toEqual([[true], [false]])
  })

  it('false when a stale dialog is closed', async () => {
    const onActive = vi.fn()
    await openConfirm(2, onActive)
    act(() => useDaemonRestartStore.setState({ restarting: { h1: true } }))
    expect(onActive.mock.calls).toEqual([[true], [false]])
  })

  it('false on unmount while active', async () => {
    const onActive = vi.fn()
    pendingCount()
    const view = render(<RestartDaemonButton hostId="h1" onActiveChange={onActive} />)
    fireEvent.click(screen.getByTestId('restart-daemon'))
    view.unmount()
    expect(onActive.mock.calls).toEqual([[true], [false]])
  })

  it('a locked click never reports active', () => {
    const onActive = vi.fn()
    render(<RestartDaemonButton hostId="h1" disabled onActiveChange={onActive} />)
    fireEvent.click(screen.getByTestId('restart-daemon'))
    expect(onActive).not.toHaveBeenCalled()
  })

  describe('open approval requests (lead-team spec §9.5)', () => {
    const approval = (id: string): Approval => ({
      id, kind: 'lead', host_id: 'd1',
      origin: { session_id: 'S', ref: '_abcdef', name: 'n', pid: 1, proc_start: 'p', cwd: '/w', tmux: '' },
      payload: { reason: 'r', max_members: 3, roots: ['/w'] },
      state: 'open', created_at: 1, deadline_at: 2, lease_until: 3,
    })
    const daemonSays = (approvals_open: number) =>
      vi.mocked(approvalApi.fetchInflight).mockResolvedValueOnce({ approvals_open, relays_active: 0 })

    it('asks GET /api/team/inflight for THIS host and names its open requests', async () => {
      // Dropping the fetch, or reading the wrong field, turns this red (mutation deliverable).
      daemonSays(2)
      await openConfirm(0)
      expect(approvalApi.fetchInflight).toHaveBeenCalledTimes(1)
      expect(approvalApi.fetchInflight).toHaveBeenCalledWith('h1')
      expect(screen.getByTestId('restart-daemon-approvals').textContent).toBe('2 個申請等待核准')
    })

    it('the daemon\'s count wins over the store\'s when both exist', async () => {
      useApprovalStore.getState().applyOpened('h1', approval('a'))
      daemonSays(3)
      await openConfirm(0)
      expect(screen.getByTestId('restart-daemon-approvals').textContent).toBe('3 個申請等待核准')
    })

    it('when the inflight call rejects it falls back to the store\'s count for this host only', async () => {
      // beforeEach leaves fetchInflight rejecting.
      useApprovalStore.getState().applyOpened('h1', approval('a'))
      useApprovalStore.getState().applyOpened('h1', approval('b'))
      useApprovalStore.getState().applyOpened('h2', approval('c'))
      await openConfirm(0)
      expect(approvalApi.fetchInflight).toHaveBeenCalledWith('h1')
      expect(screen.getByTestId('restart-daemon-approvals').textContent).toBe('2 個申請等待核准')
    })

    it('no line when the daemon says none and the store has none for this host, even if another host does', async () => {
      daemonSays(0)
      useApprovalStore.getState().applyOpened('h2', approval('c'))
      await openConfirm(0)
      expect(screen.queryByTestId('restart-daemon-approvals')).toBeNull()
    })

    it('shows both lines when workers run and requests wait', async () => {
      daemonSays(1)
      await openConfirm(2)
      expect(screen.getByTestId('restart-daemon-workers')).toBeInTheDocument()
      expect(screen.getByTestId('restart-daemon-approvals').textContent).toBe('1 個申請等待核准')
    })
  })
})
