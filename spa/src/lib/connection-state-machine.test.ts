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
    checkFn.mockResolvedValue({ daemon: 'connected', tmux: 'ok', latency: 10, mode: 'normal' })
    sm = new ConnectionStateMachine(checkFn, onStateChange)
    await sm.trigger()
    expect(onStateChange).toHaveBeenCalledWith(
      expect.objectContaining({ daemon: 'connected', tmux: 'ok', latency: 10 })
    )
  })

  it('enters FAST_RETRY then L1 on 3 timeouts', async () => {
    checkFn.mockResolvedValue({ daemon: 'unreachable', tmux: 'unavailable', latency: null, mode: 'normal' })
    sm = new ConnectionStateMachine(checkFn, onStateChange)
    await sm.trigger()

    expect(checkFn).toHaveBeenCalledTimes(3)
    const lastCall = onStateChange.mock.calls[onStateChange.mock.calls.length - 1][0]
    expect(lastCall.daemon).toBe('unreachable')
  })

  it('enters FAST_RETRY then L2 on 3 refused', async () => {
    checkFn.mockResolvedValue({ daemon: 'refused', tmux: 'unavailable', latency: null, mode: 'normal' })
    sm = new ConnectionStateMachine(checkFn, onStateChange)
    await sm.trigger()

    expect(checkFn).toHaveBeenCalledTimes(3)
    const lastCall = onStateChange.mock.calls[onStateChange.mock.calls.length - 1][0]
    expect(lastCall.daemon).toBe('refused')
  })

  it('recovers during FAST_RETRY if second attempt succeeds', async () => {
    checkFn
      .mockResolvedValueOnce({ daemon: 'unreachable', tmux: 'unavailable', latency: null, mode: 'normal' })
      .mockResolvedValueOnce({ daemon: 'connected', tmux: 'ok', latency: 5, mode: 'normal' })
    sm = new ConnectionStateMachine(checkFn, onStateChange)
    await sm.trigger()

    expect(checkFn).toHaveBeenCalledTimes(2)
    const lastCall = onStateChange.mock.calls[onStateChange.mock.calls.length - 1][0]
    expect(lastCall.daemon).toBe('connected')
  })

  it('L1 continues retrying in background', async () => {
    checkFn.mockResolvedValue({ daemon: 'unreachable', tmux: 'unavailable', latency: null, mode: 'normal' })
    sm = new ConnectionStateMachine(checkFn, onStateChange)
    await sm.trigger()

    const countAfterTrigger = checkFn.mock.calls.length

    checkFn.mockResolvedValueOnce({ daemon: 'connected', tmux: 'ok', latency: 15, mode: 'normal' })
    await vi.advanceTimersByTimeAsync(3100)

    expect(checkFn.mock.calls.length).toBeGreaterThan(countAfterTrigger)
  })

  it('L2 retries every 3s in background', async () => {
    checkFn.mockResolvedValue({ daemon: 'refused', tmux: 'unavailable', latency: null, mode: 'normal' })
    sm = new ConnectionStateMachine(checkFn, onStateChange)
    await sm.trigger()

    const countAfterTrigger = checkFn.mock.calls.length // 3 (FAST_RETRY)

    // After 3s, should have retried once
    await vi.advanceTimersByTimeAsync(3100)
    expect(checkFn.mock.calls.length).toBe(countAfterTrigger + 1)

    // After another 3s, should have retried again
    await vi.advanceTimersByTimeAsync(3100)
    expect(checkFn.mock.calls.length).toBe(countAfterTrigger + 2)
  })

  it('L2 stops retrying after 3 minutes', async () => {
    checkFn.mockResolvedValue({ daemon: 'refused', tmux: 'unavailable', latency: null, mode: 'normal' })
    sm = new ConnectionStateMachine(checkFn, onStateChange)
    await sm.trigger()

    // Advance past 3 minute deadline
    await vi.advanceTimersByTimeAsync(181_000)
    const countAfterDeadline = checkFn.mock.calls.length

    // No more retries after deadline
    await vi.advanceTimersByTimeAsync(10_000)
    expect(checkFn.mock.calls.length).toBe(countAfterDeadline)
  })

  it('manual retry restarts FAST_RETRY for L2', async () => {
    checkFn.mockResolvedValue({ daemon: 'refused', tmux: 'unavailable', latency: null, mode: 'normal' })
    sm = new ConnectionStateMachine(checkFn, onStateChange)
    await sm.trigger()

    const countBefore = checkFn.mock.calls.length
    checkFn.mockResolvedValue({ daemon: 'connected', tmux: 'ok', latency: 8, mode: 'normal' })
    await sm.trigger()

    expect(checkFn.mock.calls.length).toBeGreaterThan(countBefore)
  })

  it('uses last attempt result for classification', async () => {
    checkFn
      .mockResolvedValueOnce({ daemon: 'unreachable', tmux: 'unavailable', latency: null, mode: 'normal' })
      .mockResolvedValueOnce({ daemon: 'unreachable', tmux: 'unavailable', latency: null, mode: 'normal' })
      .mockResolvedValueOnce({ daemon: 'refused', tmux: 'unavailable', latency: null, mode: 'normal' })
    sm = new ConnectionStateMachine(checkFn, onStateChange)
    await sm.trigger()

    const lastCall = onStateChange.mock.calls[onStateChange.mock.calls.length - 1][0]
    expect(lastCall.daemon).toBe('refused')
  })

  it('auth-error exits FAST_RETRY on first attempt', async () => {
    checkFn.mockResolvedValue({ daemon: 'auth-error', tmux: 'unavailable', latency: 5, mode: 'normal' })
    sm = new ConnectionStateMachine(checkFn, onStateChange)
    await sm.trigger()
    expect(checkFn).toHaveBeenCalledTimes(1) // not 3
    expect(onStateChange).toHaveBeenCalledWith(
      expect.objectContaining({ daemon: 'auth-error' })
    )
  })

  it('auth-error does not start background retry', async () => {
    checkFn.mockResolvedValue({ daemon: 'auth-error', tmux: 'unavailable', latency: 5, mode: 'normal' })
    sm = new ConnectionStateMachine(checkFn, onStateChange)
    await sm.trigger()
    const countAfter = checkFn.mock.calls.length
    await vi.advanceTimersByTimeAsync(10_000)
    expect(checkFn.mock.calls.length).toBe(countAfter) // no background retries
  })

  it('background retry stops on auth-error after initial unreachable', async () => {
    // Start as unreachable → enters L1 background
    checkFn.mockResolvedValue({ daemon: 'unreachable', tmux: 'unavailable', latency: null, mode: 'normal' })
    sm = new ConnectionStateMachine(checkFn, onStateChange)
    await sm.trigger()
    const countAfterTrigger = checkFn.mock.calls.length

    // Background retry returns auth-error → should stop
    checkFn.mockResolvedValue({ daemon: 'auth-error', tmux: 'unavailable', latency: 5, mode: 'normal' })
    await vi.advanceTimersByTimeAsync(200) // L1 delay is 100ms
    const countAfterAuthError = checkFn.mock.calls.length
    expect(countAfterAuthError).toBe(countAfterTrigger + 1)

    // Verify no more retries after auth-error
    await vi.advanceTimersByTimeAsync(5000)
    expect(checkFn.mock.calls.length).toBe(countAfterAuthError)
    // Verify auth-error was reported
    const lastCall = onStateChange.mock.calls[onStateChange.mock.calls.length - 1][0]
    expect(lastCall.daemon).toBe('auth-error')
  })

  it('manual trigger recovers from auth-error', async () => {
    checkFn.mockResolvedValue({ daemon: 'auth-error', tmux: 'unavailable', latency: 5, mode: 'normal' })
    sm = new ConnectionStateMachine(checkFn, onStateChange)
    await sm.trigger()
    checkFn.mockResolvedValue({ daemon: 'connected', tmux: 'ok', latency: 3, mode: 'normal' })
    await sm.trigger()
    const lastCall = onStateChange.mock.calls[onStateChange.mock.calls.length - 1][0]
    expect(lastCall.daemon).toBe('connected')
  })
})
