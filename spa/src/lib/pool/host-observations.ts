// spa/src/lib/pool/host-observations.ts — the 行程 tab's undeclared heavy commands, per pool host (monitoring tabs spec M45, plan MON-8c): the grouped read of the last 7 days (`/undeclared`), kept in module memory so coming back to the tab
// shows what was read at once. Not shared with the 事件 tab's reads (that one draws 3 records a host). Same rules as `proc-history.ts`: the same ask while one is out shares it, a different ask or the last page leaving calls it off
// (it writes nothing), nobody-listening hosts are let go past 32. A failure keeps the last good page (and says the refresh failed).
import { PoolReadError } from './api'
import { fetchUndeclared, type UndeclaredPage } from './observations-api'

export type ObservationLoad = 'idle' | 'loading' | 'ok' | 'forbidden' | 'error'

export const UNDECLARED_SPAN_MS = 7 * 24 * 3600_000
const MAX_HOSTS = 32

interface ObsMemory {
  /** `key` is the source and alias it was read for: a page is shown only to the ask that read it. */
  undeclared: { page: UndeclaredPage; at: number; key: string } | null
  load: ObservationLoad
  gen: number
  holds: number
  listeners: Set<() => void>
  inFlight: { key: string; promise: Promise<ObservationLoad> } | null
}
const memory = new Map<string, ObsMemory>()
const inUse = (m: ObsMemory): boolean => m.listeners.size > 0 || m.holds > 0
function prune(except?: string): void {
  for (const [id, old] of memory) {
    if (memory.size <= MAX_HOSTS) break
    if (id !== except && !inUse(old)) memory.delete(id)
  }
}
const memoryOf = (hostId: string): ObsMemory => {
  let m = memory.get(hostId)
  if (!m) {
    m = { undeclared: null, load: 'idle', gen: 0, holds: 0, listeners: new Set(), inFlight: null }
    memory.set(hostId, m)
    prune(hostId)
  }
  return m
}
const notify = (m: ObsMemory): void => { for (const fn of [...m.listeners]) fn() }
export const clearHostObservations = (): void => { memory.clear() }

export function subscribeObservations(hostId: string, fn: () => void): () => void {
  const m = memoryOf(hostId)
  m.listeners.add(fn)
  return () => { m.listeners.delete(fn); prune() }
}

/** A page holds the read open; when the last one leaves, an answer still out is called off. */
export function holdObservations(hostId: string): () => void {
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

export const observationKey = (source: string, alias: string): string => `${source}\0${alias}`
/** The page read for this ask (`observationKey`), or null: a page read for another source or alias is never shown under this one. */
export function readUndeclared(hostId: string, key: string): { page: UndeclaredPage; at: number } | null {
  const u = memory.get(hostId)?.undeclared
  return u && u.key === key ? u : null
}
export const readUndeclaredLoad = (hostId: string): ObservationLoad => memory.get(hostId)?.load ?? 'idle'

const failure = (e: unknown): ObservationLoad => (e instanceof PoolReadError && e.poolError.code === 'forbidden' ? 'forbidden' : 'error')

/** Reads the last 7 days' groups of `alias` through `source`'s daemon. The same ask while one is out shares it. Never throws. */
export function loadUndeclared(source: string, alias: string, hostId: string, now: number): Promise<ObservationLoad> {
  const m = memoryOf(hostId)
  const key = observationKey(source, alias)
  if (m.inFlight?.key === key) return m.inFlight.promise
  const mine = ++m.gen
  // Another source or alias is another host's reading: what was read for the old one goes now, not when the new one answers (or fails).
  if (m.undeclared && m.undeclared.key !== key) m.undeclared = null
  m.load = 'loading'
  notify(m)
  const promise = (async (): Promise<ObservationLoad> => {
    let page: UndeclaredPage | null = null
    let out: ObservationLoad
    try {
      page = await fetchUndeclared(source, alias, new Date(now - UNDECLARED_SPAN_MS).toISOString())
      out = 'ok'
    } catch (e) { out = failure(e) }
    // A different ask started, or the last page left, while this one was out: it owns the memory now.
    if (m.gen !== mine) return m.load
    if (m.inFlight?.key === key) m.inFlight = null
    if (out === 'ok' && page) m.undeclared = { page, at: now, key }
    // A refusal means this token may not read them any more: what an earlier read got is not shown (an ordinary failure keeps the last good page).
    else if (out === 'forbidden') m.undeclared = null
    m.load = out
    notify(m)
    return out
  })()
  m.inFlight = { key, promise }
  return promise
}
