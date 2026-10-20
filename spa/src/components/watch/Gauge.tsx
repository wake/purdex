// spa/src/components/watch/Gauge.tsx — one of the host tab's gauges (netdata easy-pie, ported from the approved prototype): 56 px,
// 4 px stroke, track --border-default, round cap, from 12 o'clock counterclockwise; the value in the middle. It draws a reading it is given (`lib/pool/host-charts.ts` makes them); a null reading (a host that is not ok, or one the host cannot give): — and no arc.
import type { GaugeView } from '../../lib/pool/host-charts'
import { ringTransform, type UsageTone } from '../../lib/usage-display'

const SIZE = 56
const STROKE = 4
const R = SIZE / 2 - 2
const C = 2 * Math.PI * R
const ARC: Record<UsageTone, string> = { ok: 'stroke-status-success-text', warn: 'stroke-status-warning-text', danger: 'stroke-status-error-text' }

export function Gauge({ label, reading: g, testId }: { label: string; reading: GaugeView | null; testId: string }) {
  const pct = g ? Math.max(0, Math.min(100, g.pct)) : 0
  return (
    <div className="flex flex-col items-center gap-1" data-testid={testId} data-tone={g ? g.tone : 'nodata'}>
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
      <span className="text-xs text-text-secondary">{label}</span>
    </div>
  )
}
