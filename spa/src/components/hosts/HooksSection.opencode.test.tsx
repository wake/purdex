import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, waitFor, cleanup } from '@testing-library/react'
import { HooksSection } from './HooksSection'
import { useHostStore } from '../../stores/useHostStore'

const hostFetchMock = vi.fn()

vi.mock('../../lib/host-api', () => ({
  hostFetch: (...args: unknown[]) => hostFetchMock(...args),
}))

const HOST_ID = 'test-host'

beforeEach(() => {
  cleanup()
  vi.clearAllMocks()
  useHostStore.setState({
    hosts: { [HOST_ID]: { id: HOST_ID, name: 'mlab', ip: '1.2.3.4', port: 7860, order: 0 } },
    hostOrder: [HOST_ID],
    runtime: { [HOST_ID]: { status: 'connected' } },
  })
  hostFetchMock.mockImplementation(async (_hostId: string, path: string) => ({
    ok: true,
    status: 200,
    statusText: 'OK',
    json: async () => ({
      installed: path.includes('/opencode/') ? false : true,
      events: path.includes('/opencode/')
        ? { SessionStart: { installed: false }, SubagentStart: { installed: false } }
        : { SessionStart: { installed: true } },
      issues: [],
    }),
  }))
})

afterEach(() => {
  cleanup()
})

describe('HooksSection opencode integration', () => {
  it('renders OpenCode hook card from real HOOK_MODULES', async () => {
    render(<HooksSection hostId={HOST_ID} />)

    await waitFor(() => {
      expect(screen.getByText('OpenCode Hooks')).toBeInTheDocument()
    })

    expect(screen.getByText('OpenCode hooks configured in ~/.config/opencode/plugins for agent event monitoring')).toBeInTheDocument()
    expect(hostFetchMock).toHaveBeenCalledWith(HOST_ID, '/api/hooks/opencode/status', undefined)
  })
})
