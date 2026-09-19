// spa/src/lib/nex/format-cost.ts
// Pure formatting helpers for the exec pane cost summary (P-B4 spec §4.4).
//
// formatUsd(v, dp = 4)
//   '$' + v.toFixed(dp)                  '$0.0300', '$1.50' (dp 2)
//   negative / NaN / ±Infinity → '$—'
//
// formatTokens(n)
//   non-finite / negative → '—'
//   < 1000                → integer as is          '0', '364', '999'
//   otherwise             → k = n / 1000, one decimal, trailing '.0'
//                           dropped                '1k', '11.2k', '999.9k'
//   k rounds to ≥ 1000    → promote to M = n / 1e6, same one-decimal rule
//                                                   '1M', '1.2M'
//
// Unit promotion is decided AFTER rounding to one decimal, so '1000k'
// never appears: 999 950 → k = 999.95 → toFixed(1) = '1000.0' → promoted
// → 0.99995 M → '1.0' → '1M'. Anything in 999 950–999 999 renders '1M';
// 999 949 still renders '999.9k'.

const K = 1_000
const M = 1_000_000

/** One decimal with a trailing '.0' removed: 11.24 → '11.2', 1.0 → '1'. */
function oneDecimal(v: number): string {
  return v.toFixed(1).replace(/\.0$/, '')
}

export function formatUsd(v: number, dp = 4): string {
  if (!Number.isFinite(v) || v < 0) return '$—'
  return `$${v.toFixed(dp)}`
}

export function formatTokens(n: number): string {
  if (!Number.isFinite(n) || n < 0) return '—'
  if (n < K) return String(Math.round(n))

  const k = n / K
  // Compare the ROUNDED value so 999.95 (→ '1000.0') is promoted to M.
  if (Number(k.toFixed(1)) < K) return `${oneDecimal(k)}k`

  return `${oneDecimal(n / M)}M`
}
