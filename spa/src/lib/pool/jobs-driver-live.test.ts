// spa/src/lib/pool/jobs-driver-live.test.ts — the topology the jobs driver reads from the App's stores: the pool's hosts are the origins, the connected daemons announcing `jobs.v1` are the sources, and a pool host's own daemon is the App host that is it.
import { describe, it, expect, beforeEach, vi } from 'vitest'
import { useHostStore } from '../../stores/useHostStore'
import { useNexHostStore } from '../../stores/useNexHostStore'
import { usePoolStore } from './store'
import { liveTopology, watchJobs, startJobsCatchUp, jobsDriver, resetLastOwnDaemons } from './jobs-driver-live'

const A = 'app-a'
const B = 'app-b'
const WWD_A = '82b11c521df16c12'
const WWD_B = '0123456789abcdef'
const hostCfg = (id: string, extra: object = {}) => ({ id, name: id, ip: '100.64.0.2', port: 7860, token: 't', order: 0, ...extra })
const entry = (caps: string[], fetchedAt = 1, wwdHostId?: string) => ({ phase: 'ready', fetchedAt, daemonCapabilities: caps, ...(wwdHostId ? { wwd: { hostId: wwdHostId, alias: '' } } : {}) })
const poolHost = (alias: string, host_id: string) => ({ alias, host_id }) as never


beforeEach(() => {
  resetLastOwnDaemons()
  usePoolStore.getState().reset()
  useHostStore.setState({
    hosts: { [A]: hostCfg(A), [B]: hostCfg(B) }, hostOrder: [A, B], activeHostId: A,
    runtime: { [A]: { status: 'connected' }, [B]: { status: 'connected' } },
  } as never)
  useNexHostStore.setState({ byHost: { [A]: entry(['jobs.v1', 'pool.v1'], 1, WWD_A), [B]: entry(['jobs.v1'], 1, WWD_B) } } as never)
  usePoolStore.setState({ hosts: [poolHost('mlab', WWD_A), poolHost('mserv', WWD_B), poolHost('air19', 'wwd-unknown')] })
})

