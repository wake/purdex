// spa/src/stores/useJournalMembersStore.test.ts — the member lines' gate and read (JV-2, journal-v5 spec §4.3): nothing is asked
// without live `journal.members.v1` support; one read per leader at a time; a read that lands after the host was forgotten or the
// connection ended is dropped; a failed read keeps what is held.
import { beforeEach, describe, expect, it, vi } from 'vitest'

const getJournalMembers = vi.fn()
vi.mock('../lib/journal/api', () => ({ getJournalMembers: (...a: unknown[]) => getJournalMembers(...a) }))

import { membersKey, useJournalMembersStore } from './useJournalMembersStore'
import type { JournalMember } from '../lib/journal/types'

const M = (sessionId: string): JournalMember => ({ ref: '', sessionId, title: sessionId, task: null, last: null })
const st = () => useJournalMembersStore.getState()
const held = () => st().byLead[membersKey('h1', 'L')]
const deferred = () => { let resolve!: (v: unknown) => void; const p = new Promise((r) => { resolve = r }); return { p, resolve } }

beforeEach(() => { st().reset(); getJournalMembers.mockReset() })

describe('the gate', () => {
  it('asks nothing without live support (unknown, or a host that said no)', async () => {
    await st().load('h1', 'L')
    expect(getJournalMembers).not.toHaveBeenCalled()
    st().setSupport('h1', false)
    await st().load('h1', 'L')
    expect(getJournalMembers).not.toHaveBeenCalled()
    expect(held()).toBeUndefined()
  })
  it('reads and keeps the members once support is yes', async () => {
    st().setSupport('h1', true)
    getJournalMembers.mockResolvedValue({ kind: 'ok', value: [M('A')] })
    await st().load('h1', 'L')
    expect(getJournalMembers).toHaveBeenCalledWith('h1', 'L')
    expect(held()).toEqual([M('A')])
  })
  it('a 404 (no unit yet) holds an empty list', async () => {
    st().setSupport('h1', true)
    getJournalMembers.mockResolvedValue({ kind: 'none' })
    await st().load('h1', 'L')
    expect(held()).toEqual([])
  })
})

describe('reads', () => {
  it('single-flight per leader: a load while one is out asks nothing at once, and re-reads once when it lands', async () => {
    st().setSupport('h1', true)
    const d = deferred()
    getJournalMembers.mockReturnValueOnce(d.p)
    getJournalMembers.mockResolvedValue({ kind: 'ok', value: [M('B')] })
    const a = st().load('h1', 'L')
    void st().load('h1', 'L')
    void st().load('h1', 'L')
    expect(getJournalMembers).toHaveBeenCalledTimes(1)
    d.resolve({ kind: 'ok', value: [M('A')] })
    await a
    await vi.waitFor(() => expect(getJournalMembers).toHaveBeenCalledTimes(2)) // the asks made meanwhile became ONE re-read
    await vi.waitFor(() => expect(held()).toEqual([M('B')]))
  })
  it('a failed read keeps what is held', async () => {
    st().setSupport('h1', true)
    getJournalMembers.mockResolvedValueOnce({ kind: 'ok', value: [M('A')] })
    await st().load('h1', 'L')
    getJournalMembers.mockRejectedValueOnce(new Error('boom'))
    await st().load('h1', 'L')
    expect(held()).toEqual([M('A')])
  })
  it('a read that lands after forgetHost is dropped', async () => {
    st().setSupport('h1', true)
    const d = deferred()
    getJournalMembers.mockReturnValue(d.p)
    const a = st().load('h1', 'L')
    st().forgetHost('h1')
    d.resolve({ kind: 'ok', value: [M('A')] })
    await a
    expect(held()).toBeUndefined()
  })
  it('a read that lands after the connection ended (fence) is dropped', async () => {
    st().setSupport('h1', true)
    const d = deferred()
    getJournalMembers.mockReturnValue(d.p)
    const a = st().load('h1', 'L')
    st().fence('h1')
    d.resolve({ kind: 'ok', value: [M('A')] })
    await a
    expect(held()).toBeUndefined()
  })
  it('forgetHost drops that host\'s members and support, and leaves other hosts alone', async () => {
    st().setSupport('h1', true)
    st().setSupport('h2', true)
    getJournalMembers.mockResolvedValue({ kind: 'ok', value: [M('A')] })
    await st().load('h1', 'L')
    await st().load('h2', 'L')
    st().forgetHost('h1')
    expect(held()).toBeUndefined()
    expect(st().support.h1).toBeUndefined()
    expect(st().byLead[membersKey('h2', 'L')]).toEqual([M('A')])
  })
})

describe('JV-2d codex R5', () => {
  it('a reply from before a disconnect is dropped even after the host re-declares support (connection generation)', async () => {
    st().setSupport('h1', true)
    const d = deferred()
    getJournalMembers.mockReturnValue(d.p)
    const a = st().load('h1', 'L')
    st().fence('h1') // the connection ended ...
    st().setSupport('h1', true) // ... and a new one declared support again
    d.resolve({ kind: 'ok', value: [M('OLD')] })
    await a
    expect(held()).toBeUndefined()
  })

  it('after forgetHost the old and the new read interleave: the old one finishing does not clear the new one\'s marker', async () => {
    st().setSupport('h1', true)
    const d1 = deferred()
    const d2 = deferred()
    getJournalMembers.mockReturnValueOnce(d1.p).mockReturnValueOnce(d2.p)
    const a = st().load('h1', 'L')
    st().forgetHost('h1')
    st().setSupport('h1', true)
    const b = st().load('h1', 'L')
    expect(getJournalMembers).toHaveBeenCalledTimes(2)
    d1.resolve({ kind: 'ok', value: [M('OLD')] })
    await a
    void st().load('h1', 'L') // the new read is still out: this must not start a third in flight
    expect(getJournalMembers).toHaveBeenCalledTimes(2)
    getJournalMembers.mockResolvedValue({ kind: 'ok', value: [M('NEW2')] })
    d2.resolve({ kind: 'ok', value: [M('NEW')] })
    await b
    expect(getJournalMembers.mock.calls.length).toBeLessThanOrEqual(3) // at most the one re-read, after the new read landed
  })
})
