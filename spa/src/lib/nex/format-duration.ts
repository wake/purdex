// spa/src/lib/nex/format-duration.ts
// Human-readable duration for tool timing badges (spec §4.4 R2).
//
//   < 60 s      → one-decimal seconds       '0.4s', '6.2s', '59.9s'
//   < 60 min    → 'Xm YYs' (zero-padded s)  '1m 05s', '12m 00s'
//   >= 60 min   → 'Xh YYm' (zero-padded m)  '1h 02m'
//   negative/NaN → '0.0s'
//
// Seconds are rounded to one decimal; when that rounding reaches 60.0
// (59 950–59 999 ms) the value is promoted to '1m 00s' so '60.0s' never
// appears.

const SECOND = 1_000
const MINUTE = 60 * SECOND
const HOUR = 60 * MINUTE

function pad2(n: number): string {
  return n < 10 ? `0${n}` : String(n)
}

export function formatDuration(ms: number): string {
  if (!Number.isFinite(ms) || ms < 0) return '0.0s'

  if (ms >= HOUR) {
    const hours = Math.floor(ms / HOUR)
    const minutes = Math.floor((ms % HOUR) / MINUTE)
    return `${hours}h ${pad2(minutes)}m`
  }

  if (ms < MINUTE) {
    const tenths = Math.round(ms / 100)
    if (tenths < 600) return `${(tenths / 10).toFixed(1)}s`
    // rounded up to 60.0s — fall through as one full minute
    ms = MINUTE
  }

  const minutes = Math.floor(ms / MINUTE)
  const seconds = Math.floor((ms % MINUTE) / SECOND)
  return `${minutes}m ${pad2(seconds)}s`
}
