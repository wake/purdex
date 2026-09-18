// spa/src/lib/nex/subscription-slots.test.ts
import { describe, it, expect, beforeEach, vi } from 'vitest'
import { subscriptionSlots, MAX_LIVE_SUBSCRIPTIONS_PER_HOST, capFor } from './subscription-slots'

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

  it('reserve drops cap 4→3 and evicts the LRU live key with its onEvict fired', () => {
    for (const k of ['a', 'b', 'c', 'd']) subscriptionSlots.touch('h', k)
    const evicted = vi.fn()
    subscriptionSlots.onEvict('a', evicted)
    expect(capFor('h')).toBe(MAX_LIVE_SUBSCRIPTIONS_PER_HOST)
    subscriptionSlots.reserve('h', 'site-wide')
    expect(capFor('h')).toBe(MAX_LIVE_SUBSCRIPTIONS_PER_HOST - 1)
    expect(evicted).toHaveBeenCalledTimes(1)
    expect(subscriptionSlots.isLive('h', 'a')).toBe(false)
    for (const k of ['b', 'c', 'd']) expect(subscriptionSlots.isLive('h', k)).toBe(true)
    expect(capFor('other')).toBe(MAX_LIVE_SUBSCRIPTIONS_PER_HOST)
  })

  it('claimIfFree refuses the 4th while reserved', () => {
    subscriptionSlots.reserve('h', 'site-wide')
    for (const k of ['a', 'b', 'c']) expect(subscriptionSlots.claimIfFree('h', k)).toBe(true)
    expect(subscriptionSlots.claimIfFree('h', 'd')).toBe(false)
    expect(subscriptionSlots.isLive('h', 'd')).toBe(false)
    expect(subscriptionSlots.touch('h', 'd')).toEqual(['a'])
  })

  it('unreserve restores 4 and a new claimIfFree succeeds', () => {
    subscriptionSlots.reserve('h', 'site-wide')
    for (const k of ['a', 'b', 'c']) subscriptionSlots.touch('h', k)
    expect(subscriptionSlots.claimIfFree('h', 'd')).toBe(false)
    subscriptionSlots.unreserve('h', 'site-wide')
    expect(capFor('h')).toBe(MAX_LIVE_SUBSCRIPTIONS_PER_HOST)
    expect(subscriptionSlots.claimIfFree('h', 'd')).toBe(true)
    expect(subscriptionSlots.isLive('h', 'd')).toBe(true)
  })

  it('double reserve counts once', () => {
    subscriptionSlots.reserve('h', 'site-wide')
    subscriptionSlots.reserve('h', 'site-wide')
    expect(capFor('h')).toBe(MAX_LIVE_SUBSCRIPTIONS_PER_HOST - 1)
    subscriptionSlots.unreserve('h', 'site-wide')
    expect(capFor('h')).toBe(MAX_LIVE_SUBSCRIPTIONS_PER_HOST)
    subscriptionSlots.unreserve('h', 'site-wide')
    expect(capFor('h')).toBe(MAX_LIVE_SUBSCRIPTIONS_PER_HOST)
  })

  it('reserving with 2 live keys evicts nothing', () => {
    subscriptionSlots.touch('h', 'a'); subscriptionSlots.touch('h', 'b')
    const evicted = vi.fn()
    subscriptionSlots.onEvict('a', evicted); subscriptionSlots.onEvict('b', evicted)
    subscriptionSlots.reserve('h', 'site-wide')
    expect(evicted).not.toHaveBeenCalled()
    expect(subscriptionSlots.isLive('h', 'a')).toBe(true)
    expect(subscriptionSlots.isLive('h', 'b')).toBe(true)
  })

  it('unknown tag throws', () => {
    expect(() => subscriptionSlots.reserve('h', 'other' as never)).toThrow()
    expect(() => subscriptionSlots.unreserve('h', 'other' as never)).toThrow()
    expect(capFor('h')).toBe(MAX_LIVE_SUBSCRIPTIONS_PER_HOST)
  })

  it('resetForTests clears reservations', () => {
    subscriptionSlots.reserve('h', 'site-wide')
    subscriptionSlots.resetForTests()
    expect(capFor('h')).toBe(MAX_LIVE_SUBSCRIPTIONS_PER_HOST)
  })
})
