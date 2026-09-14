import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor, act, fireEvent } from '@testing-library/react'
import { LocalDaemonSection } from './LocalDaemonSection'
import { useHostStore } from '../../stores/useHostStore'
import { useI18nStore } from '../../stores/useI18nStore'

const status = (o: Partial<ElectronLocalDaemonStatus> = {}): ElectronLocalDaemonStatus => ({
  managed: 'none', binPath: '/Users/t/.config/pdx/bin/pdx', installed: null, alive: null, running: null, config: null,
  hostname: 'air-2026',
  target: { goos: 'darwin', goarch: 'arm64' }, tools: { tmux: '/opt/homebrew/bin/tmux' }, ...o,
})
const result: ElectronLocalDaemonResult = { url: 'http://100.64.0.9:7860', token: 'purdex_t', hash: 'bbb', version: '9', hostname: 'air-2026' }

const mockStatus = vi.fn()
const mockInstall = vi.fn()
const mockStart = vi.fn()
const mockRestart = vi.fn()
let progressCb: ((s: string) => void) | null = null

beforeEach(() => {
  vi.clearAllMocks()
  useI18nStore.getState().setLocale('en')
  useHostStore.getState().reset()
  window.electronAPI = {
    ...window.electronAPI!,
    localDaemonStatus: mockStatus,
    localDaemonInstall: mockInstall,
    localDaemonStart: mockStart,
    localDaemonRestart: mockRestart,
    onLocalDaemonProgress: (cb: (s: string) => void) => { progressCb = cb; return () => { progressCb = null } },
  } as typeof window.electronAPI
})

const renderIt = (latestHash: string | null = 'bbb', refreshKey: unknown = { latest_hash: latestHash }) =>
  act(async () => { render(<LocalDaemonSection daemonBase="http://100.64.0.2:7860" token="tok" latestHash={latestHash} refreshKey={refreshKey} />) })

