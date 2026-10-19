// spa/src/lib/pool/job-detail.ts — one job's detail (plan "WWD-7 design", Jobs tab, detail panel): what its log area says, and the read of the detail with its log. Read on open and again when the job itself changes (its state or its stored
// log — `jobs.changed` re-reads the job in the store, and that is what moves these); never polled. The log is the detail's own up to 1 MiB; a larger one is read on demand from `/log`.
import { useCallback, useEffect, useState } from 'react'
import { PoolReadError } from './api'
import type { PoolError } from './errors'
import { fetchJob, fetchJobLog } from './jobs-api'
import { liveTopology } from './jobs-driver-live'
import { jobSourceFor } from './jobs-store'
import { isEnded, type PoolJob } from './jobs-types'

/** A log is uploaded after the result: within this long of the end its absence is "still uploading", later it is gone. */
export const LOG_UPLOAD_GRACE_MS = 60_000

export type LogNote = 'running' | 'text' | 'on_demand' | 'uploading' | 'expired' | 'none'

/** What the log area says. `inline` is the log the detail carried (null: none, or over 1 MiB). */
export function logNote(job: PoolJob, inline: string | null, now: number): LogNote {
  if (!isEnded(job.state)) return 'running'
  if (inline !== null) return 'text'
  const r = job.result
  if (r === null) return 'none'
  if (r.log !== '') return 'on_demand'
  if (r.finished_at === null) return 'none'
  return now - r.finished_at < LOG_UPLOAD_GRACE_MS ? 'uploading' : 'expired'
}

/** The daemon to ask about a job: its origin's own, else the first that announces `jobs.v1` forwards (`?host=`). null: none connected. */
export function detailSource(origin: string) {
  const topo = liveTopology()
  return jobSourceFor(origin, topo.ownDaemon, topo.forwarders[0] ?? null)
}

/** No connected daemon announces `jobs.v1`: the same as a daemon that does not answer. */
const NO_DAEMON: PoolError = { code: 'no_answer', action: 'next_source' }
const errorOf =(e: unknown): PoolError => (e instanceof PoolReadError ? e.poolError : { code: 'unknown', action: 'log' })

export type DetailRead = { phase: 'loading' } | { phase: 'ready'; log: string | null } | { phase: 'error'; error: PoolError }
export type FullLogRead = { phase: 'idle' } | { phase: 'loading' } | { phase: 'ready'; text: string } | { phase: 'error'; error: PoolError }

export function useJobDetail(job: PoolJob): { detail: DetailRead; full: FullLogRead; loadFull: () => void } {
  const { origin, id, state } = job
  const stored = job.result?.log ?? ''
  // Every answer carries the picture of the job it was asked for: one that is not the current picture (another job, or this one changed since) reads as not yet read, so a slow answer never shows under another job.
  const tag = `${origin}\u0000${id}\u0000${state}\u0000${stored}`
  const [got, setGot] = useState<{ tag: string; read: DetailRead } | null>(null)
  const [fullGot, setFullGot] = useState<{ tag: string; read: FullLogRead } | null>(null)
  const detail: DetailRead = got?.tag === tag ? got.read : { phase: 'loading' }
  const full: FullLogRead = fullGot?.tag === tag ? fullGot.read : { phase: 'idle' }

  useEffect(() => {
    let live = true
    const source = detailSource(origin)
    const read = source === null ? Promise.reject(new PoolReadError(`/api/pool/jobs/${id}`, null, NO_DAEMON)) : fetchJob(source.daemonId, id, source.host)
    read.then(
      (d) => { if (live) setGot({ tag, read: { phase: 'ready', log: d.log } }) },
      (e) => { if (live) setGot({ tag, read: { phase: 'error', error: errorOf(e) } }) },
    )
    return () => { live = false }
  }, [origin, id, tag])

  const loadFull = useCallback(() => {
    const source = detailSource(origin)
    setFullGot({ tag, read: { phase: 'loading' } })
    const read = source === null ? Promise.reject(new PoolReadError(`/api/pool/jobs/${id}/log`, null, NO_DAEMON)) : fetchJobLog(source.daemonId, id, source.host)
    read.then(
      (text) => setFullGot({ tag, read: { phase: 'ready', text } }),
      (e) => setFullGot({ tag, read: { phase: 'error', error: errorOf(e) } }),
    )
  }, [origin, id, tag])

  return { detail, full, loadFull }
}
