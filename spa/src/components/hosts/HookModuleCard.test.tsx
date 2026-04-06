import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, cleanup, waitFor, fireEvent, act } from '@testing-library/react'
import { HookModuleCard } from './HookModuleCard'
import { useHostStore } from '../../stores/useHostStore'
import { useAgentStore } from '../../stores/useAgentStore'
import type { HookModule, HookModuleStatus } from '../../lib/hook-modules'

const HOST_ID = 'test-host'

const OK_STATUS: HookModuleStatus = {
  installed: true,
  events: { SessionStart: { installed: true }, Stop: { installed: true } },
  issues: [],
}

const NOT_INSTALLED: HookModuleStatus = {
  installed: false,
  events: { SessionStart: { installed: false } },
  issues: ['SessionStart hook not installed'],
}

function mockModule(overrides?: Partial<HookModule>): HookModule {
  return {
    id: 'test',
    labelKey: 'hosts.agent_hooks',
    descKey: 'hosts.agent_hooks_desc',
    fetchStatus: vi.fn(() => Promise.resolve(OK_STATUS)),
    setup: vi.fn(() => Promise.resolve(OK_STATUS)),
    ...overrides,
  }
}

function waitForLoaded() {
  return waitFor(() => expect(screen.getByRole('button', { name: /Install/i })).toBeInTheDocument())
}

beforeEach(() => {
  cleanup()
  useHostStore.setState({
    hosts: { [HOST_ID]: { id: HOST_ID, name: 'mlab', ip: '1.2.3.4', port: 7860, order: 0 } },
    hostOrder: [HOST_ID],
    runtime: { [HOST_ID]: { status: 'connected' } },
  })
  useAgentStore.setState({ events: {}, statuses: {}, unread: {}, activeSubagents: {}, models: {} })
})

describe('HookModuleCard', () => {
  it('shows loading indicator on initial fetch', () => {
    const mod = mockModule({ fetchStatus: () => new Promise(() => {}) })
    render(<HookModuleCard module={mod} hostId={HOST_ID} refreshKey={0} />)
    expect(screen.getByText('Loading...')).toBeInTheDocument()
  })

  it('renders installed status badge after fetch', async () => {
    const mod = mockModule()
    render(<HookModuleCard module={mod} hostId={HOST_ID} refreshKey={0} />)
    await waitForLoaded()
    expect(screen.getAllByText('Installed').length).toBeGreaterThanOrEqual(1)
  })

  it('renders not-installed badge', async () => {
    const mod = mockModule({ fetchStatus: () => Promise.resolve(NOT_INSTALLED) })
    render(<HookModuleCard module={mod} hostId={HOST_ID} refreshKey={0} />)
    await waitForLoaded()
    expect(screen.getAllByText('Not Installed').length).toBeGreaterThanOrEqual(1)
  })

  it('renders issues when present', async () => {
    const mod = mockModule({ fetchStatus: () => Promise.resolve(NOT_INSTALLED) })
    render(<HookModuleCard module={mod} hostId={HOST_ID} refreshKey={0} />)
    await waitFor(() => expect(screen.getByText('SessionStart hook not installed')).toBeInTheDocument())
  })

  it('renders error on fetch failure', async () => {
    const mod = mockModule({ fetchStatus: () => Promise.reject(new Error('503 Unavailable')) })
    render(<HookModuleCard module={mod} hostId={HOST_ID} refreshKey={0} />)
    await waitFor(() => expect(screen.getByText('503 Unavailable')).toBeInTheDocument())
  })

  it('disables buttons when offline', async () => {
    useHostStore.setState({
      hosts: { [HOST_ID]: { id: HOST_ID, name: 'mlab', ip: '1.2.3.4', port: 7860, order: 0 } },
      hostOrder: [HOST_ID],
      runtime: { [HOST_ID]: { status: 'disconnected' } },
    })
    const mod = mockModule()
    render(<HookModuleCard module={mod} hostId={HOST_ID} refreshKey={0} />)
    await waitForLoaded()
    expect(screen.getByRole('button', { name: /Install/i })).toBeDisabled()
    expect(screen.getByRole('button', { name: /Remove/i })).toBeDisabled()
  })

  it('disables install when already installed, enables remove', async () => {
    const mod = mockModule()
    render(<HookModuleCard module={mod} hostId={HOST_ID} refreshKey={0} />)
    await waitForLoaded()
    expect(screen.getByRole('button', { name: /Install/i })).toBeDisabled()
    expect(screen.getByRole('button', { name: /Remove/i })).not.toBeDisabled()
  })

  it('calls setup on button click', async () => {
    const setupFn = vi.fn(() => Promise.resolve(NOT_INSTALLED))
    const mod = mockModule({ fetchStatus: () => Promise.resolve(OK_STATUS), setup: setupFn })
    render(<HookModuleCard module={mod} hostId={HOST_ID} refreshKey={0} />)
    await waitForLoaded()
    fireEvent.click(screen.getByRole('button', { name: /Remove/i }))
    await waitFor(() => expect(setupFn).toHaveBeenCalledWith(HOST_ID, 'remove'))
  })

  it('renders last trigger time for events with getLastTrigger', async () => {
    const now = Date.now() * 1_000_000 // nanoseconds
    const mod = mockModule({
      getLastTrigger: () => ({ SessionStart: now }),
    })
    render(<HookModuleCard module={mod} hostId={HOST_ID} refreshKey={0} />)
    await waitFor(() => expect(screen.getByText('just now')).toBeInTheDocument())
  })
})

describe('HookModuleCard lastTrigger reactivity', () => {
  it('updates trigger time when store events change', async () => {
    const now = Date.now() * 1_000_000
    // getLastTrigger reads from events parameter — returns trigger only when events match
    const mod = mockModule({
      getLastTrigger: (_hostId, events) => {
        const keys = Object.keys(events)
        if (keys.length === 0) return null
        return { SessionStart: now }
      },
    })
    render(<HookModuleCard module={mod} hostId={HOST_ID} refreshKey={0} />)
    await waitForLoaded()
    // Initially no events → no trigger time
    expect(screen.queryByText('just now')).not.toBeInTheDocument()

    // Simulate a hook event arriving
    act(() => {
      useAgentStore.setState({
        events: {
          [`${HOST_ID}:sess1`]: {
            tmux_session: 'sess1', event_name: 'SessionStart',
            raw_event: {}, agent_type: 'cc', broadcast_ts: now,
          },
        },
      })
    })
    await waitFor(() => expect(screen.getByText('just now')).toBeInTheDocument())
  })
})

describe('formatRelativeTime via HookModuleCard', () => {
  it('shows minutes ago', async () => {
    const fiveMinAgo = (Date.now() - 5 * 60 * 1000) * 1_000_000
    const mod = mockModule({
      getLastTrigger: () => ({ SessionStart: fiveMinAgo }),
    })
    render(<HookModuleCard module={mod} hostId={HOST_ID} refreshKey={0} />)
    await waitFor(() => expect(screen.getByText(/5m ago/)).toBeInTheDocument())
  })

  it('shows hours ago', async () => {
    const twoHoursAgo = (Date.now() - 2 * 60 * 60 * 1000) * 1_000_000
    const mod = mockModule({
      getLastTrigger: () => ({ SessionStart: twoHoursAgo }),
    })
    render(<HookModuleCard module={mod} hostId={HOST_ID} refreshKey={0} />)
    await waitFor(() => expect(screen.getByText(/2h ago/)).toBeInTheDocument())
  })
})
