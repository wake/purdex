// spa/src/lib/pool/host-status.ts — the host page's 狀態 tab as data (monitoring tabs spec M44; plan MON-7b-1): what the six cards and the facts table say, worked out from the pool's facts and host row. Pure: data in, data out; the words are
// i18n keys (as in words.ts) and the component calls `t`. Read from the pool only, never the fleet.
import { DISK_SCORE_FROM, levelScore, type RingDriver } from './ring-driver'
import { alertRowsOf, type AlertRow, type EventHost } from './events-model'
import type { HostAlertsEntry } from './alerts-store'
import type { MonitorHost } from './use-monitor-hosts'
import { ownerMinutes } from './worker-rule'
import type { PoolException, PoolExceptionDetail, PoolFacts, PoolHold, PoolHost, PoolLiveness, PoolLoad, PoolOwner, PoolZone, Slots } from './types'

const clamp100 = (v: number): number => Math.min(Math.max(v, 0), 100)

/** One reading that goes into the composite load (the ring's length): its score, the raw reading it came from, whether it counts, and whether it is the one the daemon names as the main driver. */
export interface LoadPart { id: RingDriver; score: number; raw: number | null; counted: boolean; driver: boolean }
export interface JudgeSlot { n: number; /** The reasons the class is not taken (`headroom_reasons`); null: the daemon does not say (no `pool.assess.v1`). */ codes: string[] | null }
export interface Judge {
  zone: PoolZone
  /** The memory pressure level; 0 (unknown) is null. */
  level: number | null
  pageRate: number | null
  heavy: JudgeSlot
  light: JudgeSlot
  sessionsRoom: number
  load: PoolLoad | null
  parts: LoadPart[]
}

/**
 * The 判斷 card. wwd's zone rests on the pressure level and the page rate alone (`internal/wwd/derived/derived.go`); the composite load is the daemon's (`internal/fleet/load.go`): the largest of CPU %, memory %, the level's score,
 * the page rate capped at 100 and the fullest disk, which only counts from `DISK_SCORE_FROM`. `driver` is what the daemon says drove it, so with no `load` there is none, and none is worked out here.
 */
export function judgeOf(facts: PoolFacts, pool: PoolHost): Judge {
  const level = facts.mem.pressure > 0 ? facts.mem.pressure : null
  const cpu = 100 - facts.cpu.idle_pct
  const mem = facts.mem.total_mb > 0 ? ((facts.mem.total_mb - facts.mem.available_mb) / facts.mem.total_mb) * 100 : null
  const fullest = facts.disk.reduce<number | null>((best, d) => (best === null || d.used_pct > best ? d.used_pct : best), null)
  const counted = fullest !== null && fullest >= DISK_SCORE_FROM
  const driver = pool.load?.driver
  const part = (id: RingDriver, score: number, raw: number | null, isCounted = true): LoadPart => ({ id, score, raw, counted: isCounted, driver: driver === id })
  return {
    zone: facts.derived.zone,
    level,
    pageRate: facts.mem.page_out_rate,
    heavy: { n: facts.derived.headroom.heavy, codes: pool.headroom_reasons?.heavy ?? null },
    light: { n: facts.derived.headroom.light, codes: pool.headroom_reasons?.light ?? null },
    sessionsRoom: facts.derived.sessions_room,
    load: pool.load ?? null,
    parts: [
      part('cpu', clamp100(cpu), cpu),
      part('mem', mem === null ? 0 : clamp100(mem), mem),
      part('pressure', levelScore(facts.mem.pressure), level),
      part('paging', facts.mem.page_out_rate === null ? 0 : clamp100(facts.mem.page_out_rate), facts.mem.page_out_rate),
      part('disk', counted ? clamp100(fullest) : 0, fullest, counted),
    ],
  }
}

// ---- Worker and owner, pauses -----------------------------------------------------------------------------------------------------------------------------

export interface WorkerCard { enabled: boolean; slots: Slots; running: Slots; ownerRule: string; ownerMinutes: number | null; /** null: no reading (Linux). */ owner: PoolOwner | null }

