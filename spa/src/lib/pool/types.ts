// spa/src/lib/pool/types.ts — the App's view of `/api/pool/*` (wwd spec §3, internal/wwd/wire). Times are ms since epoch.
export const ZONES = ['normal', 'warning', 'danger', 'unknown'] as const
export type PoolZone = (typeof ZONES)[number]

export interface Slots { heavy: number; light: number }

export interface PoolWorker { enabled: boolean; slots: Slots; owner_rule: string }
export interface FactsWorker extends PoolWorker { running: Slots }

export interface ProcOwner { kind: string; id?: string; session?: string }
export interface PoolProc { pid: number; name: string; cpu_pct: number; mem_mb: number; mem_kind: string; owner: ProcOwner | null }
export interface PoolResident { session: string; kind: string; count: number; mem_mb: number }
export interface PoolDisk { mount: string; free_gb: number; used_pct: number; total_bytes: number; avail_bytes: number }
export interface PoolPsi { some10: number; full10: number; some60: number }
export interface PoolException { name: string; since: number; until: number | null }
export type PoolHold = 'heavy' | 'light'
/** One entry of `GET /api/pool/hosts/{alias}/exceptions` (`pool.control.v1`'s list, #3479): a pause with what it holds. `until` and `pid`: exactly one is set. */
export interface PoolExceptionDetail {
  name: string; until: number | null; pid: number | null; holds: PoolHold[]
  /** What the pause lets through (wwd's `allow`): absent when it lets nothing through. */
  allow?: { classes: string[]; repos: string[] }
  flags?: string[]
}
/** One core's shares of its own time, each null on its own; `kind` is `p` (performance) or `e` (efficiency), null when the platform does not say. */
export interface PoolCore { user_pct: number | null; system_pct: number | null; idle_pct: number | null; kind: 'p' | 'e' | null }
/** One physical disk's throughput since the previous sample; a rate is null where there was nothing to compare with. */
export interface PoolDiskIO { device: string; read_bps: number | null; write_bps: number | null; read_iops: number | null; write_iops: number | null }
export interface PoolSelf { cpu_pct: number | null; rss_mb: number | null }
export interface PoolOwner { active: boolean; idle_s: number; reason: string }

export interface PoolFacts {
  /** The wire version of this record (1 or 2). */
  v: number
  host_id: string
  alias: string
  at: number
  platform: string
  arch: string
  os_version: string
  // The fields marked optional are the host-metrics spec's (G-2 to G-4); each is absent when the daemon does not send it or it does not parse.
  cpu: { ncpu: number; load1: number; load5: number; idle_pct: number; load15?: number; user_pct?: number; system_pct?: number; cores?: PoolCore[] }
  mem: {
    total_mb: number; available_mb: number; pressure: number
    page_out_rate: number | null; swap_out_rate: number | null; swap_used_mb: number | null; compressor_mb: number | null
    psi: PoolPsi | null
    decompress_rate?: number; swap_total_mb?: number; wired_mb?: number; active_mb?: number; inactive_mb?: number; file_cache_mb?: number
  }
  disk: PoolDisk[]
  procs: PoolProc[]
  residents: PoolResident[]
  tools: Record<string, string | string[] | null>
  owner: PoolOwner | null
  exceptions: PoolException[]
  derived: { zone: PoolZone; headroom: Slots; sessions_room: number }
  worker: FactsWorker
  /** Jobs this host submitted that are still queued (filled from 756); absent until a daemon sends it. */
  queue?: { queued: number }
  /** The raw count of the host's process table, when it booted (ms), wwd's own use, and one entry per physical disk. */
  procs_total?: number
  boot_at?: number
  self?: PoolSelf
  disk_io?: PoolDiskIO[]
}

/** Why a class of work is not taken, in spec §3.2 order (#3478, `pool.assess.v1`). A code the App does not know stays text. */
export interface HeadroomReasons { heavy: string[]; light: string[] }
/** `driver` is cpu | mem | pressure | paging | disk today; a newer one stays text. */
export interface PoolLoad { score: number; driver: string }

/** What the daemon's own probe of a peer says (`liveness`, #3676): wwd answers, wwd does not but the machine does, neither, or not checked yet. */
export const LIVENESS = ['ok', 'wwd_down', 'host_down', 'unknown'] as const
export type PoolLiveness = (typeof LIVENESS)[number]

export interface PoolHost {
  host_id: string
  alias: string
  version: string
  proto: number
  platform: string
  arch: string
  facts: PoolFacts | null
  worker: PoolWorker
  labels: string[]
  reachable: boolean
  headroom_reasons?: HeadroomReasons
  load?: PoolLoad
  /** Only a peer row carries these (the local host has no probe of itself); each is absent when the daemon sent none or it did not parse. */
  liveness?: PoolLiveness
  /** When the probe last ran (ms). */
  liveness_at?: number
  /** Since when `liveness` has had this value (ms). */
  liveness_since?: number
  /** Round trip to the peer's wwd (ms), a non-negative number. */
  rtt_ms?: number
  /** `info.fingerprint`: what the host says it is (strings only); absent when it sent none or nothing in it parsed. */
  fingerprint?: Record<string, string>
}

/** `GET /api/pool/facts/history`: the full facts records, oldest first, at the asked step. */
export interface PoolFactsHistory { step: '1m' | '5s'; records: PoolFacts[] }

export interface SeriesPoint {
  at: number
  zone: PoolZone
  cpu_idle_pct: number
  mem_available_mb: number
  pressure: number
  page_out_rate: number | null
  disk_max_used_pct: number | null
  headroom: Slots
  // G-1 (wwd host-metrics spec §2.3). Each is absent when the daemon does not send it or it does not parse; null and absent mean the same.
  load1?: number
  load5?: number
  swap_used_mb?: number
  swap_out_rate?: number
  worker_running?: Slots
  queue_queued?: number
  disks?: SeriesDisk[]
  // G-2 to G-4 (host-metrics spec §2.5). Not in a series: the per-core shares, the pressure stall figures, the sessions room, the residents, the listed processes and the boot time.
  load15?: number
  cpu_user_pct?: number
  cpu_system_pct?: number
  compressor_mb?: number
  decompress_rate?: number
  swap_total_mb?: number
  procs_total?: number
  self_cpu_pct?: number
  self_rss_mb?: number
  disk_io?: PoolDiskIO[]
}
export interface SeriesDisk { mount: string; used_pct: number; free_gb: number }
export interface PoolSeries { step: string; points: SeriesPoint[]; truncated: boolean }
