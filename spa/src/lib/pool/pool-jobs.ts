// spa/src/lib/pool/pool-jobs.ts — the pool's jobs as the screens show them, and the gate "can the pool be read right now" (monitoring tabs MON-3a, M25).
// The jobs store keeps what it read. `poolJobsOf` is the jobs page's reading of it: the pool's hosts' jobs; a pool no daemon announces shows none; one that could not be read for a moment (no hosts, not an old
// daemon) KEEPS what was read. The sidebar does not do that last part: it asks `poolReadable` first, and a pool nobody can read draws nothing from the store.
import { useMemo } from 'react'
import { usePoolStore } from './store'
import { useJobsStore } from './jobs-store'
import type { PoolHost } from './types'
import type { PoolJob } from './jobs-types'

interface JobsOf { jobs: Record<string, PoolJob> }

/** `answered`: `sourceHostId` is set by an answer, so an empty list with it is the pool saying it has no host. */
export function poolJobsOf(byOrigin: Record<string, JobsOf>, hosts: readonly Pick<PoolHost, 'alias'>[], answered: boolean, oldDaemon: boolean): PoolJob[] {
  let shown: (origin: string) => boolean
  if (hosts.length > 0 || answered) {
    const aliases = new Set(hosts.map((h) => h.alias))
    shown = (origin) => aliases.has(origin)
  } else {
    shown = () => !oldDaemon
  }
  return Object.entries(byOrigin).filter(([origin]) => shown(origin)).flatMap(([, o]) => Object.values(o.jobs))
}

/** Some daemon answered the last read: false when every source failed (`sourceHostId` cleared) and when none is connected. */
export const poolReadable = (s: { sourceHostId: string | null }): boolean => s.sourceHostId !== null

export function usePoolJobs(): PoolJob[] {
  const hosts = usePoolStore((s) => s.hosts)
  const oldDaemon = usePoolStore((s) => s.oldDaemon)
  const answered = usePoolStore((s) => s.sourceHostId !== null)
  const byOrigin = useJobsStore((s) => s.byOrigin)
  return useMemo(() => poolJobsOf(byOrigin, hosts, answered, oldDaemon), [byOrigin, hosts, answered, oldDaemon])
}

export function usePoolReadable(): boolean {
  return usePoolStore(poolReadable)
}
