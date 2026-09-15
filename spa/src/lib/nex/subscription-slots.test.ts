// spa/src/lib/nex/subscription-slots.test.ts
import { describe, it, expect, beforeEach, vi } from 'vitest'
import { subscriptionSlots, MAX_LIVE_SUBSCRIPTIONS_PER_HOST } from './subscription-slots'

describe('subscriptionSlots', () => {
  beforeEach(() => subscriptionSlots.resetForTests())

  it('grants up to the cap per host, then evicts the least recently touched', () => {
    const keys = Array.from({ length: MAX_LIVE_SUBSCRIPTIONS_PER_HOST + 1 }, (_, i) => `h:exc_${i}`)
    const evicted = vi.fn()
    subscriptionSlots.onEvict(keys[0], evicted)
    for (const k of keys.slice(0, -1)) expect(subscriptionSlots.touch('h', k)).toEqual([])
    expect(subscriptionSlots.touch('h', keys.at(-1)!)).toEqual([keys[0]])
    expect(evicted).toHaveBeenCalledTimes(1)
    expect(subscriptionSlots.isLive('h', keys[0])).toBe(false)
    expect(subscriptionSlots.isLive('h', keys.at(-1)!)).toBe(true)
  })

  it('touching an existing key refreshes recency instead of evicting', () => {
    subscriptionSlots.touch('h', 'a'); subscriptionSlots.touch('h', 'b'); subscriptionSlots.touch('h', 'c'); subscriptionSlots.touch('h', 'd')
    subscriptionSlots.touch('h', 'a') // a is now most recent
    expect(subscriptionSlots.touch('h', 'e')).toEqual(['b'])
  })

  it('release frees a slot; hosts are independent', () => {
    for (const k of ['a', 'b', 'c', 'd']) subscriptionSlots.touch('h', k)
    subscriptionSlots.release('h', 'b')
    expect(subscriptionSlots.touch('h', 'e')).toEqual([])
    expect(subscriptionSlots.touch('other', 'x')).toEqual([])
  })

  it('claimIfFree grants a slot without eviction up to the cap, then refuses', () => {
    expect(subscriptionSlots.claimIfFree('h', 'a')).toBe(true)
    expect(subscriptionSlots.claimIfFree('h', 'b')).toBe(true)
    expect(subscriptionSlots.claimIfFree('h', 'c')).toBe(true)
    expect(subscriptionSlots.claimIfFree('h', 'd')).toBe(true)
    expect(subscriptionSlots.isLive('h', 'a')).toBe(true)
    expect(subscriptionSlots.isLive('h', 'd')).toBe(true)
    const evicted = vi.fn()
    subscriptionSlots.onEvict('a', evicted)
    expect(subscriptionSlots.claimIfFree('h', 'e')).toBe(false)
    expect(subscriptionSlots.isLive('h', 'e')).toBe(false)
    expect(subscriptionSlots.isLive('h', 'a')).toBe(true) // never evicted
    expect(evicted).not.toHaveBeenCalled()
  })

  it('claimIfFree on an already-live key refreshes recency and returns true', () => {
    for (const k of ['a', 'b', 'c', 'd']) subscriptionSlots.touch('h', k)
    expect(subscriptionSlots.claimIfFree('h', 'a')).toBe(true) // a now most recent
    expect(subscriptionSlots.touch('h', 'e')).toEqual(['b']) // b, not a, is now oldest
  })
})
