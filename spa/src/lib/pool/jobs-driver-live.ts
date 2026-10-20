// spa/src/lib/pool/jobs-driver-live.ts — the jobs driver with the App's real stores handed in: the pool's hosts say which origins exist, the host store and the Nex capability cache say which connected daemons announce `jobs.v1`
// (the daemon of a pool host is the App host whose own `/api/info.wwd` names that pool host — see `daemons.ts`; a stored `daemonId` is never compared with a wwd host id, they are different ids;
// the first of the connected daemons in host order forwards for the rest).
import { useHostStore } from '../../stores/useHostStore'
import { useNexHostStore } from '../../stores/useNexHostStore'
import { createJobsDriver, type JobsTopology } from './jobs-driver'
import { useJobsStore } from './jobs-store'
import { usePoolStore } from './store'
import { readPoolHostDaemons } from './daemons'

export const JOBS_CAPABILITY = 'jobs.v1'

/**
 * The connected daemons announcing `jobs.v1`, in host order. The Nex cache keeps a daemon's capabilities after it drops, so the connection is checked here; and a reconnect marks the entry stale (`fetchedAt` 0, data kept)
 * until `/api/info` is read again, so what is in it then is the last connection's and is not believed.
 */
export function jobsDaemons(): string[] {
  const { byHost } = useNexHostStore.getState()
  const { hostOrder, runtime } = useHostStore.getState()
  return hostOrder.filter((id) => runtime[id]?.status === 'connected' && (byHost[id]?.fetchedAt ?? 0) > 0 && byHost[id]?.daemonCapabilities?.includes(JOBS_CAPABILITY))
}

/** The own daemon each origin had at the last look: kept across the moment after a reconnect (see `liveTopology`). */
let lastOwn: Record<string, string> = {}
export function resetLastOwnDaemons(): void { lastOwn = {} }

/**
 * A reconnect marks the daemon's cache entry stale (`fetchedAt` 0, data kept) until `/api/info` is read again, and for that moment `jobsDaemons()` and the identity map leave it out. An origin that was read from its own
 * daemon would then fall back to a forwarding daemon's peer view (a different source: the store re-reads from cursor 0 and the fast-lane and undeclared jobs only the own daemon lists drop out) and switch back a moment later.
 * So an own daemon that is still connected, whose stale entry still names this very pool host and still announces `jobs.v1`, stays the origin's own daemon until the new read says otherwise.
 */
export function liveTopology(): JobsTopology {
  const forwarders = jobsDaemons()
  const pool = usePoolStore.getState().hosts
  const daemons = readPoolHostDaemons()
  const { runtime } = useHostStore.getState()
  const { byHost } = useNexHostStore.getState()
  const ownDaemon: Record<string, string> = {}
  for (const h of pool) {
    const own = daemons[h.host_id]
    if (own && forwarders.includes(own)) { ownDaemon[h.alias] = own; continue }
    const prev = lastOwn[h.alias]
    const e = prev ? byHost[prev] : undefined
    if (prev && runtime[prev]?.status === 'connected' && e && e.fetchedAt <= 0 && e.wwd?.hostId === h.host_id && e.daemonCapabilities?.includes(JOBS_CAPABILITY)) ownDaemon[h.alias] = prev
  }
  lastOwn = { ...ownDaemon }
  return { origins: pool.map((h) => h.alias), ownDaemon, forwarders }
}

export const jobsDriver = createJobsDriver({
  store: {
    read: (origin, source) => useJobsStore.getState().read(origin, source),
    readJob: (origin, source, id) => useJobsStore.getState().readJob(origin, source, id),
    drop: (origin) => useJobsStore.getState().drop(origin),
  },
  topology: liveTopology,
  visible: () => typeof document === 'undefined' || document.visibilityState !== 'hidden',
  rand: Math.random,
  known: (origin) => Object.hasOwn(useJobsStore.getState().byOrigin, origin),
})

/**
 * Always on (the event hook starts it): a connection that opened before its daemon counted as a source (a reconnect marks its capabilities stale until they are read again) is caught up
 * as soon as the capabilities, the connections or the pool's hosts say it is one — with or without a view open.
 */
export function startJobsCatchUp(): () => void {
  const unsubscribes = [
    usePoolStore.subscribe((s, prev) => { if (s.hosts !== prev.hosts) jobsDriver.retryOpened() }),
    useNexHostStore.subscribe((s, prev) => { if (s.byHost !== prev.byHost) jobsDriver.retryOpened() }),
    useHostStore.subscribe((s, prev) => { if (s.runtime !== prev.runtime) jobsDriver.retryOpened() }),
  ]
  return () => { for (const u of unsubscribes) u() }
}

/**
 * A view that shows jobs mounts: the driver polls for it, and an origin the pool lists later — or one whose daemon announces `jobs.v1` or connects later (a connection opens before its capabilities are read) — is read when it appears.
 * The returned function releases it all.
 */
export function watchJobs(): () => void {
  const release = jobsDriver.watch()
  const unsubscribes = [
    usePoolStore.subscribe((s, prev) => { if (s.hosts !== prev.hosts) jobsDriver.sync() }),
    useNexHostStore.subscribe((s, prev) => { if (s.byHost !== prev.byHost) jobsDriver.sync() }),
    useHostStore.subscribe((s, prev) => { if (s.runtime !== prev.runtime || s.hosts !== prev.hosts) jobsDriver.sync() }),
  ]
  return () => { for (const u of unsubscribes) u(); release() }
}
