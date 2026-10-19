// spa/src/components/watch/ZoneBand.tsx — a thin band above the host tab's charts (5 minute range): each sample coloured by its zone, so the
// stretches that ran high show at a glance. Same row grid and viewBox as the charts, so it lines up with them.
import { useI18nStore } from '../../stores/useI18nStore'
import type { PoolZone } from '../../lib/pool/types'
import type { ZonePoint } from '../../lib/pool/history'
import { RANGE_MS } from '../../lib/watch/series'
import { ROW } from './MetricChart'

const W = 400
const BAND_H = 8
/** How long the last sample stands for (wwd samples every 5 s). */
const SAMPLE_MS = 5000
const ZONE_CLASS: Record<PoolZone, string> = {
  normal: 'text-status-success-text', warning: 'text-status-warning-text', danger: 'text-status-error-text', unknown: 'text-text-muted',
}
const clamp01 = (n: number) => Math.min(1, Math.max(0, n))

export function ZoneBand({ points, now }: { points: ZonePoint[]; now: number }) {
  const t = useI18nStore((s) => s.t)
  if (points.length === 0) return null
  const span = RANGE_MS['5m']
  const from = now - span
  const segs = points
    .map((p, i) => {
      const end = i + 1 < points.length ? points[i + 1].t : Math.min(now, p.t + SAMPLE_MS)
      return { zone: p.zone, x0: clamp01((p.t - from) / span) * W, x1: clamp01((end - from) / span) * W }
    })
    .filter((s) => s.x1 > s.x0)
  return (
    <div className={`${ROW} py-1`} data-testid="zone-band" role="img" aria-label={t('watch.host.zone_band')}>
      <span className="text-xs text-text-secondary">{t('watch.host.zone_band')}</span>
      <svg viewBox={`0 0 ${W} ${BAND_H}`} preserveAspectRatio="none" className="w-full block" style={{ height: BAND_H }} aria-hidden="true">
        {segs.map((s, i) => (
          <rect key={i} data-testid="zone-seg" data-zone={s.zone} className={ZONE_CLASS[s.zone]} x={s.x0} width={s.x1 - s.x0} y={0} height={BAND_H} fill="currentColor" fillOpacity={s.zone === 'normal' ? 0.45 : 0.95} />
        ))}
      </svg>
      <span />
    </div>
  )
}
