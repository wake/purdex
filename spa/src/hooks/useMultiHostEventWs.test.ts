// spa/src/hooks/useMultiHostEventWs.test.ts
// Locks the P0 acceptance: a host endpoint scheme change (http → https) tears
// down its existing event WS connection and reconnects over wss://.
// Lower layers (WS transport, connection state machine, health check) are
// mocked so no real network/timers are involved; hostWsUrl is kept REAL
// because it's what derives the ws/wss URL from the (mocked) host store.
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { renderHook, act, waitFor } from '@testing-library/react'
import { useHostStore } from '../stores/useHostStore'

vi.mock('../lib/host-events', () => ({
  connectHostEvents: vi.fn(),
}))

vi.mock('../lib/connection-state-machine', () => ({
  ConnectionStateMachine: vi.fn().mockImplementation(function FakeConnectionStateMachine() {
    return { trigger: vi.fn(), stop: vi.fn() }
  }),
}))

vi.mock('../lib/host-connection', () => ({
  checkHealth: vi.fn().mockResolvedValue({
    daemon: 'connected',
    tmux: 'unavailable',
    latency: 10,
    mode: 'normal',
  }),
}))

vi.mock('../lib/host-api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../lib/host-api')>()
  return {
    ...actual,
    // Keep hostWsUrl REAL — it derives the ws/wss URL from the host store,
    // which is exactly what this test asserts on.
    fetchWsTicket: vi.fn().mockResolvedValue('tk_test'),
    fetchHistory: vi.fn().mockResolvedValue([]),
  }
})

import { connectHostEvents } from '../lib/host-events'
import { useMultiHostEventWs } from './useMultiHostEventWs'

const connectHostEventsMock = vi.mocked(connectHostEvents)

describe('useMultiHostEventWs — scheme 變更觸發事件 WS 重連', () => {
  beforeEach(() => {
    connectHostEventsMock.mockClear()
    connectHostEventsMock.mockImplementation(() => ({
      close: vi.fn(),
      reconnect: vi.fn(),
      reconnectWithTicket: vi.fn(),
    }))
    useHostStore.getState().reset()
  })

  it('host scheme http→https：關閉舊連線，以 wss:// 建立新連線', async () => {
    // The default-host seed is Electron-only, so the store starts empty here;
    // seed the host this test derives the ws/wss URL from.
    const hostId = useHostStore.getState().addHost({ name: 'mlab', ip: 'example.test', port: 7860 })
    useHostStore.getState().setActiveHost(hostId)

    const { unmount } = renderHook(() => useMultiHostEventWs())

    await waitFor(() => expect(connectHostEventsMock).toHaveBeenCalledTimes(1))
    const firstUrl = connectHostEventsMock.mock.calls[0][0] as string
    expect(firstUrl.startsWith('ws://')).toBe(true)
    const firstConn = connectHostEventsMock.mock.results[0].value as { close: () => void }

    act(() => {
      useHostStore.getState().updateHost(hostId, { scheme: 'https' })
    })

    await waitFor(() => expect(connectHostEventsMock).toHaveBeenCalledTimes(2))

    // Old connection torn down …
    expect(firstConn.close).toHaveBeenCalled()
    // … and the new one is opened over wss://
    const secondUrl = connectHostEventsMock.mock.calls[1][0] as string
    expect(secondUrl.startsWith('wss://')).toBe(true)

    unmount()
  })
})
