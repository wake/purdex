// spa/src/components/watch/Gauge.tsx — one of the host tab's five gauges (netdata easy-pie, ported from the approved prototype): 56 px,
// 4 px stroke, track --border-default, round cap, from 12 o'clock counterclockwise; the value in the middle. Offline: — and no arc.
import type { FleetFast } from '../../lib/fleet/types'
import { ringTransform } from '../../lib/usage-display'
import { gaugeOf, type MetricDef, type Tone } from '../../lib/watch/metrics'
import { useI18nStore } from '../../stores/useI18nStore'

const SIZE = 56
const STROKE = 4
const R = SIZE / 2 - 2
const C = 2 * Math.PI * R
const ARC: Record<Tone, string> = { ok: 'stroke-text-secondary', warn: 'stroke-status-warning', danger: 'stroke-status-error' }

export function Gauge({ def, fast, offline }: { def: MetricDef; fast: FleetFast | null; offline: boolean }) {
  const t = useI18nStore((s) => s.t)
  const g = offline || !fast ? null : gaugeOf(def.k, fast, t)
  const pct = g ? Math.max(0, Math.min(100, g.pct)) : 0
  return (
    <div className="flex flex-col items-center gap-1" data-testid={`gauge-${def.k}`} data-tone={offline ? 'offline' : g ? g.tone : 'nodata'}>
      <span className="relative inline-grid" style={{ width: SIZE, height: SIZE }}>
        <svg width={SIZE} height={SIZE} viewBox={`0 0 ${SIZE} ${SIZE}`} className="absolute inset-0 block" aria-hidden="true">
          <circle cx={SIZE / 2} cy={SIZE / 2} r={R} fill="none" stroke="var(--border-default)" strokeWidth={STROKE} />
          {g && pct > 0 && (
            <circle
              data-testid="gauge-arc" className={ARC[g.tone]} cx={SIZE / 2} cy={SIZE / 2} r={R} fill="none" strokeWidth={STROKE} strokeLinecap="round"
              strokeDasharray={`${((pct / 100) * C).toFixed(2)} ${C.toFixed(2)}`} transform={ringTransform(SIZE, 'ccw')}
            />
          )}
        </svg>
        <span className="absolute inset-0 flex flex-col items-center justify-center leading-none">
          <span className="text-sm font-semibold tabular-nums text-text-primary">{g ? g.value : '—'}</span>
          {g?.unit && <span className="mt-0.5 text-[10px] text-text-muted">{g.unit}</span>}
        </span>
      </span>
      <span className="text-xs text-text-secondary">{t(def.shortKey)}</span>
    </div>
  )
}