export function workerCardOf(facts: PoolFacts): WorkerCard {
  const w = facts.worker
  return { enabled: w.enabled, slots: w.slots, running: w.running, ownerRule: w.owner_rule, ownerMinutes: ownerMinutes(w.owner_rule), owner: facts.owner }
}

/** A pause in force, with what it holds (`holds`, `pid` are from `GET …/exceptions`; null when that was not read or does not name it: "not known what it holds", never "holds nothing"). */
export interface Pause { name: string; since: number; until: number | null; pid: number | null; holds: PoolHold[] | null }

/** The facts' pauses in force at `at` (the record's own time, not the App's clock), by start time. */
export function pausesOf(exceptions: readonly PoolException[], details: readonly PoolExceptionDetail[] | null, at: number): Pause[] {
  return exceptions
    .filter((e) => e.since <= at && (e.until === null || e.until > at))
    .sort((a, b) => a.since - b.since)
    .map((e) => {
      const d = details?.find((x) => x.name === e.name)
      return { name: e.name, since: e.since, until: e.until, pid: d?.pid ?? null, holds: d ? d.holds : null }
    })
}

// ---- Connection ------------------------------------------------------------------------------------------------------------------------------------------

const OUTAGES_SHOWN = 5

export type Connection =
  | { kind: 'local' }
  | { kind: 'peer'; state: PoolLiveness; since: number | null; checkedAt: number | null; /** The stops of this host, newest first (at most five), and how many more there are. */ outages: { rows: AlertRow[]; more: number } }

/**
 * The 連線 card. The local host has no liveness of its own (the daemon reads its wwd directly and does not probe it), so it is told apart by `host.local` or by a row without `liveness`. A peer's outages are the `silence` alerts about it,
 * from every host's list, with the same stop seen by several hosts being one (`alertRowsOf`, 88 10-20); another host's silence, or an ordinary alert, is not its outage.
 */
export function connectionOf(host: MonitorHost, byHost: Record<string, HostAlertsEntry>, hosts: readonly EventHost[]): Connection {
  const pool = host.state.pool
  if (host.local || pool.liveness === undefined) return { kind: 'local' }
  const all = alertRowsOf(byHost, hosts).filter((r) => r.kind === 'silence' && r.hostId === host.hostId).sort((a, b) => b.at - a.at)
  return {
    kind: 'peer', state: pool.liveness, since: pool.liveness_since ?? null, checkedAt: pool.liveness_at ?? null,
    outages: { rows: all.slice(0, OUTAGES_SHOWN), more: Math.max(0, all.length - OUTAGES_SHOWN) },
  }
}

// ---- Version and identity, labels, tools, fingerprint ---------------------------------------------------------------------------------------------------------

export interface Identity { version: string; proto: number; factsV: number | null; platform: string; arch: string; osVersion: string | null; hostId: string }

/** The 版本與身分 card. Whether a Purdex daemon fronts the host is the component's (`host.daemonHostId`). */
export function identityOf(pool: PoolHost, facts: PoolFacts | null): Identity {
  return { version: pool.version, proto: pool.proto, factsV: facts?.v ?? null, platform: pool.platform, arch: pool.arch, osVersion: facts?.os_version ?? null, hostId: pool.host_id }
}

export type LabelGroup = 'task' | 'repo' | 'other'
export interface Tags {
  labels: { text: string; group: LabelGroup }[]
  /** `value` null: the host has none of it (a null, or an empty list). A list is joined with 、. */
  tools: { name: string; value: string | null }[]
  fingerprint: { name: string; value: string }[]
}

const labelGroup = (l: string): LabelGroup => (l.startsWith('task:') ? 'task' : l.startsWith('repo:') ? 'repo' : 'other')

