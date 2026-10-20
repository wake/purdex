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
