// spa/src/lib/pool/jobs-driver-live.test.ts — the topology the jobs driver reads from the App's stores: the pool's hosts are the origins, the connected daemons announcing `jobs.v1` are the sources, and a pool host's own daemon is the App host that is it.
import { describe, it, expect, beforeEach, vi } from 'vitest'
import { hostEndpoint, useHostStore } from '../../stores/useHostStore'
import { useNexHostStore } from '../../stores/useNexHostStore'
import { usePoolStore } from './store'
import { liveTopology, watchJobs, jobsDriver } from './jobs-driver-live'

const A = 'app-a'
const B = 'app-b'
const hostCfg = (id: string, extra: object = {}) => ({ id, name: id, ip: '100.64.0.2', port: 7860, token: 't', order: 0, ...extra })
const entry = (caps: string[], fetchedAt = 1) => ({ phase: 'ready', fetchedAt, daemonCapabilities: caps })
const poolHost = (alias: string, host_id: string) => ({ alias, host_id }) as never

const verified = (id: string, daemonId: string) => ({ status: 'connected', daemonIdVerified: { endpoint: hostEndpoint(hostCfg(id)), daemonId } })

beforeEach(() => {
  usePoolStore.getState().reset()
  useHostStore.setState({
    hosts: { [A]: hostCfg(A), [B]: hostCfg(B, { daemonId: 'daemon-b' }) }, hostOrder: [A, B], activeHostId: A,
    runtime: { [A]: { status: 'connected' }, [B]: verified(B, 'daemon-b') },
  } as never)
  useNexHostStore.setState({ byHost: { [A]: entry(['jobs.v1', 'pool.v1']), [B]: entry(['jobs.v1']) } } as never)
  usePoolStore.setState({ hosts: [poolHost('mlab', A), poolHost('mserv', 'daemon-b'), poolHost('air19', 'unknown-id')] })
})

describe('liveTopology', () => {
  it('the origins are the pool\'s hosts; the forwarders are the connected daemons announcing jobs.v1, in host order', () => {
    expect(liveTopology()).toMatchObject({ origins: ['mlab', 'mserv', 'air19'], forwarders: [A, B] })
  })

  it('a pool host\'s own daemon is the App host whose daemon id this session verified is its host id', () => {
    expect(liveTopology().ownDaemon).toEqual({ mserv: B })
  })

  it('a stored daemon id nobody verified is only a claim: that host is not the pool host\'s own daemon (it would read another daemon\'s jobs under this origin)', () => {
    useHostStore.setState({ runtime: { [A]: { status: 'connected' }, [B]: { status: 'connected' } } } as never)
    expect(liveTopology().ownDaemon).toEqual({})
    useHostStore.setState({ runtime: { [A]: { status: 'connected' }, [B]: verified(B, 'another-daemon') } } as never)
    expect(liveTopology().ownDaemon).toEqual({})
    useHostStore.setState({ runtime: { [A]: { status: 'connected' }, [B]: { status: 'connected', daemonIdVerified: { endpoint: '9.9.9.9:7860', daemonId: 'daemon-b' } } } } as never)
    expect(liveTopology().ownDaemon).toEqual({})
  })

  it('an App host id is not a daemon id: it never makes a host a pool host\'s own daemon', () => {
    usePoolStore.setState({ hosts: [poolHost('mlab', A)] })
    expect(liveTopology().ownDaemon).toEqual({})
  })

  it('a daemon that dropped, or does not announce jobs.v1, answers for nobody (the capability cache outlives the connection)', () => {
    useHostStore.setState({ runtime: { [A]: { status: 'disconnected' }, [B]: { status: 'connected' } } } as never)
    useNexHostStore.setState({ byHost: { [A]: entry(['jobs.v1']), [B]: entry(['pool.v1']) } } as never)
    expect(liveTopology()).toMatchObject({ forwarders: [], ownDaemon: {} })
  })
})

describe('liveTopology and a connection that came back', () => {
  it('a daemon whose capabilities are the last connection\'s (the cache was marked stale: fetchedAt 0) is not counted until they are read again', () => {
    useNexHostStore.setState({ byHost: { [A]: entry(['jobs.v1'], 0), [B]: entry(['jobs.v1'], 0) } } as never)
    expect(liveTopology()).toMatchObject({ forwarders: [], ownDaemon: {} })
    useNexHostStore.setState({ byHost: { [A]: entry(['jobs.v1'], 5), [B]: entry(['jobs.v1'], 0) } } as never)
    expect(liveTopology().forwarders).toEqual([A])
  })
})

describe('watchJobs', () => {
  it('syncs again when the pool\'s hosts, a daemon\'s capabilities or the connections change — and no more once released', () => {
    const sync = vi.spyOn(jobsDriver, 'sync').mockImplementation(() => {})
    const release = watchJobs()
    expect(sync).toHaveBeenCalledTimes(0)
    usePoolStore.setState({ hosts: [poolHost('mlab', A)] })
    expect(sync).toHaveBeenCalledTimes(1)
    useNexHostStore.setState({ byHost: { [A]: entry(['jobs.v1']) } } as never)
    expect(sync).toHaveBeenCalledTimes(2)
    useHostStore.setState({ runtime: { [A]: { status: 'reconnecting' } } } as never)
    expect(sync).toHaveBeenCalledTimes(3)
    release()
    usePoolStore.setState({ hosts: [] })
    useNexHostStore.setState({ byHost: {} } as never)
    useHostStore.setState({ runtime: {} } as never)
    expect(sync).toHaveBeenCalledTimes(3)
    sync.mockRestore()
  })
})
