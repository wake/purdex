// spa/src/lib/pool/daemons.test.ts — which App host is the daemon of which pool host (monitoring tabs MON-1a). The key is the wwd `host_id`, which is never a Purdex daemon's host id.
import { describe, it, expect, beforeEach, vi } from 'vitest'
import { poolHostDaemons, poolHostOwners, readPoolHostDaemons, refreshPoolIdentities } from './daemons'
import { useHostStore } from '../../stores/useHostStore'
import { useNexHostStore, type NexHostEntry } from '../../stores/useNexHostStore'

const WWD_A = '82b11c521df16c12'
const WWD_B = '0123456789abcdef'

const connected = { status: 'connected' } as const
const entry = (over: Partial<NexHostEntry> = {}): NexHostEntry => ({
  info: null, capabilities: null, phase: 'unavailable', error: null, fetchedAt: 1000, generation: 1, fingerprint: 'x',
  daemonCapabilities: ['pool.v1'], wwd: null, ...over,
})

describe('poolHostDaemons', () => {
  const run = (over: Partial<Parameters<typeof poolHostDaemons>[0]> = {}) =>
    poolHostDaemons({
      hostOrder: ['d1', 'd2'],
      runtime: { d1: connected, d2: connected },
      byHost: { d1: entry({ wwd: { hostId: WWD_A, alias: 'mlab' } }), d2: entry({ wwd: { hostId: WWD_B, alias: 'air26' } }) },
      ...over,
    })

  it('maps each wwd host_id to the daemon that announced it', () => {
    expect(run()).toEqual({ [WWD_A]: 'd1', [WWD_B]: 'd2' })
  })
  it('skips a daemon that is not connected', () => {
    expect(run({ runtime: { d1: connected, d2: { status: 'disconnected' } } as never })).toEqual({ [WWD_A]: 'd1' })
  })
  it('skips an entry never fetched (fetchedAt 0 is a stale mark)', () => {
    const byHost = { d1: entry({ wwd: { hostId: WWD_A, alias: 'mlab' }, fetchedAt: 0 }), d2: entry({ wwd: { hostId: WWD_B, alias: 'air26' } }) }
    expect(run({ byHost })).toEqual({ [WWD_B]: 'd2' })
  })
  it('skips a daemon without a wwd identity', () => {
    expect(run({ byHost: { d1: entry(), d2: entry({ wwd: { hostId: WWD_B, alias: 'air26' } }) } })).toEqual({ [WWD_B]: 'd2' })
  })
  it('takes the first daemon in host order when two claim one host_id', () => {
    const same = entry({ wwd: { hostId: WWD_A, alias: 'mlab' } })
    expect(run({ byHost: { d1: same, d2: same } })).toEqual({ [WWD_A]: 'd1' })
    expect(run({ hostOrder: ['d2', 'd1'], byHost: { d1: same, d2: same } })).toEqual({ [WWD_A]: 'd2' })
  })
})

describe('refreshPoolIdentities', () => {
  const ensure = vi.fn(() => Promise.resolve())
  beforeEach(() => {
    ensure.mockClear()
    useHostStore.setState({ hostOrder: ['d1', 'd2', 'd3', 'd4', 'd5'], runtime: { d1: connected, d2: connected, d3: connected, d4: { status: 'disconnected' }, d5: connected } } as never)
    useNexHostStore.setState({
      ensure,
      byHost: {
        d1: entry({ wwd: { hostId: WWD_A, alias: 'mlab' } }),
        d2: entry(),
        d3: entry({ daemonCapabilities: ['jobs.v1'] }),
        d4: entry(),
        d5: entry({ daemonCapabilities: undefined }), // its first /api/info failed
      },
    } as never)
  })

  it('asks every connected pool.v1 daemon, with or without a wwd, and one whose /api/info never answered (ensure holds them to the TTL)', () => {
    refreshPoolIdentities()
    expect(ensure.mock.calls).toEqual([['d1'], ['d2'], ['d5']])
  })
  it('leaves a daemon with no entry at all to its own first load', () => {
    useNexHostStore.setState({ byHost: {} } as never)
    refreshPoolIdentities()
    expect(ensure).not.toHaveBeenCalled()
  })
})

describe('readPoolHostDaemons', () => {
  it('reads the live stores', () => {
    useHostStore.setState({ hostOrder: ['d1'], runtime: { d1: connected } } as never)
    useNexHostStore.setState({ byHost: { d1: entry({ wwd: { hostId: WWD_A, alias: 'mlab' } }) } } as never)
    expect(readPoolHostDaemons()).toEqual({ [WWD_A]: 'd1' })
  })
})

describe('poolHostOwners', () => {
  it('counts a daemon that is disconnected or whose entry is stale: who owns a look must not flip with a reconnect', () => {
    const owners = poolHostOwners({
      hostOrder: ['d1', 'd2', 'd3'],
      byHost: {
        d1: entry({ wwd: { hostId: WWD_A, alias: 'mlab' }, fetchedAt: 0 }),
        d2: entry({ wwd: { hostId: WWD_B, alias: 'air26' } }),
        d3: entry(), // no identity
      },
    })
    expect(owners).toEqual({ [WWD_A]: 'd1', [WWD_B]: 'd2' })
  })
  it('the first in host order wins a shared id', () => {
    const same = entry({ wwd: { hostId: WWD_A, alias: 'mlab' } })
    expect(poolHostOwners({ hostOrder: ['d2', 'd1'], byHost: { d1: same, d2: same } })).toEqual({ [WWD_A]: 'd2' })
  })
})