/** The 標籤、工具與 fingerprint card. With no facts (the host is not ok) there are no tools to say; the labels and the fingerprint are the row's and stay. */
export function tagsOf(pool: PoolHost, facts: PoolFacts | null): Tags {
  return {
    labels: pool.labels.map((text) => ({ text, group: labelGroup(text) })),
    tools: facts ? Object.entries(facts.tools).map(([name, v]) => ({ name, value: v === null ? null : Array.isArray(v) ? (v.length > 0 ? v.join('、') : null) : v })) : [],
    fingerprint: Object.entries(pool.fingerprint ?? {}).map(([name, value]) => ({ name, value })),
  }
}

// ---- Every field of the record -------------------------------------------------------------------------------------------------------------------------------

/** One line of the facts table: the field's name as the wire has it, and its value as text. `value` null: the field is not there (drawn as a faint null, never 0). `wordKey` (with `wordParams`) is a word the component draws with it. */
export interface FactRow { key: string; value: string | null; wordKey?: string; wordParams?: Record<string, string | number> }

const pad2 = (n: number): string => String(n).padStart(2, '0')
const hms = (ms: number): string => { const d = new Date(ms); return `${pad2(d.getHours())}:${pad2(d.getMinutes())}:${pad2(d.getSeconds())}` }
const dayTime = (ms: number): string => { const d = new Date(ms); return `${d.getFullYear()}-${pad2(d.getMonth() + 1)}-${pad2(d.getDate())} ${pad2(d.getHours())}:${pad2(d.getMinutes())}` }
const f1 = (n: number): string => n.toFixed(1)
const orNull = (n: number | null | undefined, fmt: (x: number) => string = String): string | null => (n === null || n === undefined ? null : fmt(n))
const nullable = (n: number | null | undefined, fmt: (x: number) => string = String): string => orNull(n, fmt) ?? 'null'
const wordList = (codes: readonly string[]): string => `[${codes.join(', ')}]`

/**
 * Every field of the record, in the design's order (`page.js` factRows), then the host-metrics fields of 768 (G-2 to G-4) and the host row's own. The values are text; the words that go with a few of them are keys (`wordKey`).
 */
