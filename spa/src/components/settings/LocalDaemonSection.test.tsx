import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, waitFor, act, fireEvent, cleanup } from '@testing-library/react'
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
const mockPathLink = vi.fn()
const mockPathAddToShell = vi.fn()
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
    localDaemonPathLink: mockPathLink,
    localDaemonPathAddToShell: mockPathAddToShell,
    onLocalDaemonProgress: (cb: (s: string) => void) => { progressCb = cb; return () => { progressCb = null } },
  } as typeof window.electronAPI
  Object.defineProperty(navigator, 'clipboard', { value: { writeText: vi.fn().mockResolvedValue(undefined) }, configurable: true })
})

const renderIt = (latestHash: string | null = 'bbb', refreshKey: unknown = { latest_hash: latestHash }, daemonBase: string | null = 'http://100.64.0.2:7860') =>
  act(async () => { render(<LocalDaemonSection daemonBase={daemonBase} token="tok" latestHash={latestHash} refreshKey={refreshKey} />) })

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

describe('LocalDaemonSection - no dev host', () => {
  it('Install disabled (none) and Update disabled (managed, stale); Start still enabled', async () => {
    mockStatus.mockResolvedValue(status())
    await renderIt('bbb', { latest_hash: 'bbb' }, null)
    expect(screen.getByRole('button', { name: 'Install' })).toBeDisabled()
    cleanup()
    mockStatus.mockResolvedValue(status({ managed: 'managed', installed: { version: '9', hash: 'aaa', goos: 'darwin', goarch: 'arm64' } }))
    await renderIt('bbb', { latest_hash: 'bbb' }, null)
    expect(screen.getByRole('button', { name: 'Update' })).toBeDisabled()
    expect(screen.getByRole('button', { name: 'Start' })).not.toBeDisabled()
    expect(mockInstall).not.toHaveBeenCalled()
  })
})

