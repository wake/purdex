// spa/src/components/hosts/pool-status-word.ts — the status words of a pool host row (its own module: a component file may only export components).
import { livenessWordOf, notOkLines, zoneKey } from '../../lib/pool/words'
import type { PoolHostRow } from '../../lib/pool/use-pool-host-rows'

/** The status words of a row: the zone while the pool reads the host, else the pool's own word for why it does not (stalled readings say where they stopped). */
export function statusWord(t: (k: string, p?: Record<string, string | number>) => string, r: PoolHostRow): string {
  if (r.state === 'ok') return t(zoneKey(r.zone ?? 'normal'))
  if (r.stalled) return notOkLines(t, r, Date.now())[0]
  return livenessWordOf(t, r.state)
}