export function factRowsOf(pool: PoolHost, facts: PoolFacts): FactRow[] {
  const m = facts.mem
  const c = facts.cpu
  const rows: FactRow[] = [
    { key: 'v・host_id・alias', value: `${facts.v}・${facts.host_id}・${facts.alias}` },
    { key: 'at', value: hms(facts.at) },
    { key: 'platform・arch・os_version', value: `${facts.platform}・${facts.arch}・${facts.os_version}` },
    { key: 'cpu.ncpu', value: String(c.ncpu) },
    { key: 'cpu.idle_pct', value: `${f1(c.idle_pct)}%` },
    { key: 'cpu.load1・load5', value: `${f1(c.load1)}・${f1(c.load5)}` },
    { key: 'cpu.load15', value: orNull(c.load15, f1) },
    { key: 'cpu.user_pct・system_pct', value: c.user_pct === undefined && c.system_pct === undefined ? null : `${nullable(c.user_pct, (x) => `${f1(x)}%`)}・${nullable(c.system_pct, (x) => `${f1(x)}%`)}` },
    coresRow(c.cores),
    { key: 'mem.total_mb', value: String(m.total_mb) },
    { key: 'mem.available_mb', value: String(m.available_mb) },
    { key: 'mem.pressure', value: String(m.pressure), wordKey: `pool.level.${m.pressure}` },
    { key: 'mem.page_out_rate', value: orNull(m.page_out_rate, f1) },
    { key: 'mem.swap_out_rate', value: orNull(m.swap_out_rate, f1) },
    { key: 'mem.swap_used_mb', value: orNull(m.swap_used_mb) },
    { key: 'mem.compressor_mb', value: orNull(m.compressor_mb) },
    { key: 'mem.decompress_rate', value: orNull(m.decompress_rate, f1) },
    { key: 'mem.psi', value: m.psi ? `some10 ${m.psi.some10}・full10 ${m.psi.full10}・some60 ${m.psi.some60}` : null },
    { key: 'mem.swap_total_mb', value: orNull(m.swap_total_mb) },
    { key: 'mem.wired_mb', value: orNull(m.wired_mb) },
    { key: 'mem.active_mb', value: orNull(m.active_mb) },
    { key: 'mem.inactive_mb', value: orNull(m.inactive_mb) },
    { key: 'mem.file_cache_mb', value: orNull(m.file_cache_mb) },
    ...facts.disk.map((d, i): FactRow => ({ key: `disk[${i}] ${d.mount}`, value: `${f1(d.used_pct)}%・${f1(d.free_gb)} GB／${Math.round(d.total_bytes / 1e9)} GB` })),
    ...(facts.disk_io ?? []).map((d, i): FactRow => ({ key: `disk_io[${i}] ${d.device}`, value: [d.read_bps, d.write_bps, d.read_iops, d.write_iops].map((x) => nullable(x)).join('・') })),
    { key: 'procs[]', value: String(facts.procs.length) },
    { key: 'residents[]', value: `${facts.residents.length}・${facts.residents.reduce((n, r) => n + r.count, 0)}・${facts.residents.reduce((n, r) => n + r.mem_mb, 0)}` },
    { key: 'tools', value: String(Object.keys(facts.tools).length) },
    { key: 'owner', value: facts.owner ? `active ${facts.owner.active}・idle_s ${facts.owner.idle_s}・${facts.owner.reason}` : null },
    { key: 'exceptions[]', value: facts.exceptions.length > 0 ? facts.exceptions.map((e) => e.name).join('、') : '[]' },
    { key: 'derived.zone', value: facts.derived.zone },
    { key: 'derived.headroom', value: `heavy ${facts.derived.headroom.heavy}・light ${facts.derived.headroom.light}` },
    { key: 'derived.sessions_room', value: String(facts.derived.sessions_room) },
    { key: 'worker.enabled・slots・running', value: `${facts.worker.enabled}・${facts.worker.slots.heavy}/${facts.worker.slots.light}・${facts.worker.running.heavy}/${facts.worker.running.light}` },
    { key: 'worker.owner_rule', value: facts.worker.owner_rule === '' ? '""' : facts.worker.owner_rule },
    { key: 'queue.queued', value: orNull(facts.queue?.queued) },
    { key: 'procs_total', value: orNull(facts.procs_total) },
    { key: 'boot_at', value: orNull(facts.boot_at, dayTime) },
    { key: 'self', value: facts.self ? `${nullable(facts.self.cpu_pct, f1)}・${nullable(facts.self.rss_mb)}` : null },
    { key: 'info.labels・fingerprint', value: `${pool.labels.length}・${Object.keys(pool.fingerprint ?? {}).length}` },
    { key: 'headroom_reasons', value: pool.headroom_reasons ? `heavy ${wordList(pool.headroom_reasons.heavy)}・light ${wordList(pool.headroom_reasons.light)}` : null },
    { key: 'load', value: pool.load ? `score ${f1(pool.load.score)}・driver ${pool.load.driver}` : null },
    { key: 'reachable', value: String(pool.reachable) },
    { key: 'liveness・_at・_since', value: pool.liveness === undefined ? null : `${pool.liveness}・${orNull(pool.liveness_at, hms) ?? 'null'}・${orNull(pool.liveness_since, hms) ?? 'null'}` },
    { key: 'rtt_ms', value: orNull(pool.rtt_ms) },
  ]
  return rows
}

/** The core count, with how many are performance and how many efficiency where the platform says (all kinds null on Linux: just the count). */
function coresRow(cores: PoolFacts['cpu']['cores']): FactRow {
  if (!cores) return { key: 'cpu.cores', value: null }
  const p = cores.filter((x) => x.kind === 'p').length
  const e = cores.filter((x) => x.kind === 'e').length
  return p + e === 0 ? { key: 'cpu.cores', value: String(cores.length) } : { key: 'cpu.cores', value: String(cores.length), wordKey: 'watch.host.status.cores', wordParams: { n: cores.length, p, e } }
}
