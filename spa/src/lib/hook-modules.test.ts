import { describe, it, expect, vi, beforeEach } from 'vitest'
import { useAgentStore } from '../stores/useAgentStore'

const hostFetchMock = vi.fn()

vi.mock('./host-api', () => ({
  hostFetch: (...args: unknown[]) => hostFetchMock(...args),
}))

describe('hook-modules', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    useAgentStore.setState({ lastEvents: {}, statuses: {}, unread: {}, subagents: {}, models: {} })
  })

  it('includes an opencode hook module', async () => {
    const { HOOK_MODULES } = await import('./hook-modules')
    const opencodeModule = HOOK_MODULES.find((mod) => mod.id === 'opencode')
    expect(opencodeModule).toBeTruthy()
    expect(opencodeModule?.labelKey).toBe('hosts.opencode_hooks')
    expect(opencodeModule?.descKey).toBe('hosts.opencode_hooks_desc')
  })

  it('opencode fetchStatus/setup use opencode hook routes', async () => {
    hostFetchMock.mockResolvedValue({
      ok: true,
      json: async () => ({ installed: false, events: {}, issues: [] }),
    })

    const { HOOK_MODULES } = await import('./hook-modules')
    const opencodeModule = HOOK_MODULES.find((mod) => mod.id === 'opencode')
    expect(opencodeModule).toBeTruthy()

    await opencodeModule!.fetchStatus('host-1')
    expect(hostFetchMock).toHaveBeenCalledWith('host-1', '/api/hooks/opencode/status', undefined)

    await opencodeModule!.setup('host-1', 'install')
    expect(hostFetchMock).toHaveBeenLastCalledWith('host-1', '/api/hooks/opencode/setup', expect.objectContaining({ method: 'POST' }))
  })

  it('opencode getLastTrigger filters only opencode events', async () => {
    const { HOOK_MODULES } = await import('./hook-modules')
    const opencodeModule = HOOK_MODULES.find((mod) => mod.id === 'opencode')
    expect(opencodeModule?.getLastTrigger).toBeTruthy()

    const result = opencodeModule!.getLastTrigger!('host-1', {
      'host-1:s1': {
        agent_type: 'opencode',
        status: 'idle',
        raw_event_name: 'SubagentStart',
        broadcast_ts: 10,
      },
      'host-1:s2': {
        agent_type: 'opencode',
        status: 'idle',
        raw_event_name: 'SubagentStart',
        broadcast_ts: 20,
      },
      'host-1:s3': {
        agent_type: 'cc',
        status: 'idle',
        raw_event_name: 'SubagentStart',
        broadcast_ts: 999,
      },
    })

    expect(result).toEqual({ SubagentStart: 20 })
  })
})
