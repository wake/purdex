import { useI18nStore } from '../../stores/useI18nStore'
import { olderThanNewest } from '../../lib/pool/version'
import { usePoolHostRows, type PoolHostRow } from '../../lib/pool/use-pool-host-rows'
import { zoneKey } from '../../lib/pool/words'
import { selectProbeGuarded } from './useProbeEditor'
import { ProbeBadge } from './probe-rows'
import { PoolStatusDot } from './PoolStatusDot'

/**
 * 「資源池主機」 on the overview of a `fleet.v1` host once the resource pool is the source (plan WWD-7, 7a-3): the four hosts in a table —
 * status (light and zone word), Worker, what each takes, wwd's version (the ones behind the newest in yellow: adjacent versions are
 * compatible, so a mismatch is shown, not an error). Nothing is edited here; adding a host is installing wwd, not something this page does.
 */
export function PoolHostsSection() {
  const t = useI18nStore((s) => s.t)
  const { rows, active } = usePoolHostRows()
  if (!active || rows.length === 0) return null
  const behind = olderThanNewest(Object.fromEntries(rows.map((r) => [r.id, r.version])))

  return (
    <div data-testid="pool-hosts-section" className="mt-6">
      <h3 className="text-sm font-semibold mb-2">{t('pool_hosts.title')}</h3>
      <p className="text-xs text-text-muted mb-3">{t('pool_hosts.hint')}</p>
      <table className="w-full text-sm border border-border-subtle rounded-lg">
        <thead>
          <tr className="text-xs text-text-secondary text-left">
            <th className="px-3 py-1.5 font-normal">{t('pool_hosts.col.host')}</th>
            <th className="px-3 py-1.5 font-normal">{t('pool_hosts.col.status')}</th>
            <th className="px-3 py-1.5 font-normal">{t('pool_hosts.col.worker')}</th>
            <th className="px-3 py-1.5 font-normal">{t('pool_hosts.col.headroom')}</th>
            <th className="px-3 py-1.5 font-normal">{t('pool_hosts.col.version')}</th>
          </tr>
        </thead>
        <tbody className="divide-y divide-border-subtle">
          {rows.map((r) => <PoolHostTableRow key={r.id} row={r} behind={behind.has(r.id)} />)}
        </tbody>
      </table>
    </div>
  )
}

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

function PoolHostTableRow({ row: r, behind }: { row: PoolHostRow; behind: boolean }) {
  const t = useI18nStore((s) => s.t)
  const unknown = t('pool.card.unknown')
  const w = r.worker
  const open = () => { if (r.kind === 'probe') selectProbeGuarded({ id: r.id, isNew: false }) }
  return (
    <tr data-testid={`pool-host-row-${r.id}`} data-state={r.state} onClick={open} className={r.kind === 'probe' ? 'cursor-pointer hover:bg-surface-secondary/50' : undefined}>
      <td className="px-3 py-2"><span className="flex items-center gap-2"><ProbeBadge row={r.look as never} /><span className="truncate">{r.name}</span></span></td>
      <td className="px-3 py-2"><span className="flex items-center gap-2"><PoolStatusDot dot={r.dot} /><span data-testid="pool-host-status">{statusWord(t, r)}</span></span></td>
      <td data-testid="pool-host-worker" className="px-3 py-2">
        {!w ? unknown : w.enabled ? t('pool.card.worker_on', { hr: w.running.heavy, hs: w.slots.heavy, lr: w.running.light, ls: w.slots.light }) : t('pool.card.worker_off')}
      </td>
      <td data-testid="pool-host-headroom" className="px-3 py-2">{r.headroom ? t('pool.card.headroom_value', { h: r.headroom.heavy, l: r.headroom.light }) : unknown}</td>
      <td data-testid="pool-host-version" data-behind={behind ? 'true' : undefined} className={`px-3 py-2 tabular-nums ${behind ? 'text-status-warning-text' : ''}`} title={behind ? t('pool_hosts.version_behind') : undefined}>
        {r.version || '—'}
      </td>
    </tr>
  )
}
