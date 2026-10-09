// spa/src/lib/team/relay-quota-writer.test.ts — the stepper's write path (plan RQ-A Task 4): the desired value is set at
// once, clicks coalesce (300 ms, last wins), at most one PUT is in flight per (host, root, field), a click during the
// flight is sent after it, a failure falls back to the confirmed value with a toast, and `pending_lineage` falls back,
// toasts and re-reads the host 5 s later.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { ApprovalApiError } from './approval-api'
import { useRelayQuotaStore, shownValue } from './relay-quota'
import { setQuota, configureWriter, resetWriter, DEBOUNCE_MS, LINEAGE_REFETCH_MS } from './relay-quota-writer'
import type { RelayQuotaField, RelayQuotaView } from './types'

const H = 'h1'
const ROOT = 'r1'
const target = (over: Partial<{ hostId: string; sessionId: string; root: string; label: string }> = {}) =>
  ({ hostId: H, sessionId: 's1', root: ROOT, label: 'Lead A', ...over })
const answer = (over: Partial<RelayQuotaView> = {}): RelayQuotaView =>
  ({ session_id: 's1', root_session_id: ROOT, self_left: 0, member_pool_left: 0, rev: 1, updated_at: 1, ...over })

interface Call { hostId: string; sessionId: string; field: RelayQuotaField; value: number; resolve: (v: RelayQuotaView) => void; reject: (e: unknown) => void }
let calls: Call[]
let toasts: string[]
let refetches: string[]
let identity: string | null = 'ep1:tok'

const st = () => useRelayQuotaStore.getState()
const shown = (field: RelayQuotaField, fallback = 3) => shownValue(st(), H, ROOT, field, fallback)

beforeEach(() => {
  vi.useFakeTimers()
  st().reset()
  calls = []
  toasts = []
  refetches = []
  identity = 'ep1:tok'
  resetWriter()
  configureWriter({
    put: (hostId, sessionId, field, value) => new Promise<RelayQuotaView>((resolve, reject) => { calls.push({ hostId, sessionId, field, value, resolve, reject }) }),
    toast: (m) => { toasts.push(m) },
    message: (key, params) => `${key}|${JSON.stringify(params)}`,
    hostLabel: (id) => `label:${id}`,
    refetch: (id) => { refetches.push(id) },
    identity: () => identity,
  })
})
afterEach(() => vi.useRealTimers())

describe('coalescing', () => {
  it('shows the desired value at once and sends nothing before the debounce', () => {
    setQuota(target(), 'self_left', 4)
    expect(shown('self_left')).toBe(4)
    vi.advanceTimersByTime(DEBOUNCE_MS - 1)
    expect(calls).toHaveLength(0)
  })

  it('5 clicks → 1 PUT of the last value, carrying only that field', async () => {
    for (const v of [4, 5, 6, 7, 8]) { setQuota(target(), 'self_left', v); vi.advanceTimersByTime(50) }
    vi.advanceTimersByTime(DEBOUNCE_MS)
    expect(calls).toHaveLength(1)
    expect(calls[0]).toMatchObject({ hostId: H, sessionId: 's1', field: 'self_left', value: 8 })
  })

  it('clicks on two rows of the same root share one queue (one PUT, the last row\'s session)', () => {
    setQuota(target({ sessionId: 'a', label: 'A' }), 'self_left', 4)
    setQuota(target({ sessionId: 'b', label: 'B' }), 'self_left', 5)
    vi.advanceTimersByTime(DEBOUNCE_MS)
    expect(calls).toHaveLength(1)
    expect(calls[0]).toMatchObject({ sessionId: 'b', value: 5 })
  })

  it('the two fields of a root, and two roots, are separate queues', () => {
    setQuota(target(), 'self_left', 4)
    setQuota(target(), 'member_pool_left', 2)
    setQuota(target({ root: 'r2', sessionId: 's2' }), 'self_left', 9)
    vi.advanceTimersByTime(DEBOUNCE_MS)
    expect(calls.map((c) => `${c.sessionId}:${c.field}:${c.value}`).sort()).toEqual(['s1:member_pool_left:2', 's1:self_left:4', 's2:self_left:9'])
  })

  it('clamps at 0 and 99 and keeps integers', () => {
    setQuota(target(), 'self_left', -3)
    expect(shown('self_left')).toBe(0)
    setQuota(target(), 'self_left', 250)
    expect(shown('self_left')).toBe(99)
    setQuota(target(), 'self_left', 4.7)
    expect(shown('self_left')).toBe(4)
  })
})

