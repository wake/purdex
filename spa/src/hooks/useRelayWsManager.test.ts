// spa/src/hooks/useRelayWsManager.test.ts
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { useStreamStore } from '../stores/useStreamStore'
import type { StreamConnection } from '../lib/stream-ws'

const HOST = 'local'

const emptyState = {
  sessions: {},
  relayStatus: {},
  handoffProgress: {},
}

describe('useRelayWsManager store integration', () => {
  beforeEach(() => {
    useStreamStore.setState(emptyState)
  })

  it('setRelayStatus triggers store update', () => {
    useStreamStore.getState().setRelayStatus(HOST, 'test', true)
    expect(useStreamStore.getState().relayStatus[`${HOST}:test`]).toBe(true)
  })

  it('setConn stores connection for session', () => {
    const mockConn = { send: vi.fn(), close: vi.fn() } as unknown as StreamConnection
    useStreamStore.getState().setConn(HOST, 'test', mockConn)
    expect(useStreamStore.getState().sessions[`${HOST}:test`].conn).toBe(mockConn)
  })

  it('clearing relay status and conn works together', () => {
    const mockConn = { send: vi.fn(), close: vi.fn() } as unknown as StreamConnection
    useStreamStore.getState().setConn(HOST, 'test', mockConn)
    useStreamStore.getState().setRelayStatus(HOST, 'test', true)

    // Simulate disconnect
    useStreamStore.getState().setRelayStatus(HOST, 'test', false)
    useStreamStore.getState().sessions[`${HOST}:test`]?.conn?.close()
    useStreamStore.getState().setConn(HOST, 'test', null)

    expect(useStreamStore.getState().relayStatus[`${HOST}:test`]).toBe(false)
    expect(useStreamStore.getState().sessions[`${HOST}:test`].conn).toBeNull()
    expect(mockConn.close).toHaveBeenCalled()
  })

  it('subscribeWithSelector works for relayStatus changes', () => {
    const changes: Record<string, boolean>[] = []
    const unsub = useStreamStore.subscribe(
      (s) => s.relayStatus,
      (relayStatus) => { changes.push({ ...relayStatus }) },
    )

    useStreamStore.getState().setRelayStatus(HOST, 'sess-a', true)
    useStreamStore.getState().setRelayStatus(HOST, 'sess-b', false)

    expect(changes).toHaveLength(2)
    expect(changes[0]).toEqual({ [`${HOST}:sess-a`]: true })
    expect(changes[1]).toEqual({ [`${HOST}:sess-a`]: true, [`${HOST}:sess-b`]: false })

    unsub()
  })
})
