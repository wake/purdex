// spa/src/lib/pool/jobs-driver-live.ts — the jobs driver with the App's real stores handed in: the pool's hosts say which origins exist, the host store and the Nex capability cache say which connected daemons announce `jobs.v1`
// (the daemon of a pool host is the App host whose daemon id THIS SESSION VERIFIED at its current endpoint is that host's `host_id` — a stored `daemonId` alone is a claim, and a wrong one would read another daemon's jobs under this origin;
// the first of the connected daemons in host order forwards for the rest).
import { selectDaemonIdVerified, useHostStore } from '../../stores/useHostStore'
import { useNexHostStore } from '../../stores/useNexHostStore'
import { createJobsDriver, type JobsTopology } from './jobs-driver'
import { useJobsStore } from './jobs-store'
import { usePoolStore } from './store'

export const JOBS_CAPABILITY = 'jobs.v1'

/** The connected daemons announcing `jobs.v1`, in host order. The Nex cache keeps a daemon's capabilities after it drops, so the connection is checked here. */
export function jobsDaemons(): string[] {
  const { byHost } = useNexHostStore.getState()
  const { hostOrder, runtime } = useHostStore.getState()
  return hostOrder.filter((id) => runtime[id]?.status === 'connected' && byHost[id]?.daemonCapabilities?.includes(JOBS_CAPABILITY))
}

export function liveTopology(): JobsTopology {
  const forwarders = jobsDaemons()
  const hostState = useHostStore.getState()
  const pool = usePoolStore.getState().hosts
  const ownDaemon: Record<string, string> = {}
  for (const h of pool) {
    const own = forwarders.find((id) => hostState.hosts[id]?.daemonId === h.host_id && selectDaemonIdVerified(hostState, id))
    if (own) ownDaemon[h.alias] = own
  }
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
