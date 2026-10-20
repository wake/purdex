// spa/src/components/fleet/HostBars.tsx — the bar indicator of a host cell (monitoring tabs M9): in the ring's square, one bar (`right` left out) or two, each one reading as its fill height, coloured by that
// reading's own band with the ring's status tokens (`muted`: grey, an unknown zone or a worker that is off). No reading: the grey tracks alone. The track is the ring's (`--border-default`).
// `bars` draws any number (the narrow sidebar's four, M14) in a box of its own size.
import type { IndicatorReading } from '../../lib/pool/indicator'

type BarReading = IndicatorReading

const FILL: Record<BarReading['tone'], string> = { ok: 'bg-status-success', warn: 'bg-status-warning', danger: 'bg-status-error', muted: 'bg-text-muted' }
const BAR_W = 4
const BAR_GAP = 3

function Bar({ side, r, h, w = BAR_W }: { side: string; r: BarReading | null; h: number; w?: number }) {
  return (
    <span
      data-testid={`host-bar-${side}`}
      data-pct={r?.pct}
      data-tone={r?.tone}
      className="relative block overflow-hidden rounded-full"
      style={{ width: w, height: h, background: 'var(--border-default)' }}
    >
      {r && r.pct > 0 && (
        <span
          data-testid="host-bar-fill"
          className={`absolute bottom-0 left-0 right-0 rounded-full ${FILL[r.tone]}`}
          style={{ height: Math.max(w, (r.pct / 100) * h) }}
        />
      )}
    </span>
  )
}

/** N bars (`bars`, ids `host-bar-0` …) side by side, each `barWidth` wide and `height` tall, `gap` apart. */
export function BarColumns({ bars, width, height, barWidth, gap }: { bars: (BarReading | null)[]; width: number; height: number; barWidth: number; gap: number }) {
  return (
    <span data-testid="host-bars" className="inline-flex items-center justify-center shrink-0" style={{ width, height, gap }}>
      {bars.map((r, i) => <Bar key={i} side={String(i)} r={r} h={height} w={barWidth} />)}
    </span>
  )
}

/** One bar (`right` undefined) or two in a `size` square (the ring's): each `size - 2` tall, like the ring's outer diameter. */
export function HostBars({ left, right, size }: { left: BarReading | null; right?: BarReading | null; size: number }) {
  const h = size - 2
  return (
    <span data-testid="host-bars" className="inline-flex items-center justify-center shrink-0" style={{ width: size, height: size, gap: BAR_GAP }}>
      <Bar side="left" r={left} h={h} />
      {right !== undefined && <Bar side="right" r={right} h={h} />}
    </span>
  )
}
