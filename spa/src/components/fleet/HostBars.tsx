// spa/src/components/fleet/HostBars.tsx — the two-bar indicator of a fleet host cell (「主機格指示」 = 雙柱, the user 10-16 on
// #3033): in the ring's square, two bars, each one reading (lib/pool/bar-readings) as its fill height, coloured by that
// reading's own band with the ring's status tokens. No reading (offline, or none yet): the grey tracks alone. The track is the
// ring's (`--border-default`).
import type { UsageTone } from '../../lib/usage-display'
import type { BarReading } from '../../lib/pool/bar-readings'

const FILL: Record<UsageTone, string> = { ok: 'bg-status-success', warn: 'bg-status-warning', danger: 'bg-status-error' }
const BAR_W = 4
const BAR_GAP = 3

function Bar({ side, r, h }: { side: 'left' | 'right'; r: BarReading | null; h: number }) {
  return (
    <span
      data-testid={`host-bar-${side}`}
      data-pct={r?.pct}
      data-tone={r?.tone}
      className="relative block overflow-hidden rounded-full"
      style={{ width: BAR_W, height: h, background: 'var(--border-default)' }}
    >
      {r && r.pct > 0 && (
        <span
          data-testid="host-bar-fill"
          className={`absolute bottom-0 left-0 right-0 rounded-full ${FILL[r.tone]}`}
          style={{ height: Math.max(BAR_W, (r.pct / 100) * h) }}
        />
      )}
    </span>
  )
}

/** The two bars in a `size` square (the ring's): each `size - 2` tall, like the ring's outer diameter. */
export function HostBars({ left, right, size }: { left: BarReading | null; right: BarReading | null; size: number }) {
  const h = size - 2
  return (
    <span data-testid="host-bars" className="inline-flex items-center justify-center shrink-0" style={{ width: size, height: size, gap: BAR_GAP }}>
      <Bar side="left" r={left} h={h} />
      <Bar side="right" r={right} h={h} />
    </span>
  )
}
