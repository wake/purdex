import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import { PaneLayoutRenderer } from './PaneLayoutRenderer'
import { registerBuiltinModules } from '../lib/register-modules'
import { clearAllBuiltinModuleRegistries, resetModuleEnabledStore } from '../lib/__tests__/test-bootstrap-harness'
import { useHostStore } from '../stores/useHostStore'
import { useModuleEnabledStore } from '../stores/useModuleEnabledStore'
import type { PaneLayout } from '../types/tab'
import { fetchMonitorConfig, fetchMonitorSnapshot } from '../lib/host-api'

vi.mock('../features/workspace/lib/icon-path-cache', () => ({
  getIconPath: () => null,
  isWeightLoaded: () => true,
  prefetchWeight: () => Promise.resolve(),
}))

vi.mock('../lib/host-api', () => ({
  fetchMonitorConfig: vi.fn(),
  fetchMonitorSnapshot: vi.fn(),
  updateMonitorConfig: vi.fn(),
}))

const memoryMonitorLayout: PaneLayout = {
  type: 'leaf',
  pane: { id: 'monitor-pane', content: { kind: 'memory-monitor' } },
}

describe('disabled Performance Monitor module', () => {
  beforeEach(() => {
    clearAllBuiltinModuleRegistries()
    vi.clearAllMocks()
    resetModuleEnabledStore()
    useHostStore.setState({
      hosts: { 'host-a': { id: 'host-a', name: 'Host A', ip: '100.64.0.1', port: 7860, order: 0 } },
      hostOrder: ['host-a'],
      activeHostId: 'host-a',
      runtime: {},
    })
    delete window.electronAPI
  })

  afterEach(() => {
    vi.useRealTimers()
    delete window.electronAPI
    clearAllBuiltinModuleRegistries()
  })

  it('does not mount monitor polling or Electron metric pulls when disabled', async () => {
    vi.useFakeTimers()
    const getProcessMetrics = vi.fn()
    window.electronAPI = { getProcessMetrics } as unknown as typeof window.electronAPI
    registerBuiltinModules()
    useModuleEnabledStore.getState().setEnabled('memory-monitor', false)

    render(<PaneLayoutRenderer layout={memoryMonitorLayout} tabId="tab-monitor" isActive />)

    expect(screen.getByRole('button', { name: /memory-monitor/i })).toBeInTheDocument()
    await Promise.resolve()
    await vi.advanceTimersByTimeAsync(60000)

    expect(fetchMonitorConfig).not.toHaveBeenCalled()
    expect(fetchMonitorSnapshot).not.toHaveBeenCalled()
    expect(getProcessMetrics).not.toHaveBeenCalled()
  })
})