describe('one write in flight per field', () => {
  it('a click during the flight is sent after it settles, not beside it', async () => {
    setQuota(target(), 'self_left', 4)
    vi.advanceTimersByTime(DEBOUNCE_MS)
    expect(calls).toHaveLength(1)
    setQuota(target(), 'self_left', 6)
    vi.advanceTimersByTime(DEBOUNCE_MS * 2)
    expect(calls).toHaveLength(1) // still only the first in flight
    expect(shown('self_left')).toBe(6)
    calls[0].resolve(answer({ self_left: 4, rev: 2 }))
    await vi.advanceTimersByTimeAsync(0)
    expect(calls).toHaveLength(2)
    expect(calls[1]).toMatchObject({ field: 'self_left', value: 6 })
    expect(shown('self_left')).toBe(6) // the confirmed 4 never flashes over the click
    calls[1].resolve(answer({ self_left: 6, rev: 3 }))
    await vi.advanceTimersByTimeAsync(0)
    expect(shown('self_left')).toBe(6)
    expect(st().writes).toEqual({})
  })

  it('a click of the value already in flight is not sent again', async () => {
    setQuota(target(), 'self_left', 4)
    vi.advanceTimersByTime(DEBOUNCE_MS)
    setQuota(target(), 'self_left', 4)
    vi.advanceTimersByTime(DEBOUNCE_MS)
    calls[0].resolve(answer({ self_left: 4, rev: 2 }))
    await vi.advanceTimersByTimeAsync(0)
    expect(calls).toHaveLength(1)
    expect(st().writes).toEqual({})
  })

  it('an answer settles to the confirmed numbers (by rev) and clears the write', async () => {
    setQuota(target(), 'self_left', 4)
    vi.advanceTimersByTime(DEBOUNCE_MS)
    calls[0].resolve(answer({ self_left: 4, member_pool_left: 5, rev: 7 }))
    await vi.advanceTimersByTimeAsync(0)
    expect(st().confirmed[`${H}\u0000${ROOT}`]).toEqual({ self_left: 4, member_pool_left: 5, rev: 7 })
    expect(shown('self_left')).toBe(4)
    expect(shown('member_pool_left')).toBe(5)
  })

  it('an answer for a host removed meanwhile is dropped', async () => {
    setQuota(target(), 'self_left', 4)
    vi.advanceTimersByTime(DEBOUNCE_MS)
    identity = null
    st().forgetHost(H)
    calls[0].resolve(answer({ self_left: 4, rev: 2 }))
    await vi.advanceTimersByTimeAsync(0)
    expect(st().confirmed).toEqual({})
    expect(toasts).toEqual([])
  })
})

// codex R1: a host re-pointed (same id, another endpoint or token) is another daemon. What was started against the old one
// must not be applied to, resent to, or re-read from the new one.
describe('a host re-pointed meanwhile', () => {
  it('its answer is dropped (not applied to the new daemon\'s numbers) and nothing is toasted', async () => {
    setQuota(target(), 'self_left', 4)
    vi.advanceTimersByTime(DEBOUNCE_MS)
    identity = 'ep2:tok' // re-pointed while the PUT is out
    calls[0].resolve(answer({ self_left: 4, rev: 9 }))
    await vi.advanceTimersByTimeAsync(0)
    expect(st().confirmed).toEqual({})
    expect(st().writes).toEqual({})
    expect(toasts).toEqual([])
  })

  it('a click made during the flight is not resent to the new daemon', async () => {
    setQuota(target(), 'self_left', 4)
    vi.advanceTimersByTime(DEBOUNCE_MS)
    setQuota(target(), 'self_left', 6)
    identity = 'ep2:tok'
    calls[0].resolve(answer({ self_left: 4, rev: 9 }))
    await vi.advanceTimersByTimeAsync(DEBOUNCE_MS * 2)
    expect(calls).toHaveLength(1)
    expect(st().writes).toEqual({})
  })

  it('a pending write is not sent at all once the host was re-pointed before the debounce ended', () => {
    setQuota(target(), 'self_left', 4)
    identity = 'ep2:tok'
    vi.advanceTimersByTime(DEBOUNCE_MS)
    expect(calls).toHaveLength(0)
    expect(st().writes).toEqual({})
  })

  it('the delayed re-read after pending_lineage is not made against the new endpoint', async () => {
    setQuota(target(), 'self_left', 7)
    vi.advanceTimersByTime(DEBOUNCE_MS)
    calls[0].resolve(answer({ pending_lineage: true }))
    await vi.advanceTimersByTimeAsync(0)
    identity = 'ep2:tok'
    await vi.advanceTimersByTimeAsync(LINEAGE_REFETCH_MS)
    expect(refetches).toEqual([])
  })
})