describe('liveTopology', () => {
  it('the origins are the pool\'s hosts; the forwarders are the connected daemons announcing jobs.v1, in host order', () => {
    expect(liveTopology()).toMatchObject({ origins: ['mlab', 'mserv', 'air19'], forwarders: [A, B] })
  })

  it('a pool host\'s own daemon is the connected jobs.v1 App host whose /api/info.wwd names it', () => {
    expect(liveTopology().ownDaemon).toEqual({ mlab: A, mserv: B })
  })

  it('a stored daemon id equal to the wwd host id, with no wwd identity from /api/info, is not an own daemon (the old rule)', () => {
    useHostStore.setState({ hosts: { [A]: hostCfg(A), [B]: hostCfg(B, { daemonId: WWD_B }) } } as never)
    useNexHostStore.setState({ byHost: { [A]: entry(['jobs.v1']), [B]: entry(['jobs.v1']) } } as never)
    expect(liveTopology().ownDaemon).toEqual({})
  })

  it('a daemon that does not announce jobs.v1 is nobody\'s own daemon', () => {
    useNexHostStore.setState({ byHost: { [A]: entry(['jobs.v1'], 1, WWD_A), [B]: entry(['pool.v1'], 1, WWD_B) } } as never)
    expect(liveTopology().ownDaemon).toEqual({ mlab: A })
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

describe('liveTopology across a reconnect (the moment after it, before /api/info is read again)', () => {
  const stale = (wwd: string, caps = ['jobs.v1']) => entry(caps, 0, wwd)

  it('an origin that was read from its own daemon keeps it while that daemon is connected and its stale entry still names the host', () => {
    expect(liveTopology().ownDaemon).toEqual({ mlab: A, mserv: B })
    useNexHostStore.setState({ byHost: { [A]: entry(['jobs.v1', 'pool.v1'], 1, WWD_A), [B]: stale(WWD_B) } } as never)
    const topo = liveTopology()
    expect(topo.ownDaemon).toEqual({ mlab: A, mserv: B })
    expect(topo.forwarders).toEqual([A])
  })
  it('it is dropped once the daemon is not connected, the stale entry names another wwd, or it no longer announces jobs.v1', () => {
    liveTopology()
    useHostStore.setState({ runtime: { [A]: { status: 'connected' }, [B]: { status: 'reconnecting' } } } as never)
    useNexHostStore.setState({ byHost: { [A]: entry(['jobs.v1'], 1, WWD_A), [B]: stale(WWD_B) } } as never)
    expect(liveTopology().ownDaemon).toEqual({ mlab: A })
    useHostStore.setState({ runtime: { [A]: { status: 'connected' }, [B]: { status: 'connected' } } } as never)
    useNexHostStore.setState({ byHost: { [A]: entry(['jobs.v1'], 1, WWD_A), [B]: entry(['jobs.v1'], 1, WWD_B) } } as never)
    liveTopology()
    useNexHostStore.setState({ byHost: { [A]: entry(['jobs.v1'], 1, WWD_A), [B]: stale('ffffffffffffffff') } } as never)
    expect(liveTopology().ownDaemon).toEqual({ mlab: A })
    useNexHostStore.setState({ byHost: { [A]: entry(['jobs.v1'], 1, WWD_A), [B]: entry(['jobs.v1'], 1, WWD_B) } } as never)
    liveTopology()
    useNexHostStore.setState({ byHost: { [A]: entry(['jobs.v1'], 1, WWD_A), [B]: stale(WWD_B, ['pool.v1']) } } as never)
    expect(liveTopology().ownDaemon).toEqual({ mlab: A })
  })
  it('the memory survives the not-connected moment in between (reconnecting, then connected with the entry still stale)', () => {
    liveTopology()
    useHostStore.setState({ runtime: { [A]: { status: 'connected' }, [B]: { status: 'reconnecting' } } } as never)
    useNexHostStore.setState({ byHost: { [A]: entry(['jobs.v1'], 1, WWD_A), [B]: stale(WWD_B) } } as never)
    expect(liveTopology().ownDaemon).toEqual({ mlab: A })
    useHostStore.setState({ runtime: { [A]: { status: 'connected' }, [B]: { status: 'connected' } } } as never)
    expect(liveTopology().ownDaemon).toEqual({ mlab: A, mserv: B })
  })
  it('the memory also survives a reconnecting moment in which the entry is still fresh (it is marked stale only when the connection comes back)', () => {
    liveTopology()
    useHostStore.setState({ runtime: { [A]: { status: 'connected' }, [B]: { status: 'reconnecting' } } } as never)
    expect(liveTopology().ownDaemon).toEqual({ mlab: A })
    useHostStore.setState({ runtime: { [A]: { status: 'connected' }, [B]: { status: 'connected' } } } as never)
    useNexHostStore.setState({ byHost: { [A]: entry(['jobs.v1'], 1, WWD_A), [B]: stale(WWD_B) } } as never)
    expect(liveTopology().ownDaemon).toEqual({ mlab: A, mserv: B })
  })
  it('a fresh read that no longer names the host ends the memory for good', () => {
    liveTopology()
    useNexHostStore.setState({ byHost: { [A]: entry(['jobs.v1'], 1, WWD_A), [B]: entry(['jobs.v1'], 5, 'ffffffffffffffff') } } as never)
    expect(liveTopology().ownDaemon).toEqual({ mlab: A })
    useNexHostStore.setState({ byHost: { [A]: entry(['jobs.v1'], 1, WWD_A), [B]: stale(WWD_B) } } as never)
    expect(liveTopology().ownDaemon).toEqual({ mlab: A })
  })
  it('a daemon never seen as an origin\'s own is not made one by a stale entry', () => {
    useNexHostStore.setState({ byHost: { [A]: entry(['jobs.v1'], 1, WWD_A), [B]: stale(WWD_B) } } as never)
    expect(liveTopology().ownDaemon).toEqual({ mlab: A })
  })
})

describe('startJobsCatchUp', () => {
  it('looks again at the connections that opened early when the pool\'s hosts, the capabilities or the connections change — and no more once stopped', () => {
    const retry = vi.spyOn(jobsDriver, 'retryOpened').mockImplementation(() => {})
    const stop = startJobsCatchUp()
    usePoolStore.setState({ hosts: [poolHost('mlab', WWD_A)] })
    expect(retry).toHaveBeenCalledTimes(1)
    useNexHostStore.setState({ byHost: { [A]: entry(['jobs.v1']) } } as never)
    expect(retry).toHaveBeenCalledTimes(2)
    useHostStore.setState({ runtime: { [A]: { status: 'reconnecting' } } } as never)
    expect(retry).toHaveBeenCalledTimes(3)
    stop()
    usePoolStore.setState({ hosts: [] })
    useNexHostStore.setState({ byHost: {} } as never)
    useHostStore.setState({ runtime: {} } as never)
    expect(retry).toHaveBeenCalledTimes(3)
    retry.mockRestore()
  })
})

describe('watchJobs', () => {
  it('syncs again when the pool\'s hosts, a daemon\'s capabilities or the connections change — and no more once released', () => {
    const sync = vi.spyOn(jobsDriver, 'sync').mockImplementation(() => {})
    const release = watchJobs()
    expect(sync).toHaveBeenCalledTimes(0)
    usePoolStore.setState({ hosts: [poolHost('mlab', WWD_A)] })
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
