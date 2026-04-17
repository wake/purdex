import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, waitFor, act, fireEvent } from '@testing-library/react'
import { DevEnvironmentSection } from './DevEnvironmentSection'

const mockGetAppInfo = vi.fn().mockResolvedValue({
  version: '1.0.0-alpha.21',
  electronHash: 'abc1234',
  spaHash: 'def5678',
  devUpdateEnabled: true,
})

const mockStreamCheck = vi.fn()
const mockApplyUpdate = vi.fn()
const mockForceLoadSPA = vi.fn().mockResolvedValue(undefined)

function baseCheck(overrides: Partial<ElectronRemoteVersionInfo> = {}): ElectronRemoteVersionInfo {
  return {
    version: '1.0.0-alpha.21',
    spaHash: 'def5678',
    electronHash: 'abc1234',
    source: { spaHash: 'src111', electronHash: 'src222' },
    building: false,
    buildError: '',
    requiresFullRebuild: false,
    ...overrides,
  }
}

// Capture the latest streamCheck callback so tests can drive events post-render.
let lastStreamCallback: ((ev: ElectronStreamCheckEvent) => void) | null = null
let lastStreamClose = vi.fn()

function arrangeStream(emitInline?: (cb: (ev: ElectronStreamCheckEvent) => void) => void) {
  mockStreamCheck.mockImplementation((_url: string, _tok: string | undefined, cb: (ev: ElectronStreamCheckEvent) => void) => {
    lastStreamCallback = cb
    lastStreamClose = vi.fn()
    emitInline?.(cb)
    return lastStreamClose
  })
}

beforeEach(() => {
  vi.clearAllMocks()
  lastStreamCallback = null
  window.electronAPI = {
    ...window.electronAPI!,
    getAppInfo: mockGetAppInfo,
    streamCheck: mockStreamCheck,
    applyUpdate: mockApplyUpdate,
    forceLoadSPA: mockForceLoadSPA,
  } as typeof window.electronAPI
  // Default: emit a non-stale check immediately
  arrangeStream((cb) => {
    cb({ type: 'check', check: baseCheck() })
    cb({ type: 'done', check: baseCheck() })
  })
})

afterEach(() => {
  vi.useRealTimers()
})

