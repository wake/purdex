// spa/src/lib/pool/host-observations.ts — the 行程 tab's undeclared heavy commands, per pool host (monitoring tabs spec M45, plan MON-8c): the grouped read of the last 7 days (`/undeclared`), kept in module memory so coming back to the tab
// shows what was read at once. Not shared with the 事件 tab's reads (that one draws 3 records a host). Same rules as `proc-history.ts`: the same ask while one is out shares it, a different ask or the last page leaving calls it off
// (it writes nothing), nobody-listening hosts are let go past 32. A failure keeps the last good page (and says the refresh failed).
import { PoolReadError } from './api'
import { fetchObservations, fetchUndeclared, type PoolObservation, type UndeclaredPage } from './observations-api'
import { mergeObservations } from './events-model'

export type ObservationLoad = 'idle' | 'loading' | 'ok' | 'forbidden' | 'error'

export const UNDECLARED_SPAN_MS = 7 * 24 * 3600_000
export const OBSERVATIONS_SPAN_MS = 24 * 3600_000
const MAX_HOSTS = 32

/** The records the reader has: the newest page (read again every minute) and the older pages they asked for, which a re-read keeps (the ones the sliding 24 hours have left go). */
export interface ObservationRecords {
  /** One record by its id, the newest start first. */
  list: PoolObservation[]
  /** The cursor for the next older page, exactly as wwd wrote it; null when there is none. */
  nextUntil: string | null
  truncated: boolean
  known: boolean
  at: number
}
interface RecordsMemory {
  key: string
  /** The `since` of the newest page; older pages are asked with the same one. */
  since: string
  first: { list: readonly PoolObservation[]; nextUntil: string | null; truncated: boolean; known: boolean }
  older: readonly PoolObservation[]
  /** null until an older page is loaded: then the cursor is the newest page's. */
  olderNext: { value: string | null } | null
  view: ObservationRecords
}
export type OlderLoad = 'idle' | 'loading' | 'error'

interface ObsMemory {
  records: RecordsMemory | null
  recordsLoad: ObservationLoad
  recordsGen: number
  recordsInFlight: { key: string; promise: Promise<ObservationLoad> } | null
  olderLoad: OlderLoad
  olderInFlight: Promise<OlderLoad> | null
  /** Identifies the older-page request that may still write; anything that changes what the cursor means takes it away. */
  olderToken: object | null
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
    m = { records: null, recordsLoad: 'idle', recordsGen: 0, recordsInFlight: null, olderLoad: 'idle', olderInFlight: null, olderToken: null, undeclared: null, load: 'idle', gen: 0, holds: 0, listeners: new Set(), inFlight: null }
    memory.set(hostId, m)
    prune(hostId)
  }
  return m
}
/** Calls off the older page that is out: whatever it brings back belongs to records that are gone or have moved on. */
const dropOlder = (m: ObsMemory): void => { m.olderToken = null; m.olderInFlight = null; if (m.olderLoad === 'loading') m.olderLoad = 'idle' }
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
    if (m.holds <= 0) { m.gen += 1; m.inFlight = null; m.recordsGen += 1; m.recordsInFlight = null; dropOlder(m) }
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

const viewOf = (r: Omit<RecordsMemory, 'view'>, at: number): ObservationRecords => ({
  list: mergeObservations(r.first.list, r.older).sort((a, b) => b.startedAt - a.startedAt),
  nextUntil: r.olderNext ? r.olderNext.value : r.first.nextUntil,
  truncated: r.first.truncated, known: r.first.known, at,
})
/** The records read for this ask (`observationKey`), or null: records read for another source or alias are never shown under this one. */
export function readRecords(hostId: string, key: string): ObservationRecords | null {
  const r = memory.get(hostId)?.records
  return r && r.key === key ? r.view : null
}
export const readRecordsLoad = (hostId: string): ObservationLoad => memory.get(hostId)?.recordsLoad ?? 'idle'
export const readOlderLoad = (hostId: string): OlderLoad => memory.get(hostId)?.olderLoad ?? 'idle'

/**
 * Reads the newest page (the last 24 hours) of `alias`'s observation records through `source`'s daemon. The older pages the reader loaded stay: the new first page is merged with them. The same ask while one is out shares it.
 * Never throws.
 */
export function loadRecords(source: string, alias: string, hostId: string, now: number): Promise<ObservationLoad> {
  const m = memoryOf(hostId)
  const key = observationKey(source, alias)
  if (m.recordsInFlight?.key === key) return m.recordsInFlight.promise
  const mine = ++m.recordsGen
  if (m.records && m.records.key !== key) { m.records = null; dropOlder(m) }
  m.recordsLoad = 'loading'
  notify(m)
  const since = new Date(now - OBSERVATIONS_SPAN_MS).toISOString()
  const promise = (async (): Promise<ObservationLoad> => {
    let page: Awaited<ReturnType<typeof fetchObservations>> | null = null
    let out: ObservationLoad
    try {
      page = await fetchObservations(source, alias, { since })
      out = 'ok'
    } catch (e) { out = failure(e) }
    if (m.recordsGen !== mine) return m.recordsLoad
    if (m.recordsInFlight?.key === key) m.recordsInFlight = null
    if (out === 'ok' && page) {
      const kept = m.records && m.records.key === key ? m.records : null
      const base = {
        key, since,
        first: { list: page.observations, nextUntil: page.nextUntil, truncated: page.truncated, known: page.known },
        older: (kept?.older ?? []).filter((o) => o.startedAt >= now - OBSERVATIONS_SPAN_MS), olderNext: kept?.olderNext ?? null,
      }
      m.records = { ...base, view: viewOf(base, now) }
      // The window moved: an older page asked with the old window and cursor must not be merged into this one.
      dropOlder(m)
    } else if (out === 'forbidden') { m.records = null; dropOlder(m) }
    m.recordsLoad = out
    notify(m)
    return out
  })()
  m.recordsInFlight = { key, promise }
  return promise
}

/** The next older page, from the cursor the last page gave, sent back as it came. Does nothing when there is no cursor or one is already out. Never throws. */
export function loadOlder(source: string, alias: string, hostId: string): Promise<OlderLoad> {
  const m = memoryOf(hostId)
  const key = observationKey(source, alias)
  const r = m.records
  if (m.olderInFlight) return m.olderInFlight
  if (!r || r.key !== key || r.view.nextUntil === null) return Promise.resolve(m.olderLoad)
  const until = r.view.nextUntil
  const token = {}
  m.olderToken = token
  m.olderLoad = 'loading'
  notify(m)
  const promise = (async (): Promise<OlderLoad> => {
    let page: Awaited<ReturnType<typeof fetchObservations>> | null = null
    let forbidden = false
    try { page = await fetchObservations(source, alias, { since: r.since, until }) } catch (e) { forbidden = failure(e) === 'forbidden' }
    // The records were dropped or are for another ask, or the last page left: this answer is not theirs.
    const cur = m.records
    if (m.olderToken !== token || !cur || cur.key !== key) return m.olderLoad
    m.olderToken = null; m.olderInFlight = null
    if (page) {
      const base = { key: cur.key, since: cur.since, first: cur.first, older: mergeObservations(cur.older, page.observations), olderNext: { value: page.nextUntil } }
      m.records = { ...base, view: viewOf(base, cur.view.at) }
      m.olderLoad = 'idle'
    } else if (forbidden) { m.records = null; m.recordsLoad = 'forbidden'; m.olderLoad = 'idle' }
    else m.olderLoad = 'error'
    notify(m)
    return m.olderLoad
  })()
  m.olderInFlight = promise
  return promise
}
