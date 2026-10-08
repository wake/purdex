// spa/src/lib/team/unattended-aggregate.test.ts — the title-bar button's reading of every shown host (unattended spec
// D-U23-5; plan PU-2b): off / on / partial / none, and the groups the tooltip and the press use.
import { describe, it, expect } from 'vitest'
import { aggregateUnattended } from './unattended-aggregate'
import type { HostRuntime } from '../../stores/useHostStore'
import type { UnattendedHostEntry } from '../../stores/useUnattendedStore'

const ON = { on: true, since: 1_000, changed_at: 1_000 }
const OFF = { on: false, since: 0, changed_at: 0 }
const up: HostRuntime = { status: 'connected' }
const down: HostRuntime = { status: 'disconnected' }
const yes = (state: typeof ON): UnattendedHostEntry => ({ support: 'yes', state })

describe('aggregateUnattended', () => {
  it('every shown host on → on', () => {
    const agg = aggregateUnattended(['a', 'b'], { a: up, b: up }, { a: yes(ON), b: yes(ON) })
    expect(agg).toEqual({ mode: 'on', on: ['a', 'b'], off: [], unreachable: [], unsupported: [], reachable: ['a', 'b'] })
  })

  it('every shown host off → off', () => {
    const agg = aggregateUnattended(['a', 'b'], { a: up, b: up }, { a: yes(OFF), b: yes(OFF) })
    expect(agg).toEqual({ mode: 'off', on: [], off: ['a', 'b'], unreachable: [], unsupported: [], reachable: ['a', 'b'] })
  })

  it('one off among on hosts → partial, naming the off one', () => {
    const agg = aggregateUnattended(['a', 'b'], { a: up, b: up }, { a: yes(ON), b: yes(OFF) })
    expect(agg.mode).toBe('partial')
    expect(agg.on).toEqual(['a'])
    expect(agg.off).toEqual(['b'])
    expect(agg.reachable).toEqual(['a', 'b'])
  })

  it('one disconnected with the rest off → partial (it may still be on and approving)', () => {
    const agg = aggregateUnattended(['a', 'b'], { a: up, b: down }, { a: yes(OFF), b: yes(ON) })
    expect(agg.mode).toBe('partial')
    expect(agg.unreachable).toEqual(['b'])
    expect(agg.reachable).toEqual(['a'])
    expect(agg.on).toEqual([])
  })

  it('a host with no runtime yet is unreachable', () => {
    const agg = aggregateUnattended(['a', 'b'], { a: up }, { a: yes(OFF) })
    expect(agg.mode).toBe('partial')
    expect(agg.unreachable).toEqual(['b'])
  })

  it('connected but support unknown → unreachable, never reachable', () => {
    const agg = aggregateUnattended(['a', 'b'], { a: up, b: up }, { a: yes(OFF), b: { support: 'unknown', state: OFF } })
    expect(agg.mode).toBe('partial')
    expect(agg.unreachable).toEqual(['b'])
    expect(agg.reachable).toEqual(['a'])
  })

  it('connected and supported but no state yet → unreachable', () => {
    const agg = aggregateUnattended(['a', 'b'], { a: up, b: up }, { a: yes(ON), b: { support: 'yes' } })
    expect(agg.mode).toBe('partial')
    expect(agg.unreachable).toEqual(['b'])
    expect(agg.reachable).toEqual(['a'])
  })

  it('a connected host with no entry at all → unreachable', () => {
    const agg = aggregateUnattended(['a', 'b'], { a: up, b: up }, { a: yes(ON) })
    expect(agg.unreachable).toEqual(['b'])
    expect(agg.mode).toBe('partial')
  })

  it('one unsupported (daemon too old) → partial, in its own group', () => {
    const agg = aggregateUnattended(['a', 'b'], { a: up, b: up }, { a: yes(ON), b: { support: 'no' } })
    expect(agg.mode).toBe('partial')
    expect(agg.unsupported).toEqual(['b'])
    expect(agg.unreachable).toEqual([])
    expect(agg.reachable).toEqual(['a'])
  })

  it('an unsupported host that is disconnected reads unreachable (what it runs now is unknown)', () => {
    const agg = aggregateUnattended(['a', 'b'], { a: up, b: down }, { a: yes(ON), b: { support: 'no' } })
    expect(agg.unreachable).toEqual(['b'])
    expect(agg.unsupported).toEqual([])
  })

  it('no shown host → none', () => {
    const agg = aggregateUnattended([], { a: up }, { a: yes(ON) })
    expect(agg).toEqual({ mode: 'none', on: [], off: [], unreachable: [], unsupported: [], reachable: [] })
  })

  it('a host not shown that is on does not count', () => {
    const agg = aggregateUnattended(['a'], { a: up, hidden: up }, { a: yes(OFF), hidden: yes(ON) })
    expect(agg).toEqual({ mode: 'off', on: [], off: ['a'], unreachable: [], unsupported: [], reachable: ['a'] })
  })

  it('groups keep the shown order', () => {
    const agg = aggregateUnattended(['c', 'a', 'b'], { a: up, b: up, c: up }, { a: yes(OFF), b: yes(ON), c: yes(OFF) })
    expect(agg.off).toEqual(['c', 'a'])
    expect(agg.reachable).toEqual(['c', 'a', 'b'])
  })
})