describe('slot identity (codex attack)', () => {
  it('a click after the host was re-pointed starts a fresh slot for the new daemon (the old slot does not eat it)', async () => {
    setQuota(target(), 'self_left', 4)
    identity = 'ep2:tok' // re-pointed during the debounce
    st().forgetHost(H) // what unattended-support does on the same host change
    setQuota(target(), 'self_left', 6) // the person clicks again, against the new daemon
    vi.advanceTimersByTime(DEBOUNCE_MS)
    expect(calls).toHaveLength(1)
    expect(calls[0]).toMatchObject({ value: 6 })
    calls[0].resolve(answer({ self_left: 6, rev: 2 }))
    await vi.advanceTimersByTimeAsync(0)
    expect(shown('self_left')).toBe(6)
    expect(st().writes).toEqual({})
  })

  it('the late answer of a PUT sent to the old daemon does not touch the new slot\'s write', async () => {
    setQuota(target(), 'self_left', 4)
    vi.advanceTimersByTime(DEBOUNCE_MS) // PUT(4) to the old daemon is out
    identity = 'ep2:tok'
    st().forgetHost(H)
    setQuota(target(), 'self_left', 6) // new slot, new daemon
    calls[0].resolve(answer({ self_left: 4, rev: 9 })) // the old daemon answers late
    await vi.advanceTimersByTimeAsync(0)
    expect(shown('self_left')).toBe(6) // the new intent is still on screen
    expect(st().writes).not.toEqual({})
    vi.advanceTimersByTime(DEBOUNCE_MS)
    expect(calls).toHaveLength(2)
    expect(calls[1]).toMatchObject({ value: 6 })
  })
})

describe('failure', () => {
  it('drops the desired value (the stepper falls back to the confirmed one) and toasts with the code', async () => {
    st().applyAnswer(H, ROOT, { self_left: 2, member_pool_left: 0 }, 1)
    setQuota(target(), 'self_left', 7)
    vi.advanceTimersByTime(DEBOUNCE_MS)
    calls[0].reject(new ApprovalApiError(400, 'bad_request'))
    await vi.advanceTimersByTimeAsync(0)
    expect(shown('self_left')).toBe(2)
    expect(st().writes).toEqual({})
    expect(toasts).toHaveLength(1)
    expect(toasts[0]).toContain('unattended.quota.save_failed')
    expect(toasts[0]).toContain('"code":"bad_request"')
    expect(toasts[0]).toContain('"host":"label:h1"')
    expect(toasts[0]).toContain('"session":"Lead A"')
  })

  it('a click made during the flight survives the failure of the earlier PUT and is sent (codex attack: a later intent is not the failed request\'s)', async () => {
    setQuota(target(), 'self_left', 7)
    vi.advanceTimersByTime(DEBOUNCE_MS)
    setQuota(target(), 'self_left', 8)
    calls[0].reject(new ApprovalApiError(0, 'network'))
    await vi.advanceTimersByTimeAsync(0)
    expect(toasts).toHaveLength(1) // the failed request is reported
    expect(shown('self_left')).toBe(8) // the newer value stays on screen
    expect(calls).toHaveLength(2)
    expect(calls[1]).toMatchObject({ value: 8 })
    calls[1].resolve(answer({ self_left: 8, rev: 2 }))
    await vi.advanceTimersByTimeAsync(0)
    expect(shown('self_left')).toBe(8)
    expect(st().writes).toEqual({})
  })

  it('a failure with no newer click falls back to the confirmed value as before', async () => {
    st().applyAnswer(H, ROOT, { self_left: 2, member_pool_left: 0 }, 1)
    setQuota(target(), 'self_left', 7)
    vi.advanceTimersByTime(DEBOUNCE_MS)
    calls[0].reject(new ApprovalApiError(0, 'network'))
    await vi.advanceTimersByTimeAsync(DEBOUNCE_MS * 2)
    expect(calls).toHaveLength(1)
    expect(shown('self_left')).toBe(2)
  })

  it('a non-API error is code "error"', async () => {
    setQuota(target(), 'self_left', 7)
    vi.advanceTimersByTime(DEBOUNCE_MS)
    calls[0].reject(new Error('boom'))
    await vi.advanceTimersByTimeAsync(0)
    expect(toasts[0]).toContain('"code":"error"')
  })
})

describe('pending_lineage', () => {
  it('falls back, toasts, does not take the provisional numbers, and re-reads the host after 5 s', async () => {
    st().applyAnswer(H, ROOT, { self_left: 2, member_pool_left: 0 }, 1)
    setQuota(target(), 'self_left', 7)
    vi.advanceTimersByTime(DEBOUNCE_MS)
    calls[0].resolve(answer({ self_left: 7, rev: 2, pending_lineage: true, root_session_id: 'provisional' }))
    await vi.advanceTimersByTimeAsync(0)
    expect(shown('self_left')).toBe(2)
    expect(st().confirmed[`${H}\u0000provisional`]).toBeUndefined()
    expect(toasts).toHaveLength(1)
    expect(toasts[0]).toContain('unattended.quota.pending_lineage')
    expect(toasts[0]).toContain('"session":"Lead A"')
    await vi.advanceTimersByTimeAsync(LINEAGE_REFETCH_MS - 1)
    expect(refetches).toEqual([])
    await vi.advanceTimersByTimeAsync(1)
    expect(refetches).toEqual([H])
  })

  it('the re-read is cancelled when the host is gone by then', async () => {
    setQuota(target(), 'self_left', 7)
    vi.advanceTimersByTime(DEBOUNCE_MS)
    calls[0].resolve(answer({ pending_lineage: true }))
    await vi.advanceTimersByTimeAsync(0)
    identity = null
    await vi.advanceTimersByTimeAsync(LINEAGE_REFETCH_MS)
    expect(refetches).toEqual([])
  })
})