describe('LocalDaemonSection - config rows', () => {
  const cfg = { bind: '100.64.0.9', port: 7860, token: 'purdex_secret' }
  const originalExecCommand = document.execCommand

  afterEach(() => { document.execCommand = originalExecCommand })

  it('shows URL and a masked token; reveal and copy work', async () => {
    mockStatus.mockResolvedValue(status({ managed: 'external', reason: 'x', config: cfg }))
    await renderIt()
    expect(screen.getByText('http://100.64.0.9:7860')).toBeTruthy()
    expect(screen.queryByText('purdex_secret')).toBeNull()
    fireEvent.click(screen.getByRole('button', { name: 'Show token' }))
    expect(screen.getByText('purdex_secret')).toBeTruthy()
    fireEvent.click(screen.getByRole('button', { name: 'Hide token' }))
    expect(screen.queryByText('purdex_secret')).toBeNull()
    await act(async () => { fireEvent.click(screen.getByRole('button', { name: 'Copy token' })) })
    expect(navigator.clipboard.writeText).toHaveBeenCalledWith('purdex_secret')
    expect(screen.getByText('Copied')).toBeTruthy()
  })

  it('token missing → notice, Add to hosts disabled', async () => {
    mockStatus.mockResolvedValue(status({ managed: 'managed', config: { ...cfg, token: null } }))
    await renderIt()
    expect(screen.getByText('No token in config.toml')).toBeTruthy()
    expect(screen.getByRole('button', { name: 'Add to hosts' })).toBeDisabled()
  })

  it('endpoint not in host list → Add to hosts registers it with the config token and hostname', async () => {
    mockStatus.mockResolvedValue(status({ managed: 'external', reason: 'x', config: cfg }))
    await renderIt()
    await act(async () => { fireEvent.click(screen.getByRole('button', { name: 'Add to hosts' })) })
    const added = Object.values(useHostStore.getState().hosts).find((h) => h.ip === '100.64.0.9' && h.port === 7860)
    expect(added).toMatchObject({ name: 'air-2026', token: 'purdex_secret' })
    expect(screen.getByText('Registered as air-2026')).toBeTruthy()
    expect(screen.queryByRole('button', { name: 'Add to hosts' })).toBeNull()
  })

  it('endpoint already in host list → shows the host name, no button', async () => {
    useHostStore.getState().addHost({ name: 'my-air', ip: '100.64.0.9', port: 7860, token: 't' })
    mockStatus.mockResolvedValue(status({ managed: 'managed', config: cfg }))
    await renderIt()
    expect(screen.getByText('Registered as my-air')).toBeTruthy()
    expect(screen.queryByRole('button', { name: 'Add to hosts' })).toBeNull()
  })

  it('loopback bind is a different endpoint from the Tailscale host entry', async () => {
    useHostStore.getState().addHost({ name: 'my-air', ip: '100.64.0.9', port: 7860, token: 't' })
    mockStatus.mockResolvedValue(status({ managed: 'managed', config: { ...cfg, bind: '127.0.0.1' } }))
    await renderIt()
    expect(screen.getByRole('button', { name: 'Add to hosts' })).toBeTruthy()
  })

  it('copy failure surfaces the error and does not show Copied', async () => {
    Object.defineProperty(navigator, 'clipboard', { value: { writeText: vi.fn().mockRejectedValue(new Error('denied')) }, configurable: true })
    mockStatus.mockResolvedValue(status({ managed: 'external', reason: 'x', config: cfg }))
    await renderIt()
    await act(async () => { fireEvent.click(screen.getByRole('button', { name: 'Copy token' })) })
    expect(screen.getByText('Copy failed — select the token and copy it manually')).toBeTruthy()
    expect(screen.queryByText('Copied')).toBeNull()
  })

  it('copy retried after a failure shows Copied and clears the failure text', async () => {
    const writeText = vi.fn().mockRejectedValueOnce(new Error('denied')).mockResolvedValueOnce(undefined)
    Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true })
    mockStatus.mockResolvedValue(status({ managed: 'external', reason: 'x', config: cfg }))
    await renderIt()
    await act(async () => { fireEvent.click(screen.getByRole('button', { name: 'Copy token' })) })
    expect(screen.getByText('Copy failed — select the token and copy it manually')).toBeTruthy()
    expect(screen.queryByText('Copied')).toBeNull()
    await act(async () => { fireEvent.click(screen.getByRole('button', { name: 'Copy token' })) })
    expect(screen.getByText('Copied')).toBeTruthy()
    expect(screen.queryByText('Copy failed — select the token and copy it manually')).toBeNull()
  })

  it('copy failing after a prior success clears Copied and shows the failure text', async () => {
    const writeText = vi.fn().mockResolvedValueOnce(undefined).mockRejectedValueOnce(new Error('denied'))
    Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true })
    mockStatus.mockResolvedValue(status({ managed: 'external', reason: 'x', config: cfg }))
    await renderIt()
    await act(async () => { fireEvent.click(screen.getByRole('button', { name: 'Copy token' })) })
    expect(screen.getByText('Copied')).toBeTruthy()
    await act(async () => { fireEvent.click(screen.getByRole('button', { name: 'Copy token' })) })
    expect(screen.getByText('Copy failed — select the token and copy it manually')).toBeTruthy()
    expect(screen.queryByText('Copied')).toBeNull()
  })

  it('insecure origin (no navigator.clipboard) falls back to execCommand and still shows Copied', async () => {
    Object.defineProperty(navigator, 'clipboard', { value: undefined, configurable: true })
    document.execCommand = vi.fn(() => true)
    mockStatus.mockResolvedValue(status({ managed: 'external', reason: 'x', config: cfg }))
    await renderIt()
    await act(async () => { fireEvent.click(screen.getByRole('button', { name: 'Copy token' })) })
    expect(document.execCommand).toHaveBeenCalledWith('copy')
    expect(screen.getByText('Copied')).toBeTruthy()
  })
})

