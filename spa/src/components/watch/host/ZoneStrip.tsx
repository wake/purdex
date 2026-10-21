// spa/src/components/watch/host/ZoneStrip.tsx — the zone strip above the 狀態 tab's full charts (monitoring tabs spec M44; plan MON-7c-2): one thin bar where each sample's zone (`HostSample.zone`, the pool's own) is placed by its time in
// [now − range, now], so it lines up with the charts below. A run of one zone is one segment; where nothing was read the bar is empty (a hole of more than 3 steps splits it), and after the host went it is slanted to the right edge (M22),
// like `MiniChart`. A zone the sample does not carry is drawn as unknown, never as normal.
import { useId } from 'react'
import { useI18nStore } from '../../../stores/useI18nStore'
import { RANGE_MS, readsFiveSecond, type HostRange, type HostSample } from '../../../lib/pool/host-history'

const BREAK_STEPS = 3
const ZONE_CLASS = { normal: 'bg-status-success', warning: 'bg-status-warning', danger: 'bg-status-error', unknown: 'bg-text-muted' } as const
const DEAD_COLOR = { warning: 'text-status-warning-text', error: 'text-status-error-text', muted: 'text-text-muted' } as const
type ZoneKey = keyof typeof ZONE_CLASS

interface Seg { zone: ZoneKey; from: number; to: number }

const zoneOf = (s: HostSample): ZoneKey => (s.zone === 'normal' || s.zone === 'warning' || s.zone === 'danger' ? s.zone : 'unknown')

export function ZoneStrip({ samples, range, now, dead }: { samples: readonly HostSample[]; range: HostRange; now: number; dead: { fromAt: number; tone: 'warning' | 'error' | 'muted' } | null }) {
  const t = useI18nStore((s) => s.t)
  const hatchId = useId()
  const R = RANGE_MS[range]
  const stepMs = readsFiveSecond(range) ? 5000 : 60_000
  const pct = (ms: number): number => Math.min(100, Math.max(0, ((ms - (now - R)) / R) * 100))

  // A segment runs from its first sample to the sample after its last (or, for the last, one step on), and is cut by a hole.
  const segs: Seg[] = []
  const drawn = samples.filter((s) => !dead || s.at <= dead.fromAt)
  drawn.forEach((s, i) => {
    const zone = zoneOf(s)
    const prev = segs[segs.length - 1]
    const gap = i > 0 ? s.at - drawn[i - 1].at : 0
    if (prev && prev.zone === zone && gap <= BREAK_STEPS * stepMs) prev.to = s.at + stepMs
    else segs.push({ zone, from: s.at, to: s.at + stepMs })
    if (prev && prev.to > s.at && prev.zone !== zone) prev.to = s.at
  })
  const deadFrom = dead ? pct(dead.fromAt) : null

  return (
    <div data-testid="zone-strip" className="flex items-center gap-2">
      <span className="w-12 shrink-0 text-[11px] text-text-muted">{t('watch.host.full.zone')}</span>
      <div className="relative h-2 flex-1 overflow-hidden rounded-sm">
        {segs.map((g) => {
          const left = pct(g.from)
          const right = pct(Math.min(g.to, now))
          return <i key={`${g.from}`} data-testid="zone-seg" data-zone={g.zone} data-left={left} className={`absolute inset-y-0 opacity-60 ${ZONE_CLASS[g.zone]}`} style={{ left: `${left}%`, width: `${Math.max(0.3, right - left)}%` }} />
        })}
        {dead && deadFrom !== null && deadFrom < 100 && (
          <svg data-testid="zone-dead" data-tone={dead.tone} data-from-pct={deadFrom} className={`absolute inset-y-0 ${DEAD_COLOR[dead.tone]}`} style={{ left: `${deadFrom}%`, width: `${100 - deadFrom}%` }} height="100%" aria-hidden="true">
            <defs><pattern id={hatchId} width="6" height="6" patternUnits="userSpaceOnUse" patternTransform="rotate(45)"><line x1="0" y1="0" x2="0" y2="6" stroke="currentColor" strokeWidth="1.5" /></pattern></defs>
            <rect width="100%" height="100%" fill={`url(#${hatchId})`} opacity="0.5" />
          </svg>
        )}
      </div>
    </div>
  )
}
