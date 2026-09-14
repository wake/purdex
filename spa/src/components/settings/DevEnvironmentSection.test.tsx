import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, waitFor, act, fireEvent } from '@testing-library/react'
import { DevEnvironmentSection } from './DevEnvironmentSection'
import { useHostStore, selectDevHostId } from '../../stores/useHostStore'

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
  useHostStore.getState().reset()
  useHostStore.getState().setDevHost(useHostStore.getState().hostOrder[0])
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
    expect(screen.getByRole('heading', { name: /Development Environment|開發環境/ })).toBeTruthy()
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

  it('restarts the stream when daemonBase changes', async () => {
    arrangeStream((cb) => {
      cb({ type: 'check', check: baseCheck() })
      cb({ type: 'done', check: baseCheck() })
    })
    await act(async () => { render(<DevEnvironmentSection />) })
    await waitFor(() => expect(mockStreamCheck).toHaveBeenCalledTimes(1))
    // Capture the first stream's close before arrangeStream replaces it on
    // the next mockImplementation call.
    const firstClose = lastStreamClose

    const hostId = selectDevHostId(useHostStore.getState())!
    await act(async () => {
      useHostStore.getState().updateHost(hostId, { port: 9999 })
    })

    await waitFor(() => expect(mockStreamCheck).toHaveBeenCalledTimes(2))
    expect(firstClose).toHaveBeenCalled()
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

describe('DevEnvironmentSection - Daemon block', () => {
  const originalFetch = globalThis.fetch

  beforeEach(() => {
    globalThis.fetch = vi.fn(async (url: string | URL) => {
      const href = String(url)
      if (href.endsWith('/api/dev/daemon/check')) {
        return new Response(JSON.stringify({ current_hash: 'abc1234', latest_hash: 'abc1234', available: false }), {
          status: 200,
          headers: { 'Content-Type': 'application/json' },
        })
      }
      // Fallback: minimal 200 with empty body
      return new Response('{}', { status: 200 })
    }) as typeof globalThis.fetch
  })

  afterEach(() => { globalThis.fetch = originalFetch })

  it('renders Daemon heading', async () => {
    await act(async () => { render(<DevEnvironmentSection />) })
    await waitFor(() => {
      const headings = screen.getAllByText(/Daemon|daemon/i)
      expect(headings.length).toBeGreaterThan(0)
    })
  })

  it('shows Rebuild button', async () => {
    await act(async () => { render(<DevEnvironmentSection />) })
    await waitFor(() => expect(screen.getByText(/Rebuild|重新建置|rebuild/i)).toBeTruthy())
  })

  it('displays current_hash and latest_hash after fetch', async () => {
    await act(async () => { render(<DevEnvironmentSection />) })
    await waitFor(() => {
      const abc = screen.queryAllByText('abc1234')
      expect(abc.length).toBeGreaterThan(0)
    })
  })
})

describe('DevEnvironmentSection - dev host picker', () => {
  it('renders the picker with every host and the current selection', async () => {
    const extra = useHostStore.getState().addHost({ name: 'air', ip: '100.64.0.4', port: 7860 })
    await act(async () => { render(<DevEnvironmentSection />) })
    const select = screen.getByLabelText('Development host') as HTMLSelectElement
    expect(select.value).toBe(useHostStore.getState().hostOrder[0])
    expect(screen.getByRole('option', { name: 'air (100.64.0.4:7860)' })).toBeTruthy()
    expect(screen.getByRole('option', { name: '— not set —' })).toBeTruthy()
    fireEvent.change(select, { target: { value: extra } })
    expect(useHostStore.getState().devHostId).toBe(extra)
  })

  it('with no dev host: shows the notice, makes no requests, disables the buttons', async () => {
    useHostStore.getState().setDevHost(null)
    const fetchSpy = vi.spyOn(globalThis, 'fetch')
    await act(async () => { render(<DevEnvironmentSection />) })
    await waitFor(() => expect(mockGetAppInfo).toHaveBeenCalled())
    expect(screen.getByText(/Pick a development host first/)).toBeTruthy()
    expect(mockStreamCheck).not.toHaveBeenCalled()
    expect(fetchSpy).not.toHaveBeenCalled()
    expect(screen.getByRole('button', { name: 'Check Update' })).toBeDisabled()          // daemon block
    expect(screen.getByRole('button', { name: 'Rebuild & Restart' })).toBeDisabled()
    expect(screen.getByRole('button', { name: 'Check for Updates' })).toBeDisabled()      // app block
    fetchSpy.mockRestore()
  })

  it('picking a host starts the check against that host', async () => {
    useHostStore.getState().setDevHost(null)
    const extra = useHostStore.getState().addHost({ name: 'air', ip: '100.64.0.4', port: 7860, token: 'tok-air' })
    await act(async () => { render(<DevEnvironmentSection />) })
    await waitFor(() => expect(mockGetAppInfo).toHaveBeenCalled())
    expect(mockStreamCheck).not.toHaveBeenCalled()
    await act(async () => { fireEvent.change(screen.getByLabelText('Development host'), { target: { value: extra } }) })
    await waitFor(() => expect(mockStreamCheck).toHaveBeenCalledWith('http://100.64.0.4:7860', 'tok-air', expect.any(Function)))
  })
})

describe('DevEnvironmentSection - source change discipline', () => {
  const originalFetch = globalThis.fetch
  afterEach(() => { globalThis.fetch = originalFetch; vi.useRealTimers() })

  function deferred<T>() {
    let resolve!: (v: T) => void
    const promise = new Promise<T>((r) => { resolve = r })
    return { promise, resolve }
  }

  const checkJson = (body: unknown) => new Response(JSON.stringify(body), { status: 200, headers: { 'Content-Type': 'application/json' } })

  it('A → unset: A\'s painted daemon check is cleared', async () => {
    globalThis.fetch = vi.fn(async (url: string | URL) =>
      String(url).endsWith('/api/dev/daemon/check') ? checkJson({ current_hash: 'aaa', latest_hash: 'bbb', available: true }) : new Response('{}', { status: 200 })) as typeof globalThis.fetch
    await act(async () => { render(<DevEnvironmentSection />) })
    await waitFor(() => expect(screen.getByText('aaa')).toBeTruthy()) // A's result is on screen first
    await act(async () => { useHostStore.getState().setDevHost(null) })
    expect(screen.queryByText('aaa')).toBeNull()
    expect(screen.queryByText('Current hash')).toBeNull()
  })

  it('A → unset: A\'s late daemon-check response is discarded', async () => {
    const d = deferred<Response>()
    globalThis.fetch = vi.fn(async (url: string | URL) =>
      String(url).endsWith('/api/dev/daemon/check') ? d.promise : new Response('{}', { status: 200 })) as typeof globalThis.fetch
    await act(async () => { render(<DevEnvironmentSection />) })
    await act(async () => { useHostStore.getState().setDevHost(null) })
    await act(async () => { d.resolve(checkJson({ current_hash: 'aaa', latest_hash: 'bbb', available: true })) })
    expect(screen.queryByText('aaa')).toBeNull()
    expect(screen.queryByText('Current hash')).toBeNull()
  })

  it('A → unset: A\'s late rebuild 409 does not paint an error', async () => {
    const d = deferred<Response>()
    globalThis.fetch = vi.fn(async (url: string | URL, init?: RequestInit) => {
      const href = String(url)
      if (href.endsWith('/api/dev/daemon/rebuild') && init?.method === 'POST') return d.promise
      if (href.endsWith('/api/dev/daemon/check')) return checkJson({ current_hash: 'a', latest_hash: 'a', available: false })
      return new Response('{}', { status: 200 })
    }) as typeof globalThis.fetch
    await act(async () => { render(<DevEnvironmentSection />) })
    await waitFor(() => expect(screen.getByRole('button', { name: 'Rebuild & Restart' })).not.toBeDisabled())
    await act(async () => { fireEvent.click(screen.getByRole('button', { name: 'Rebuild & Restart' })) })
    await act(async () => { useHostStore.getState().setDevHost(null) })
    await act(async () => { d.resolve(new Response('', { status: 409 })) })
    expect(screen.queryByText('Rebuild already in progress')).toBeNull()
  })

  it('picker is disabled while a daemon rebuild is in flight', async () => {
    const d = deferred<Response>()
    globalThis.fetch = vi.fn(async (url: string | URL, init?: RequestInit) => {
      const href = String(url)
      if (href.endsWith('/api/dev/daemon/rebuild') && init?.method === 'POST') return d.promise
      if (href.endsWith('/api/dev/daemon/check')) return checkJson({ current_hash: 'a', latest_hash: 'a', available: false })
      return new Response('{}', { status: 200 })
    }) as typeof globalThis.fetch
    await act(async () => { render(<DevEnvironmentSection />) })
    await waitFor(() => expect(screen.getByRole('button', { name: 'Rebuild & Restart' })).not.toBeDisabled())
    await act(async () => { fireEvent.click(screen.getByRole('button', { name: 'Rebuild & Restart' })) })
    expect(screen.getByLabelText('Development host')).toBeDisabled()
  })

  it('A → B: a late "done" from A\'s stream does not paint B\'s view', async () => {
    arrangeStream() // hold A's stream open
    await act(async () => { render(<DevEnvironmentSection />) })
    await waitFor(() => expect(mockStreamCheck).toHaveBeenCalledTimes(1))
    const cbA = lastStreamCallback!
    const closeA = lastStreamClose
    const b = useHostStore.getState().addHost({ name: 'b', ip: '10.0.0.2', port: 7860 })
    arrangeStream() // B's stream, also held open
    await act(async () => { useHostStore.getState().setDevHost(b) })
    await waitFor(() => expect(mockStreamCheck).toHaveBeenCalledTimes(2))
    expect(closeA).toHaveBeenCalled()
    await act(async () => { cbA({ type: 'done', check: baseCheck({ spaHash: 'zzz9999' }) }) })
    expect(screen.queryByText(/zzz9999/)).toBeNull()
    expect(screen.queryByText(/Update available/)).toBeNull()
  })

  it('post-rebuild 3 s re-check is cancelled by a source change', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    const fetchMock = vi.fn(async (url: string | URL, init?: RequestInit) => {
      const href = String(url)
      if (href.endsWith('/api/dev/daemon/rebuild') && init?.method === 'POST') {
        return new Response('data: {"type":"success","new_hash":"n1"}\n\n', { status: 200 })
      }
      if (href.endsWith('/api/dev/daemon/check')) {
        return new Response(JSON.stringify({ current_hash: 'a', latest_hash: 'a', available: false }), { status: 200, headers: { 'Content-Type': 'application/json' } })
      }
      return new Response('{}', { status: 200 })
    })
    globalThis.fetch = fetchMock as typeof globalThis.fetch
    await act(async () => { render(<DevEnvironmentSection />) })
    await waitFor(() => expect(fetchMock.mock.calls.some(([u]) => String(u).endsWith('/api/dev/daemon/check'))).toBe(true))
    const checksBefore = () => fetchMock.mock.calls.filter(([u]) => String(u).endsWith('/api/dev/daemon/check')).length
    await act(async () => { fireEvent.click(screen.getByRole('button', { name: 'Rebuild & Restart' })) })
    await waitFor(() => expect(screen.getByText(/Build complete/)).toBeTruthy())
    const n = checksBefore()
    await act(async () => { useHostStore.getState().setDevHost(null) })
    await act(async () => { await vi.advanceTimersByTimeAsync(3500) })
    expect(checksBefore()).toBe(n)
  })

  it('picker is disabled while an app update is running', async () => {
    arrangeStream((cb) => {
      cb({ type: 'check', check: baseCheck({ electronHash: 'newhash' }) })
      cb({ type: 'done', check: baseCheck({ electronHash: 'newhash' }) })
    })
    mockApplyUpdate.mockReturnValue(new Promise(() => {}))
    await act(async () => { render(<DevEnvironmentSection />) })
    const update = await screen.findByRole('button', { name: 'Update App' })
    await act(async () => { fireEvent.click(update) })
    expect(screen.getByLabelText('Development host')).toBeDisabled()
  })
})