describe('LocalDaemonSection - CLI block (spec §5)', () => {
  const cli = (o: Partial<NonNullable<ElectronLocalDaemonStatus['cli']>> = {}): ElectronLocalDaemonStatus['cli'] => ({
    resolved: '/Users/t/.local/bin/pdx', isManagedBinary: true, pathSource: 'shell',
    localBinOnPath: true, link: 'ok', ...o,
  })
  const managed = (c: ElectronLocalDaemonStatus['cli']) =>
    status({ managed: 'managed', installed: { version: '9', hash: 'bbb', goos: 'darwin', goarch: 'arm64' }, cli: c })

  it('shows what pdx resolves to, both commands, and both buttons', async () => {
    mockStatus.mockResolvedValue(managed(cli()))
    await renderIt('bbb')
    expect(screen.getByText('/Users/t/.local/bin/pdx')).toBeTruthy()
    expect(screen.getByRole('button', { name: 'Create symlink' })).toBeTruthy()
    expect(screen.getByRole('button', { name: 'Add to PATH' })).toBeTruthy()
    expect(screen.getByText('/Users/t/.config/pdx/bin/pdx path link')).toBeTruthy()
    expect(screen.getByText('/Users/t/.config/pdx/bin/pdx path add-to-shell')).toBeTruthy()
  })

  it('warns, and names the winner, when the resolved pdx is not the managed binary', async () => {
    mockStatus.mockResolvedValue(managed(cli({ resolved: '/repo/bin/pdx', isManagedBinary: false })))
    await renderIt('bbb')
    expect(screen.getByText(/not the binary this app manages/)).toBeTruthy()
    expect(screen.getByText('/repo/bin/pdx')).toBeTruthy()
  })

  it('says plainly when pdx is not on PATH at all', async () => {
    mockStatus.mockResolvedValue(managed(cli({ resolved: null, isManagedBinary: false, localBinOnPath: false, link: 'missing' })))
    await renderIt('bbb')
    expect(screen.getByText(/command not found/)).toBeTruthy()
  })

  it('shows the fallback caveat only when the shell PATH could not be read (spec §7.7)', async () => {
    mockStatus.mockResolvedValue(managed(cli({ pathSource: 'fallback' })))
    await renderIt('bbb')
    const caveat = screen.getByText(/could not be verified/)
    expect(caveat).toBeTruthy()
    expect(caveat.textContent).toMatch(/pdx path/)
    cleanup()
    mockStatus.mockResolvedValue(managed(cli()))
    await renderIt('bbb')
    expect(screen.queryByText(/could not be verified/)).toBeNull()
  })

  it('Create symlink runs the command and shows its output', async () => {
    mockStatus.mockResolvedValue(managed(cli()))
    mockPathLink.mockResolvedValue({ code: 0, stdout: 'created /Users/t/.local/bin/pdx\n', stderr: '' })
    await renderIt('bbb')
    await act(async () => { fireEvent.click(screen.getByRole('button', { name: 'Create symlink' })) })
    await waitFor(() => expect(mockPathLink).toHaveBeenCalled())
    expect(screen.getByText(/created \/Users\/t\/.local\/bin\/pdx/)).toBeTruthy()
  })

  it("a refusal is readable, not swallowed — the whole value is the path it names", async () => {
    mockStatus.mockResolvedValue(managed(cli()))
    mockPathLink.mockResolvedValue({ code: 1, stdout: '', stderr: '/Users/t/.local/bin/pdx is a symlink to /repo/bin/pdx; use --force\n' })
    await renderIt('bbb')
    await act(async () => { fireEvent.click(screen.getByRole('button', { name: 'Create symlink' })) })
    expect(await screen.findByText(/is a symlink to \/repo\/bin\/pdx; use --force/)).toBeTruthy()
  })

  it('Add to PATH runs the other command', async () => {
    mockStatus.mockResolvedValue(managed(cli({ localBinOnPath: false })))
    mockPathAddToShell.mockResolvedValue({ code: 0, stdout: 'added to /Users/t/.zshrc\n', stderr: '' })
    await renderIt('bbb')
    await act(async () => { fireEvent.click(screen.getByRole('button', { name: 'Add to PATH' })) })
    await waitFor(() => expect(mockPathAddToShell).toHaveBeenCalled())
    expect(screen.getByText(/added to \/Users\/t\/.zshrc/)).toBeTruthy()
  })

  it('no CLI block when there is no binary to ask', async () => {
    mockStatus.mockResolvedValue(status())
    await renderIt('bbb')
    expect(screen.queryByRole('button', { name: 'Create symlink' })).toBeNull()
  })
})
