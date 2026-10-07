// spa/src/lib/nex/working-line.ts — pure parts of the room's "working" line
// (`✻ Whirlpooling… (1m 40s)`): the verb pick and the elapsed format. The
// verb is chosen once per turn by hashing the turn's key, so it never changes
// on a re-render and two panes on the same turn agree.

/** Verbs stay English in every locale, as the terminal's do. */
export const WORKING_VERBS = [
  'Thinking', 'Pondering', 'Whirlpooling', 'Cogitating', 'Brewing', 'Ruminating',
  'Musing', 'Churning', 'Simmering', 'Noodling', 'Percolating', 'Conjuring',
] as const

/** The glyph's frames, in the terminal's order. */
export const WORKING_FRAMES = ['·', '✢', '✳', '✶', '✻', '✽'] as const

export function pickWorkingVerb(turnKey: string): string {
  let h = 0
  for (let i = 0; i < turnKey.length; i++) h = (h * 31 + turnKey.charCodeAt(i)) >>> 0
  return WORKING_VERBS[h % WORKING_VERBS.length]
}

/** `7s`, `1m 40s`, `1h 02m` — whole seconds, never a decimal (the line ticks once a second). */
export function formatWorkingElapsed(ms: number): string {
  const total = Number.isFinite(ms) && ms > 0 ? Math.floor(ms / 1000) : 0
  if (total < 60) return `${total}s`
  const m = Math.floor(total / 60)
  if (m < 60) return `${m}m ${total % 60}s`
  return `${Math.floor(m / 60)}h ${String(m % 60).padStart(2, '0')}m`
}
