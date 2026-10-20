// spa/src/lib/pool/host-status.ts — the host page's 狀態 tab as data (monitoring tabs spec M44; plan MON-7b-1): what the six cards and the facts table say, worked out from the pool's facts and host row. Pure: data in, data out; the words are
// i18n keys (as in words.ts) and the component calls `t`. Read from the pool only, never the fleet.
import { DISK_SCORE_FROM, levelScore, type RingDriver } from './ring-driver'
import type { PoolFacts, PoolHost, PoolLoad, PoolZone } from './types'

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
