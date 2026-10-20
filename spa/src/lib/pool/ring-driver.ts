// spa/src/lib/pool/ring-driver.ts — which reading sets the ring's length: the daemon's composite (internal/fleet/load.go
// `Composite`) is the largest of CPU %, memory %, the level score (1 -> 30, 2 -> 75, 4 -> 100), the page rate capped at 100,
// and the disk % only from 85 %. The hover card marks that row. A tie goes to the first in this order.
import type { CellFast } from './cell-model'

export type RingDriver = 'cpu' | 'mem' | 'pressure' | 'paging' | 'disk'

export const DISK_SCORE_FROM = 85
const clamp100 = (v: number) => Math.min(Math.max(v, 0), 100)

export function levelScore(level: number): number {
  if (level >= 4) return 100
  if (level >= 2) return 75
  if (level === 1) return 30
  return 0
}

/** null when every score is 0 (all 0 or unknown): the ring is empty, so no row is marked. */
export function ringDriver(f: Pick<CellFast, 'cpu' | 'mem' | 'level' | 'page_rate' | 'disk'>): RingDriver | null {
  const scores: [RingDriver, number][] = [
    ['cpu', clamp100(f.cpu)],
    ['mem', clamp100(f.mem)],
    ['pressure', levelScore(f.level)],
    ['paging', clamp100(f.page_rate)],
    ['disk', f.disk >= DISK_SCORE_FROM ? clamp100(f.disk) : 0],
  ]
  let best = scores[0]
  for (const s of scores) if (s[1] > best[1]) best = s
  return best[1] > 0 ? best[0] : null
}
