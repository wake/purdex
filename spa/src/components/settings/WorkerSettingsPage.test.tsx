import { describe, it, expect, beforeEach, vi } from 'vitest'
import { useNexHostStore } from '../../stores/useNexHostStore'

vi.mock('../../lib/nex/nex-api', () => ({ listExecutions: vi.fn().mockResolvedValue({ items: [], next_cursor: '' }), attachControl: vi.fn(), terminateExecution: vi.fn(), releaseLease: vi.fn(), archiveExecution: vi.fn() }))
vi.mock('../../lib/nex/nex-sse', () => ({ openNexSse: vi.fn() }))
import { render, screen, fireEvent } from '@testing-library/react'
import { WorkerSettingsPage } from './WorkerSettingsPage'
import { clearWorkerSettingsTabs, registerWorkerSettingsTab } from '../../lib/worker-settings-tabs'
import { useShownHostsStore } from '../../stores/useShownHostsStore'
import { useHostStore } from '../../stores/useHostStore'
import { resetAndRegisterBuiltinModules } from '../../lib/__tests__/test-bootstrap-harness'

const Probe = ({ hostId }: { hostId?: string }) => <div data-testid="probe">{hostId ?? 'none'}</div>

describe('WorkerSettingsPage', () => {
  beforeEach(() => {
    resetAndRegisterBuiltinModules()
    useHostStore.setState({
      hosts: {
        a: { id: 'a', name: 'Alpha', ip: '1.1.1.1', port: 1, token: null } as never,
        b: { id: 'b', name: 'Beta', ip: '1.1.1.2', port: 1, token: null } as never,
      },
      hostOrder: ['a', 'b'],
    })
    useShownHostsStore.setState({ ids: ['a', 'b'] })
  })

  it('shows Appearance first, then a host picker on the Workers tab', () => {
    render(<WorkerSettingsPage />)
    expect(screen.getByTestId('worker-settings-tab-appearance')).toHaveAttribute('aria-selected', 'true')
    expect(screen.queryByTestId('worker-settings-host-picker')).toBeNull()
    fireEvent.click(screen.getByTestId('worker-settings-tab-workers'))
    expect(screen.getByTestId('worker-settings-tab-workers')).toHaveAttribute('aria-selected', 'true')
    expect(screen.getByTestId('worker-settings-host-picker')).toBeInTheDocument()
    expect(screen.getByText('Alpha')).toBeInTheDocument()
    expect(screen.getByText('Beta')).toBeInTheDocument()
  })

  it('passes the picked host (default first) to a host-scoped tab', () => {
    clearWorkerSettingsTabs()
    registerWorkerSettingsTab({ id: 'p', labelKey: 'p', order: 0, hostScoped: true, component: Probe })
    render(<WorkerSettingsPage />)
    expect(screen.getByTestId('probe')).toHaveTextContent('a')
    fireEvent.click(screen.getByText('Beta'))
    expect(screen.getByTestId('probe')).toHaveTextContent('b')
  })

  it('the Workers tab shows a disabled / unavailable host state instead of loading', () => {
    const base = { info: null, capabilities: null, error: null, fetchedAt: 1, generation: 1, fingerprint: 'x' }
    useNexHostStore.setState({
      byHost: { a: { ...base, phase: 'disabled' } as never, b: { ...base, phase: 'unavailable', error: 'nope' } as never },
      ensure: vi.fn().mockResolvedValue(undefined),
    })
    render(<WorkerSettingsPage />)
    fireEvent.click(screen.getByTestId('worker-settings-tab-workers'))
    expect(screen.getByTestId('worker-settings-live-disabled')).toBeInTheDocument()
    expect(screen.queryByTestId('worker-settings-live-loading')).toBeNull()
    fireEvent.click(screen.getByText('Beta'))
    expect(screen.getByTestId('worker-settings-live-unavailable')).toHaveTextContent('nope')
    expect(screen.queryByTestId('worker-settings-live-loading')).toBeNull()
  })

  it('shows the no-hosts copy when no host is shown', () => {
    clearWorkerSettingsTabs()
    registerWorkerSettingsTab({ id: 'p', labelKey: 'p', order: 0, hostScoped: true, component: Probe })
    useHostStore.setState({ hosts: {}, hostOrder: [] })
    render(<WorkerSettingsPage />)
    expect(screen.getByTestId('worker-settings-no-hosts')).toBeInTheDocument()
    expect(screen.queryByTestId('probe')).toBeNull()
  })
})
