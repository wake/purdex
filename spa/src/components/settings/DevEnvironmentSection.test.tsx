import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, waitFor, act } from '@testing-library/react'
import { DevEnvironmentSection } from './DevEnvironmentSection'

const mockGetAppInfo = vi.fn().mockResolvedValue({
  version: '1.0.0-alpha.21',
  electronHash: 'abc1234',
  spaHash: 'def5678',
  devUpdateEnabled: true,
})

const mockCheckUpdate = vi.fn()
const mockApplyUpdate = vi.fn()

beforeEach(() => {
  vi.clearAllMocks()
  window.electronAPI = {
    ...window.electronAPI!,
    getAppInfo: mockGetAppInfo,
    checkUpdate: mockCheckUpdate,
    applyUpdate: mockApplyUpdate,
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
