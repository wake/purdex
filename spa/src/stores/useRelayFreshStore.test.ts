// spa/src/stores/useRelayFreshStore.test.ts — whole-list replacement and the event-versus-GET generation.
import { describe, it, expect, beforeEach } from 'vitest'
import { useRelayFreshStore } from './useRelayFreshStore'
import type { RelayFresh } from '../lib/relay/fresh'

const e = (id: string): RelayFresh => ({ session_id: id, ref: '_aaaaaa', op_id: `op-${id}`, path: `/p/${id}`, written_at: 1 })
const st = () => useRelayFreshStore.getState()
const ids = (host: string) => (st().byHost[host] ?? []).map((s) => s.session_id)

beforeEach(() => st().reset())

describe('useRelayFreshStore', () => {
  it('apply replaces the host\'s list whole (a changed is not a delta)', () => {
    st().apply('h1', [e('a'), e('b')])
    st().apply('h1', [e('c')])
    expect(ids('h1')).toEqual(['c'])
    st().apply('h1', [])
    expect(ids('h1')).toEqual([])
    expect(Object.hasOwn(st().byHost, 'h1')).toBe(true)
  })

  it('hosts are independent', () => {
    st().apply('h1', [e('a')])
    st().apply('h2', [e('b')])
    st().apply('h1', [])
    expect(ids('h2')).toEqual(['b'])
  })

  it('a GET answer is applied while no event has come since it was asked', () => {
    const gen = st().begin('h1')
    expect(st().applyFetched('h1', gen, [e('a')])).toBe(true)
    expect(ids('h1')).toEqual(['a'])
  })

  it('an event that arrives while a GET is out wins over the GET\'s older answer', () => {
    const gen = st().begin('h1')
    st().apply('h1', [e('new')])
    expect(st().applyFetched('h1', gen, [e('old')])).toBe(false)
    expect(ids('h1')).toEqual(['new'])
  })

  it('an event that arrived BEFORE the GET was asked does not block its (newer) answer', () => {
    st().apply('h1', [e('old')])
    const gen = st().begin('h1')
    expect(st().applyFetched('h1', gen, [e('new')])).toBe(true)
    expect(ids('h1')).toEqual(['new'])
  })

  it('of two GETs the later one wins, whatever the order they answer in', () => {
    const first = st().begin('h1')
    const second = st().begin('h1')
    expect(st().applyFetched('h1', second, [e('second')])).toBe(true)
    expect(st().applyFetched('h1', first, [e('first')])).toBe(false)
    expect(ids('h1')).toEqual(['second'])
  })

  it('forgetHost drops the list and outdates a GET still out; the generation is not reused by a re-added host', () => {
    st().apply('h1', [e('a')])
    const gen = st().begin('h1')
    st().forgetHost('h1')
    expect(Object.hasOwn(st().byHost, 'h1')).toBe(false)
    expect(st().applyFetched('h1', gen, [e('stale')])).toBe(false)
    expect(Object.hasOwn(st().byHost, 'h1')).toBe(false)
    expect(st().begin('h1')).toBeGreaterThan(gen)
  })

  it('forgetting a host that holds nothing still outdates its GET', () => {
    const gen = st().begin('h9')
    st().forgetHost('h9')
    expect(st().applyFetched('h9', gen, [e('x')])).toBe(false)
  })

  it('reset clears everything', () => {
    st().apply('h1', [e('a')])
    st().reset()
    expect(st().byHost).toEqual({})
  })
})