describe('DevEnvironmentSection', () => {
  it('renders section title', async () => {
    await act(async () => { render(<DevEnvironmentSection />) })
    expect(screen.getByText(/Development|開發環境/)).toBeTruthy()
  })

  it('calls getAppInfo on mount and opens stream', async () => {
    await act(async () => { render(<DevEnvironmentSection />) })
    await waitFor(() => expect(mockGetAppInfo).toHaveBeenCalledOnce())
    await waitFor(() => expect(mockStreamCheck).toHaveBeenCalled())
  })

  it('shows building status and renders the log panel while build streams', async () => {
    // Hold the stream open so we can push events manually
    arrangeStream()

    await act(async () => { render(<DevEnvironmentSection />) })

    // Emit initial check (building=true), then phase + stdout events
    await act(async () => {
      lastStreamCallback!({ type: 'check', check: baseCheck({ building: true, spaHash: 'old', electronHash: 'old' }) })
    })
    await waitFor(() => expect(screen.getByText(/Building|建置中/)).toBeTruthy())

    await act(async () => {
      lastStreamCallback!({ type: 'phase', phase: 'install' })
      lastStreamCallback!({ type: 'stdout', line: 'resolving dependencies' })
    })

    const pre = screen.getByTestId('dev-build-log')
    expect(pre.textContent).toContain('── install ──')
    expect(pre.textContent).toContain('resolving dependencies')

    // Emit terminal done with fresh hashes — status should flip to update_available
    await act(async () => {
      lastStreamCallback!({ type: 'done', check: baseCheck({ spaHash: 'new5678', electronHash: 'newabc1' }) })
    })
    await waitFor(() => expect(screen.getByText(/Update available|有新版本/)).toBeTruthy())
  })

  it('shows buildError when done check carries it', async () => {
    arrangeStream((cb) => {
      cb({ type: 'check', check: baseCheck({ building: true, spaHash: 'old', electronHash: 'old' }) })
      cb({ type: 'stderr', line: 'ERR_SOMETHING' })
      cb({ type: 'done', check: baseCheck({ spaHash: 'old', electronHash: 'old', buildError: 'exit code 1' }) })
    })

    await act(async () => { render(<DevEnvironmentSection />) })
    await waitFor(() => expect(screen.getByText('exit code 1')).toBeTruthy())
  })

  it('shows requiresFullRebuild hint banner', async () => {
    arrangeStream((cb) => {
      const check = baseCheck({ requiresFullRebuild: true, fullRebuildReason: 'rebuild-tracked paths changed (old → new)' })
      cb({ type: 'check', check })
      cb({ type: 'done', check })
    })

    await act(async () => { render(<DevEnvironmentSection />) })
    await waitFor(() => expect(screen.getByText(/Full app rebuild recommended|建議重跑完整打包/)).toBeTruthy())
    expect(screen.getByText('rebuild-tracked paths changed (old → new)')).toBeTruthy()
  })

  it('closes the stream on unmount', async () => {
    arrangeStream()
    const { unmount } = await act(async () => render(<DevEnvironmentSection />))
    await waitFor(() => expect(mockStreamCheck).toHaveBeenCalled())
    unmount()
    expect(lastStreamClose).toHaveBeenCalled()
  })

  describe('SPA source mode', () => {
    it('shows "Dev Server" when loaded from http: protocol', async () => {
      await act(async () => { render(<DevEnvironmentSection />) })
      await waitFor(() => expect(screen.getByText('Dev Server')).toBeTruthy())
    })

    it('shows "Bundled" when loaded from app: protocol', async () => {
      const originalProtocol = window.location.protocol
      Object.defineProperty(window, 'location', {
        value: { ...window.location, protocol: 'app:' },
        writable: true,
      })
      try {
        await act(async () => { render(<DevEnvironmentSection />) })
        await waitFor(() => expect(screen.getByText('Bundled')).toBeTruthy())
      } finally {
        Object.defineProperty(window, 'location', {
          value: { ...window.location, protocol: originalProtocol },
          writable: true,
        })
      }
    })

    it('shows switch button and calls forceLoadSPA("bundled") from dev mode', async () => {
      await act(async () => { render(<DevEnvironmentSection />) })
      await waitFor(() => expect(screen.getByText('Dev Server')).toBeTruthy())
      const switchBtn = screen.getByRole('button', { name: /Bundled/i })
      fireEvent.click(switchBtn)
      expect(mockForceLoadSPA).toHaveBeenCalledWith('bundled')
    })

    it('shows switch button and calls forceLoadSPA("dev") from bundled mode', async () => {
      const originalProtocol = window.location.protocol
      Object.defineProperty(window, 'location', {
        value: { ...window.location, protocol: 'app:' },
        writable: true,
      })
      try {
        await act(async () => { render(<DevEnvironmentSection />) })
        await waitFor(() => expect(screen.getByText('Bundled')).toBeTruthy())
        const switchBtn = screen.getByRole('button', { name: /Dev Server/i })
        fireEvent.click(switchBtn)
        expect(mockForceLoadSPA).toHaveBeenCalledWith('dev')
      } finally {
        Object.defineProperty(window, 'location', {
          value: { ...window.location, protocol: originalProtocol },
          writable: true,
        })
      }
    })

    it('shows error when switching to bundled fails', async () => {
      mockForceLoadSPA.mockRejectedValueOnce('protocol error')
      await act(async () => { render(<DevEnvironmentSection />) })
      await waitFor(() => expect(screen.getByText('Dev Server')).toBeTruthy())
      const switchBtn = screen.getByRole('button', { name: /Bundled/i })
      await act(async () => { fireEvent.click(switchBtn) })
      await waitFor(() => expect(screen.getByText(/Failed to load bundled SPA.*protocol error/)).toBeTruthy())
    })

    it('shows error when switching to dev server fails', async () => {
      mockForceLoadSPA.mockRejectedValueOnce('ERR_CONNECTION_REFUSED')
      const originalProtocol = window.location.protocol
      Object.defineProperty(window, 'location', {
        value: { ...window.location, protocol: 'app:' },
        writable: true,
      })
      try {
        await act(async () => { render(<DevEnvironmentSection />) })
        await waitFor(() => expect(screen.getByText('Bundled')).toBeTruthy())
        const switchBtn = screen.getByRole('button', { name: /Dev Server/i })
        await act(async () => { fireEvent.click(switchBtn) })
        await waitFor(() => expect(screen.getByText(/Dev server is not reachable.*ERR_CONNECTION_REFUSED/)).toBeTruthy())
      } finally {
        Object.defineProperty(window, 'location', {
          value: { ...window.location, protocol: originalProtocol },
          writable: true,
        })
      }
    })
  })
})