describe('LocalDaemonSection', () => {
  it('none → Install button and target', async () => {
    mockStatus.mockResolvedValue(status())
    await renderIt()
    expect(screen.getByText('No daemon installed on this machine')).toBeTruthy()
    expect(screen.getByText('darwin/arm64')).toBeTruthy()
    expect(screen.getByRole('button', { name: 'Install' })).toBeTruthy()
  })

  it('managed+stopped → Start, and Update when hash differs', async () => {
    mockStatus.mockResolvedValue(status({ managed: 'managed', installed: { version: '9', hash: 'aaa', goos: 'darwin', goarch: 'arm64' } }))
    await renderIt('bbb')
    expect(screen.getByRole('button', { name: 'Start' })).toBeTruthy()
    expect(screen.getByRole('button', { name: 'Update' })).toBeTruthy()
  })

  it('managed+running, same hash → Up to date, no Update', async () => {
    mockStatus.mockResolvedValue(status({ managed: 'managed', alive: { pid: 1 }, installed: { version: '9', hash: 'bbb', goos: 'darwin', goarch: 'arm64' }, running: { version: '9', hash: 'bbb', url: 'http://100.64.0.9:7860' } }))
    await renderIt('bbb')
    expect(screen.getByText('Up to date')).toBeTruthy()
    expect(screen.queryByRole('button', { name: 'Update' })).toBeNull()
  })

  it('managed+running with on-disk ≠ running → Restart', async () => {
    mockStatus.mockResolvedValue(status({ managed: 'managed', alive: { pid: 1 }, installed: { version: '9', hash: 'bbb', goos: 'darwin', goarch: 'arm64' }, running: { version: '9', hash: 'aaa', url: 'http://100.64.0.9:7860' } }))
    await renderIt('bbb')
    expect(screen.getByRole('button', { name: 'Restart' })).toBeTruthy()
    expect(screen.queryByRole('button', { name: 'Update' })).toBeNull()
  })

  it('Update wins over Restart when both would apply', async () => {
    mockStatus.mockResolvedValue(status({ managed: 'managed', alive: { pid: 1 }, installed: { version: '9', hash: 'bbb', goos: 'darwin', goarch: 'arm64' }, running: { version: '9', hash: 'aaa', url: 'http://100.64.0.9:7860' } }))
    await renderIt('ccc')
    expect(screen.getByRole('button', { name: 'Update' })).toBeTruthy()
    expect(screen.queryByRole('button', { name: 'Restart' })).toBeNull()
  })

  it('re-queries status when refreshKey changes even with the same hash', async () => {
    mockStatus.mockResolvedValue(status())
    const view = render(<LocalDaemonSection daemonBase="x" latestHash="bbb" refreshKey={{ latest_hash: 'bbb' }} />)
    await waitFor(() => expect(mockStatus).toHaveBeenCalledTimes(1))
    await act(async () => { view.rerender(<LocalDaemonSection daemonBase="x" latestHash="bbb" refreshKey={{ latest_hash: 'bbb' }} />) })
    await waitFor(() => expect(mockStatus).toHaveBeenCalledTimes(2))
  })

  it('alive but unhealthy → Restart with pid', async () => {
    mockStatus.mockResolvedValue(status({ managed: 'managed', alive: { pid: 4242 }, installed: { version: '9', hash: 'bbb', goos: 'darwin', goarch: 'arm64' } }))
    await renderIt()
    expect(screen.getByText(/4242/)).toBeTruthy()
    expect(screen.getByRole('button', { name: 'Restart' })).toBeTruthy()
  })

  it('external → full URL in the message, reason, no buttons', async () => {
    mockStatus.mockResolvedValue(status({ managed: 'external', reason: 'running daemon is /repo/bin/pdx', running: { version: 'unknown', hash: 'unknown', url: 'http://100.64.0.2:7860' } }))
    await renderIt()
    expect(screen.getByText('A daemon is running at http://100.64.0.2:7860 but is not managed by this app')).toBeTruthy()
    expect(screen.getByText(/\/repo\/bin\/pdx/)).toBeTruthy()
    for (const n of ['Install', 'Update', 'Start', 'Restart']) expect(screen.queryByRole('button', { name: n })).toBeNull()
  })

  it('external without running info falls back to the config endpoint', async () => {
    mockStatus.mockResolvedValue(status({ managed: 'external', reason: 'custom data_dir', config: { bind: '100.64.0.9', port: 7860, token: 'purdex_t' } }))
    await renderIt()
    expect(screen.getByText('A daemon is running at http://100.64.0.9:7860 but is not managed by this app')).toBeTruthy()
  })

  it('tmux missing → warning', async () => {
    mockStatus.mockResolvedValue(status({ tools: { tmux: null } }))
    await renderIt()
    expect(screen.getByText(/tmux not found/)).toBeTruthy()
  })

  it('install shows progress, registers the host once, refreshes status', async () => {
    mockStatus.mockResolvedValue(status())
    mockInstall.mockImplementation(async () => { progressCb?.('download'); return result })
    await renderIt()
    await act(async () => { fireEvent.click(screen.getByRole('button', { name: 'Install' })) })
    await waitFor(() => expect(mockInstall).toHaveBeenCalledWith('http://100.64.0.2:7860', 'tok'))
    const hosts = Object.values(useHostStore.getState().hosts).filter((h) => h.ip === '100.64.0.9')
    expect(hosts).toHaveLength(1)
    expect(hosts[0].token).toBe('purdex_t')
    expect(mockStatus.mock.calls.length).toBeGreaterThanOrEqual(2)
  })

  it('start also registers the host', async () => {
    mockStatus.mockResolvedValue(status({ managed: 'managed', installed: { version: '9', hash: 'bbb', goos: 'darwin', goarch: 'arm64' } }))
    mockStart.mockResolvedValue(result)
    await renderIt()
    await act(async () => { fireEvent.click(screen.getByRole('button', { name: 'Start' })) })
    await waitFor(() => expect(Object.values(useHostStore.getState().hosts).some((h) => h.ip === '100.64.0.9')).toBe(true))
  })

  it('install error renders inline and re-enables', async () => {
    mockStatus.mockResolvedValue(status())
    mockInstall.mockRejectedValue('download failed: sha256 mismatch')
    await renderIt()
    await act(async () => { fireEvent.click(screen.getByRole('button', { name: 'Install' })) })
    expect(await screen.findByText(/sha256 mismatch/)).toBeTruthy()
    expect((screen.getByRole('button', { name: 'Install' }) as HTMLButtonElement).disabled).toBe(false)
  })

  it('a post-swap start failure leaves the error and offers Start on the refreshed status', async () => {
    mockStatus
      .mockResolvedValueOnce(status())
      .mockResolvedValue(status({ managed: 'managed', installed: { version: '9', hash: 'bbb', goos: 'darwin', goarch: 'arm64' } }))
    mockInstall.mockRejectedValue('pdx start failed: bind: address not available')
    await renderIt()
    await act(async () => { fireEvent.click(screen.getByRole('button', { name: 'Install' })) })
    expect(await screen.findByText(/address not available/)).toBeTruthy()
    expect(screen.getByRole('button', { name: 'Start' })).toBeTruthy()
  })

  it('renders nothing when the bridge is absent (web build)', async () => {
    window.electronAPI = { ...window.electronAPI!, localDaemonStatus: undefined } as typeof window.electronAPI
    const { container } = render(<LocalDaemonSection daemonBase="x" latestHash={null} refreshKey={null} />)
    expect(container.innerHTML).toBe('')
  })
})
