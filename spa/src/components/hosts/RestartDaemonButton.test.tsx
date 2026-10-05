import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, act } from '@testing-library/react'
import { RestartDaemonButton } from './RestartDaemonButton'
import { useDaemonRestartStore } from '../../stores/useDaemonRestartStore'
import { useI18nStore } from '../../stores/useI18nStore'
import * as restartLib from '../../lib/daemon-restart'

vi.mock('../../lib/daemon-restart', async (orig) => ({ ...(await orig<typeof import('../../lib/daemon-restart')>()), countRunningWorkers: vi.fn() }))

const restart = vi.fn(async () => {})
beforeEach(() => {
  useI18nStore.getState().setLocale('zh-TW')
  restart.mockClear()
  useDaemonRestartStore.setState({ restarting: {}, settled: {}, restart })
  vi.mocked(restartLib.countRunningWorkers).mockReset()
})

async function openConfirm(workers: number | null) {
  vi.mocked(restartLib.countRunningWorkers).mockResolvedValueOnce(workers)
  render(<RestartDaemonButton hostId="h1" />)
  await act(async () => { fireEvent.click(screen.getByTestId('restart-daemon')) })
  await screen.findByTestId('restart-daemon-confirm-dialog')
}

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
