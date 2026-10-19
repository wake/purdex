// spa/src/components/hosts/pool-status-word.ts — the status words of a pool host row (its own module: a component file may only export components).
import { zoneKey } from '../../lib/pool/words'
import type { PoolHostRow } from '../../lib/pool/use-pool-host-rows'

/** The status words of a row: the zone while the pool reads the host, else why it does not. */
export function statusWord(t: (k: string, p?: Record<string, string | number>) => string, r: PoolHostRow): string {
  if (r.state === 'live') return t(zoneKey(r.zone ?? 'normal'))
  if (r.state === 'fallback') {
    return r.since !== null
      ? t('pool.card.fallback', { time: new Date(r.since).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' }) })
      : t('pool.card.fallback_nosince')
  }
  return r.dot === 'error' ? t('fleet_hosts.status.error') : t('fleet.zone.offline')
}
