// spa/src/hooks/useRelayWsManager.hook.test.ts
// Separate from store-level integration tests to allow module-level mocks.
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { renderHook, act } from '@testing-library/react'
import { useStreamStore } from '../stores/useStreamStore'
import { useHostStore } from '../stores/useHostStore'

// Must mock before import of the hook
vi.mock('../lib/host-api', () => ({
  fetchWsTicket: vi.fn(),
}))
vi.mock('../lib/stream-ws', () => ({
  connectStream: vi.fn(() => ({ send: vi.fn(), close: vi.fn() })),
}))

import { fetchWsTicket } from '../lib/host-api'
import { connectStream } from '../lib/stream-ws'
import { useRelayWsManager } from './useRelayWsManager'

describe('useRelayWsManager cancelled flag', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    useStreamStore.setState({ sessions: {}, relayStatus: {}, handoffProgress: {} })
    // Ensure getWsBase returns a valid wsBase for any hostId
    vi.spyOn(useHostStore.getState(), 'getWsBase').mockImplementation(
      () => 'ws://localhost:7860',
    )
  })

  it('does not create WS if unmount runs before fetchWsTicket resolves', async () => {
    let resolveTicket!: (v: string) => void
    const ticketPromise = new Promise<string>((r) => {
      resolveTicket = r
    })
    vi.mocked(fetchWsTicket).mockReturnValue(ticketPromise)

    const { unmount } = renderHook(() => useRelayWsManager())

    // Trigger relay connect — this starts the fetchWsTicket call
    act(() => {
      useStreamStore.getState().setRelayStatus('local', 'sess1', true)
    })

    expect(fetchWsTicket).toHaveBeenCalledOnce()

    // Unmount BEFORE the ticket resolves
    unmount()

    // Now resolve the ticket after unmount
    resolveTicket('test-ticket')
    await ticketPromise

    // Flush microtasks so the .then() callback has a chance to run
    await new Promise((r) => setTimeout(r, 0))

    // connectStream should NOT have been called — cancelled flag prevents it
    expect(connectStream).not.toHaveBeenCalled()
  })
})
