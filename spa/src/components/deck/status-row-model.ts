// spa/src/components/deck/status-row-model.ts — the pure side of the status row (U3-5): the compact phrases it prints and the
// container-query classes that decide which part drops out at which width. The classes live here, as one table, so a test can
// lock every breakpoint (jsdom does not lay out; the real behaviour is checked in Chromium).

/** Compact reset phrase: `45m`, `2h`, `3d`; null once the reset has passed or there is none. */
export function shortReset(resetsAtMs: number | null | undefined, nowMs: number): string | null {
  if (typeof resetsAtMs !== 'number') return null
  const mins = Math.floor((resetsAtMs - nowMs) / 60_000)
  if (mins < 0) return null
  if (mins < 60) return `${mins}m`
  if (mins < 24 * 60) return `${Math.floor(mins / 60)}h`
  return `${Math.floor(mins / (24 * 60))}d`
}

/** 620000 → `620K`, 1_000_000 → `1M`, 1_500_000 → `1.5M`, 800 → `800`. */
export function formatTokens(n: number): string {
  if (n >= 1_000_000) return `${Number((n / 1_000_000).toFixed(1))}M`
  if (n >= 1000) return `${Math.round(n / 1000)}K`
  return String(Math.round(n))
}

/** `Opus 5.5 (1M)` → name `Opus 5.5`, window `(1M)`. */
export function splitModel(model: string): { name: string; window: string | null } {
  const m = /^(.*?)\s*(\(\d+(?:\.\d+)?[KM]\))$/.exec(model.trim())
  return m ? { name: m[1], window: m[2] } : { name: model.trim(), window: null }
}

/** Tokens left in a window of `total`, given the used share. */
export function tokensLeft(totalTokens: number, usedPct: number): number {
  return Math.round(totalTokens * (100 - Math.min(100, Math.max(0, usedPct))) / 100)
}

/**
 * Hide-at-width classes, on the row's own `@container` (so the row follows its pane, not the window). Narrowing order:
 * cost (≤640) → 7d (≤560) → (1M) (≤520) → context tokens (≤500) → reset times (≤460) → effort (≤400) → 5h (≤360).
 * Context and the state cell never drop; at 412 the row still shows state · model · context · 5h.
 */
export const HIDE = {
  cost: '@max-[640px]:hidden',
  sevenDay: '@max-[560px]:hidden',
  modelWindow: '@max-[520px]:hidden',
  ctxTokens: '@max-[500px]:hidden',
  reset: '@max-[460px]:hidden',
  effort: '@max-[400px]:hidden',
  fiveHour: '@max-[360px]:hidden',
  modelSep: '@max-[330px]:hidden',
} as const

/** `0:42`, `12:05`. */
export function formatElapsed(ms: number): string {
  const s = Math.max(0, Math.floor(ms / 1000))
  return `${Math.floor(s / 60)}:${String(s % 60).padStart(2, '0')}`
}
