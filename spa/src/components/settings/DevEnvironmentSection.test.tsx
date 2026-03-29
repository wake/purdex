import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, waitFor, act, fireEvent } from '@testing-library/react'
import { DevEnvironmentSection } from './DevEnvironmentSection'

const mockGetAppInfo = vi.fn().mockResolvedValue({
  version: '1.0.0-alpha.21',
  electronHash: 'abc1234',
  spaHash: 'def5678',
  devUpdateEnabled: true,
})

const mockCheckUpdate = vi.fn()
const mockApplyUpdate = vi.fn()
const mockForceLoadSPA = vi.fn().mockResolvedValue(undefined)

beforeEach(() => {
  vi.clearAllMocks()
  window.electronAPI = {
    ...window.electronAPI!,
    getAppInfo: mockGetAppInfo,
    checkUpdate: mockCheckUpdate,
    applyUpdate: mockApplyUpdate,
    forceLoadSPA: mockForceLoadSPA,
  } as any
})

afterEach(() => {
  vi.useRealTimers()
})

describe('DevEnvironmentSection', () => {
  it('renders section title', async () => {
    mockCheckUpdate.mockResolvedValue({
      version: '1.0.0-alpha.21',
      spaHash: 'def5678',
      electronHash: 'abc1234',
      source: { spaHash: 'src111', electronHash: 'src222' },
      building: false,
      buildError: '',
    })

    render(<DevEnvironmentSection />)
    expect(screen.getByText(/Development|開發環境/)).toBeTruthy()
  })

  it('calls getAppInfo on mount', async () => {
    mockCheckUpdate.mockResolvedValue({
      version: '1.0.0-alpha.21',
      spaHash: 'def5678',
      electronHash: 'abc1234',
      source: { spaHash: 'src111', electronHash: 'src222' },
      building: false,
      buildError: '',
    })

    render(<DevEnvironmentSection />)
    expect(mockGetAppInfo).toHaveBeenCalledOnce()
  })

  it('shows building status and polls', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })

    // First call: building in progress
    mockCheckUpdate.mockResolvedValueOnce({
      version: '1.0.0-alpha.21',
      spaHash: 'def5678',
      electronHash: 'abc1234',
      source: { spaHash: 'src111', electronHash: 'src222' },
      building: true,
      buildError: '',
    })
    // Second call (poll): build done, new hashes
    mockCheckUpdate.mockResolvedValueOnce({
      version: '1.0.0-alpha.21',
      spaHash: 'new5678',
      electronHash: 'newabc1',
      source: { spaHash: 'src333', electronHash: 'src444' },
      building: false,
      buildError: '',
    })

    await act(async () => {
      render(<DevEnvironmentSection />)
    })

    // Wait for initial check to complete and show building status
    await waitFor(() => {
      expect(screen.getByText(/Building|建置中/)).toBeTruthy()
    })

    // Advance timer to trigger poll
    await act(async () => {
      await vi.advanceTimersByTimeAsync(3000)
    })

    // After poll, building is done — should show update_available
    await waitFor(() => {
      expect(screen.getByText(/Update available|有新版本/)).toBeTruthy()
    })
  })

  describe('SPA source mode', () => {
    const upToDateRemote = {
      version: '1.0.0-alpha.21',
      spaHash: 'def5678',
      electronHash: 'abc1234',
      source: { spaHash: 'src111', electronHash: 'src222' },
      building: false,
      buildError: '',
    }

    it('shows "Dev Server" when loaded from http: protocol', async () => {
      mockCheckUpdate.mockResolvedValue(upToDateRemote)
      // jsdom default is http://localhost — which means dev server
      await act(async () => {
        render(<DevEnvironmentSection />)
      })
      await waitFor(() => {
        expect(screen.getByText('Dev Server')).toBeTruthy()
      })
    })

    it('shows "Bundled" when loaded from app: protocol', async () => {
      mockCheckUpdate.mockResolvedValue(upToDateRemote)
      // Simulate app:// protocol by overriding location.protocol
      const originalProtocol = window.location.protocol
      Object.defineProperty(window, 'location', {
        value: { ...window.location, protocol: 'app:' },
        writable: true,
      })
      try {
        await act(async () => {
          render(<DevEnvironmentSection />)
        })
        await waitFor(() => {
          expect(screen.getByText('Bundled')).toBeTruthy()
        })
      } finally {
        Object.defineProperty(window, 'location', {
          value: { ...window.location, protocol: originalProtocol },
          writable: true,
        })
      }
    })

    it('shows switch button and calls forceLoadSPA("bundled") from dev mode', async () => {
      mockCheckUpdate.mockResolvedValue(upToDateRemote)
      // Default is http: → dev server, so button should offer "Switch to Bundled"
      await act(async () => {
        render(<DevEnvironmentSection />)
      })
      await waitFor(() => {
        expect(screen.getByText('Dev Server')).toBeTruthy()
      })
      const switchBtn = screen.getByRole('button', { name: /Bundled/i })
      fireEvent.click(switchBtn)
      expect(mockForceLoadSPA).toHaveBeenCalledWith('bundled')
    })

    it('shows switch button and calls forceLoadSPA("dev") from bundled mode', async () => {
      mockCheckUpdate.mockResolvedValue(upToDateRemote)
      const originalProtocol = window.location.protocol
      Object.defineProperty(window, 'location', {
        value: { ...window.location, protocol: 'app:' },
        writable: true,
      })
      try {
        await act(async () => {
          render(<DevEnvironmentSection />)
        })
        await waitFor(() => {
          expect(screen.getByText('Bundled')).toBeTruthy()
        })
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

    it('shows error when forceLoadSPA rejects', async () => {
      mockCheckUpdate.mockResolvedValue(upToDateRemote)
      mockForceLoadSPA.mockRejectedValueOnce(new Error('ERR_CONNECTION_REFUSED'))
      await act(async () => {
        render(<DevEnvironmentSection />)
      })
      await waitFor(() => {
        expect(screen.getByText('Dev Server')).toBeTruthy()
      })
      const switchBtn = screen.getByRole('button', { name: /Bundled/i })
      await act(async () => {
        fireEvent.click(switchBtn)
      })
      await waitFor(() => {
        expect(screen.getByText(/Failed to load bundled SPA/)).toBeTruthy()
      })
    })
  })

  it('shows build error', async () => {
    mockCheckUpdate.mockResolvedValue({
      version: '1.0.0-alpha.21',
      spaHash: 'def5678',
      electronHash: 'abc1234',
      source: { spaHash: 'src111', electronHash: 'src222' },
      building: false,
      buildError: 'exit code 1',
    })

    await act(async () => {
      render(<DevEnvironmentSection />)
    })

    await waitFor(() => {
      expect(screen.getByText('exit code 1')).toBeTruthy()
    })
  })
})
