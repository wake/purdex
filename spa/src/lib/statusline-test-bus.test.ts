import { describe, it, expect, vi, beforeEach } from 'vitest'
import { statuslineTestBus } from './statusline-test-bus'

beforeEach(() => {
  statuslineTestBus.reset()
})

describe('statuslineTestBus', () => {
  it('delivers received events to subscribers for the matching nonce', () => {
    const handler = vi.fn()
    const unsubscribe = statuslineTestBus.subscribe('__pdx_test_aaaa1111', handler)
    statuslineTestBus.emit({ nonce: '__pdx_test_aaaa1111', hostId: 'h1', raw: { x: 1 } })
    expect(handler).toHaveBeenCalledWith({ nonce: '__pdx_test_aaaa1111', hostId: 'h1', raw: { x: 1 } })
    unsubscribe()
  })

  it('does not deliver events for a different nonce', () => {
    const handler = vi.fn()
    statuslineTestBus.subscribe('__pdx_test_aaaa1111', handler)
    statuslineTestBus.emit({ nonce: '__pdx_test_bbbb2222', hostId: 'h1', raw: {} })
    expect(handler).not.toHaveBeenCalled()
  })

  it('unsubscribe stops further deliveries', () => {
    const handler = vi.fn()
    const unsubscribe = statuslineTestBus.subscribe('__pdx_test_aaaa1111', handler)
    unsubscribe()
    statuslineTestBus.emit({ nonce: '__pdx_test_aaaa1111', hostId: 'h1', raw: {} })
    expect(handler).not.toHaveBeenCalled()
  })
})
