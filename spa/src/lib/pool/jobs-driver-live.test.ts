// spa/src/lib/pool/jobs-driver-live.test.ts — the topology the jobs driver reads from the App's stores: the pool's hosts are the origins, the connected daemons announcing `jobs.v1` are the sources, and a pool host's own daemon is the App host that is it.
import { describe, it, expect, beforeEach } from 'vitest'
import { useHostStore } from '../../stores/useHostStore'
import { useNexHostStore } from '../../stores/useNexHostStore'
import { usePoolStore } from './store'
import { liveTopology } from './jobs-driver-live'

const A = 'app-a'
const B = 'app-b'
const hostCfg = (id: string, extra: object = {}) => ({ id, name: id, ip: '100.64.0.2', port: 7860, token: 't', order: 0, ...extra })
const entry = (caps: string[]) => ({ phase: 'ready', daemonCapabilities: caps })
const poolHost = (alias: string, host_id: string) => ({ alias, host_id }) as never

beforeEach(() => {
  usePoolStore.getState().reset()
  useHostStore.setState({
    hosts: { [A]: hostCfg(A), [B]: hostCfg(B, { daemonId: 'daemon-b' }) }, hostOrder: [A, B], activeHostId: A,
    runtime: { [A]: { status: 'connected' }, [B]: { status: 'connected' } },
  } as never)
  useNexHostStore.setState({ byHost: { [A]: entry(['jobs.v1', 'pool.v1']), [B]: entry(['jobs.v1']) } } as never)
  usePoolStore.setState({ hosts: [poolHost('mlab', A), poolHost('mserv', 'daemon-b'), poolHost('air19', 'unknown-id')] })
})

describe('liveTopology', () => {
  it('the origins are the pool\'s hosts; the forwarders are the connected daemons announcing jobs.v1, in host order', () => {
    expect(liveTopology()).toMatchObject({ origins: ['mlab', 'mserv', 'air19'], forwarders: [A, B] })
  })

  it('a pool host\'s own daemon is the App host whose id, or whose verified daemon id, is its host id', () => {
    expect(liveTopology().ownDaemon).toEqual({ mlab: A, mserv: B })
  })

  it('a daemon that dropped, or does not announce jobs.v1, answers for nobody (the capability cache outlives the connection)', () => {
    useHostStore.setState({ runtime: { [A]: { status: 'disconnected' }, [B]: { status: 'connected' } } } as never)
    useNexHostStore.setState({ byHost: { [A]: entry(['jobs.v1']), [B]: entry(['pool.v1']) } } as never)
    expect(liveTopology()).toMatchObject({ forwarders: [], ownDaemon: {} })
  })
})
