// spa/src/lib/pool/proc-history.ts — the 行程 tab's hour of processes (monitoring tabs spec M45, plan MON-8b): one record of `procs` every 15 s for the last hour, per pool host. wwd's 5 s ring holds exactly an hour
// (720 records) and every record carries `procs`; the short track of `host-history.ts` keeps only the readings, so the processes are kept here. Read once when somebody first moves the timeline, then topped up from the live facts.
// Module memory, not persisted. Same rules as `host-history.ts`: a late answer never writes over a newer one, nobody-listening hosts are let go past 32.
import { fetchFactsHistory, PoolReadError } from './api'
import { SKEW_MS } from './host-history'
import type { PoolFacts, PoolProc } from './types'

export interface ProcSnap { at: number; procs: PoolProc[] }
export type ProcLoad = 'idle' | 'loading' | 'ok' | 'host_not_local' | 'error'

export const PROC_STEP_MS = 15_000
/** An hour, and a minute for the clock of the daemon. */
export const PROC_KEEP_MS = 61 * 60_000
/** A read this recent is not made again. */
const FRESH_MS = 5 * 60_000
/** A record dated more than this ahead of this clock cannot be placed and is dropped. */
const FUTURE_TOLERANCE_MS = 5_000
const MAX_HOSTS = 32

interface ProcMemory {
  snaps: ProcSnap[]; load: ProcLoad; loadedAt: number | null; gen: number; holds: number; listeners: Set<() => void>
  /** The read in flight, by what it asks for: a second page asking the same is given this one, not a new request that would call it off. */
  inFlight: { key: string; promise: Promise<ProcLoad> } | null
}
const memory = new Map<string, ProcMemory>()
const inUse = (m: ProcMemory): boolean => m.listeners.size > 0 || m.holds > 0
function prune(except?: string): void {
  for (const [id, old] of memory) {
    if (memory.size <= MAX_HOSTS) break
    if (id !== except && !inUse(old)) memory.delete(id)
  }
}
const memoryOf = (hostId: string): ProcMemory => {
  let m = memory.get(hostId)
  if (!m) {
    m = { snaps: [], load: 'idle', loadedAt: null, gen: 0, holds: 0, listeners: new Set(), inFlight: null }
    memory.set(hostId, m)
    prune(hostId)
  }
  return m
}
const notify = (m: ProcMemory): void => { for (const fn of [...m.listeners]) fn() }
export const clearProcHistory = (): void => { memory.clear() }

export function subscribeProcs(hostId: string, fn: () => void): () => void {
  const m = memoryOf(hostId)
  m.listeners.add(fn)
  return () => { m.listeners.delete(fn); prune() }
}

/** A page holds the read open; when the last one leaves, an answer still out is called off (it writes nothing). */
export function holdProcs(hostId: string): () => void {
  const m = memoryOf(hostId)
  m.holds += 1
  let released = false
  return () => {
    if (released) return
    released = true
    m.holds -= 1
    if (m.holds <= 0) { m.gen += 1; m.inFlight = null }
    prune()
  }
}

export const readProcSnaps = (hostId: string): ProcSnap[] => memory.get(hostId)?.snaps ?? []
export const readProcLoad = (hostId: string): ProcLoad => memory.get(hostId)?.load ?? 'idle'
/** Was the hour read successfully in the last 5 minutes? Then coming back does not read it again, the live facts carry on from it. */
export function procsFresh(hostId: string, now: number): boolean {
  const m = memory.get(hostId)
  return !!m && m.load === 'ok' && m.loadedAt !== null && now - m.loadedAt < FRESH_MS
}

/** Time order, once per time (what is there stays), nothing from the future, only an hour (and a minute) back from the newest. */
function merge(have: ProcSnap[], add: ProcSnap[], now: number): ProcSnap[] {
  const byAt = new Map<number, ProcSnap>()
  for (const s of add) if (s.at <= now + FUTURE_TOLERANCE_MS) byAt.set(s.at, s)
  for (const s of have) byAt.set(s.at, s)
  const all = [...byAt.values()].sort((a, b) => a.at - b.at)
  if (all.length === 0) return all
  const newest = all[all.length - 1].at
  return all.filter((s) => s.at >= newest - PROC_KEEP_MS)
}

/** Oldest first: keep a record when it is a step after the one kept before it. */
function thin(records: PoolFacts[]): ProcSnap[] {
  const out: ProcSnap[] = []
  for (const r of records) {
    if (out.length === 0 || r.at - out[out.length - 1].at >= PROC_STEP_MS) out.push({ at: r.at, procs: r.procs })
  }
  return out
}

const failure = (e: unknown): ProcLoad => (e instanceof PoolReadError && e.poolError.code === 'host_not_local' ? 'host_not_local' : 'error')

/** Reads the last hour of 5 s records of `alias` through `source`'s daemon and keeps every 15 s. The same ask while one is out shares it. Never throws. */
export function loadProcHistory(source: string, alias: string, hostId: string, now: number): Promise<ProcLoad> {
  const m = memoryOf(hostId)
  const key = `${source}\0${alias}`
  if (m.inFlight?.key === key) return m.inFlight.promise
  const mine = ++m.gen
  m.load = 'loading'
  notify(m)
  const promise = (async (): Promise<ProcLoad> => {
    let records: PoolFacts[] = []
    let out: ProcLoad
    try {
      records = (await fetchFactsHistory(source, alias, new Date(now - 60 * 60_000 - SKEW_MS).toISOString(), '5s')).records
      out = 'ok'
    } catch (e) { out = failure(e) }
    // A different ask started, or the page left, while this one was out: it owns the memory now, whatever this one found.
    if (m.gen !== mine) return m.load
    if (m.inFlight?.promise === promise) m.inFlight = null
    if (out === 'ok') { m.snaps = merge(m.snaps, thin(records), now); m.loadedAt = now }
    m.load = out
    notify(m)
    return out
  })()
  m.inFlight = { key, promise }
  return promise
}

/** A new facts record of this host joins the hour when it is a step after the last one. */
export function appendLiveProcs(hostId: string, facts: PoolFacts, now: number = Date.now()): void {
  const m = memoryOf(hostId)
  const last = m.snaps[m.snaps.length - 1]
  if (last && facts.at - last.at < PROC_STEP_MS) return
  m.snaps = merge(m.snaps, [{ at: facts.at, procs: facts.procs }], now)
  notify(m)
}
