import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { ConnectionStateMachine } from './connection-state-machine'
import type { HealthResult } from './host-connection'

describe('ConnectionStateMachine', () => {
  let checkFn: ReturnType<typeof vi.fn<() => Promise<HealthResult>>>
  let onStateChange: ReturnType<typeof vi.fn<(result: HealthResult) => void>>
  let sm: ConnectionStateMachine

  beforeEach(() => {
    vi.useFakeTimers()
    checkFn = vi.fn()
    onStateChange = vi.fn()
  })

  afterEach(() => {
    sm?.stop()
    vi.useRealTimers()
  })

  it('transitions to connected on first successful check', async () => {
    checkFn.mockResolvedValue({ daemon: 'connected', tmux: 'ok', latency: 10 })
    sm = new ConnectionStateMachine(checkFn, onStateChange)
    await sm.trigger()
    expect(onStateChange).toHaveBeenCalledWith(
      expect.objectContaining({ daemon: 'connected', tmux: 'ok', latency: 10 })
    )
  })

  it('enters FAST_RETRY then L1 on 3 timeouts', async () => {
    checkFn.mockResolvedValue({ daemon: 'unreachable', tmux: 'unavailable', latency: null })
    sm = new ConnectionStateMachine(checkFn, onStateChange)
    await sm.trigger()

    expect(checkFn).toHaveBeenCalledTimes(3)
    const lastCall = onStateChange.mock.calls[onStateChange.mock.calls.length - 1][0]
    expect(lastCall.daemon).toBe('unreachable')
  })

  it('enters FAST_RETRY then L2 on 3 refused', async () => {
    checkFn.mockResolvedValue({ daemon: 'refused', tmux: 'unavailable', latency: null })
    sm = new ConnectionStateMachine(checkFn, onStateChange)
    await sm.trigger()

    expect(checkFn).toHaveBeenCalledTimes(3)
    const lastCall = onStateChange.mock.calls[onStateChange.mock.calls.length - 1][0]
    expect(lastCall.daemon).toBe('refused')
  })

  it('recovers during FAST_RETRY if second attempt succeeds', async () => {
    checkFn
      .mockResolvedValueOnce({ daemon: 'unreachable', tmux: 'unavailable', latency: null })
      .mockResolvedValueOnce({ daemon: 'connected', tmux: 'ok', latency: 5 })
    sm = new ConnectionStateMachine(checkFn, onStateChange)
    await sm.trigger()

    expect(checkFn).toHaveBeenCalledTimes(2)
    const lastCall = onStateChange.mock.calls[onStateChange.mock.calls.length - 1][0]
    expect(lastCall.daemon).toBe('connected')
  })

  it('L1 continues retrying in background', async () => {
    checkFn.mockResolvedValue({ daemon: 'unreachable', tmux: 'unavailable', latency: null })
    sm = new ConnectionStateMachine(checkFn, onStateChange)
    await sm.trigger()

    const countAfterTrigger = checkFn.mock.calls.length

    checkFn.mockResolvedValueOnce({ daemon: 'connected', tmux: 'ok', latency: 15 })
    await vi.advanceTimersByTimeAsync(3100)

    expect(checkFn.mock.calls.length).toBeGreaterThan(countAfterTrigger)
  })

  it('L2 does NOT continue retrying', async () => {
    checkFn.mockResolvedValue({ daemon: 'refused', tmux: 'unavailable', latency: null })
    sm = new ConnectionStateMachine(checkFn, onStateChange)
    await sm.trigger()

    const countAfterTrigger = checkFn.mock.calls.length
    await vi.advanceTimersByTimeAsync(10000)
    expect(checkFn.mock.calls.length).toBe(countAfterTrigger)
  })

  it('manual retry restarts FAST_RETRY for L2', async () => {
    checkFn.mockResolvedValue({ daemon: 'refused', tmux: 'unavailable', latency: null })
    sm = new ConnectionStateMachine(checkFn, onStateChange)
    await sm.trigger()

    const countBefore = checkFn.mock.calls.length
    checkFn.mockResolvedValue({ daemon: 'connected', tmux: 'ok', latency: 8 })
    await sm.trigger()

    expect(checkFn.mock.calls.length).toBeGreaterThan(countBefore)
  })

  it('uses last attempt result for classification', async () => {
    checkFn
      .mockResolvedValueOnce({ daemon: 'unreachable', tmux: 'unavailable', latency: null })
      .mockResolvedValueOnce({ daemon: 'unreachable', tmux: 'unavailable', latency: null })
      .mockResolvedValueOnce({ daemon: 'refused', tmux: 'unavailable', latency: null })
    sm = new ConnectionStateMachine(checkFn, onStateChange)
    await sm.trigger()

    const lastCall = onStateChange.mock.calls[onStateChange.mock.calls.length - 1][0]
    expect(lastCall.daemon).toBe('refused')
  })
})
