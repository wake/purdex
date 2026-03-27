import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen } from '@testing-library/react'
import { DevEnvironmentSection } from './DevEnvironmentSection'

const mockGetAppInfo = vi.fn().mockResolvedValue({
  version: '1.0.0-alpha.21',
  electronHash: 'abc1234',
  spaHash: 'def5678',
  devUpdateEnabled: true,
})

beforeEach(() => {
  vi.clearAllMocks()
  window.electronAPI = {
    ...window.electronAPI!,
    getAppInfo: mockGetAppInfo,
  } as any
})

describe('DevEnvironmentSection', () => {
  it('renders section title', () => {
    render(<DevEnvironmentSection />)
    expect(screen.getByText(/Development|開發環境/)).toBeTruthy()
  })

  it('calls getAppInfo on mount', async () => {
    render(<DevEnvironmentSection />)
    expect(mockGetAppInfo).toHaveBeenCalledOnce()
  })
})
