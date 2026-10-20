// spa/src/lib/pool/pause.ts — the 「暫停」 the App adds to a host (wwd's time-bounded exception; plan "WWD-7 design", user Q6). The word 例外 never
// appears in the App. A pause is a name, an end time (ms since epoch) and what it holds (heavy, or heavy + light).
import type { PoolHold } from './types'

const HOUR_MS = 3_600_000
export const PAUSE_HOURS = [1, 3, 8] as const

/** The end time of "N 小時 from now". */
export const untilInHours = (now: number, hours: number): number => now + hours * HOUR_MS

/** `HH:mm` (24 h, the value of an `<input type="time">`) as hours and minutes, or null. */
export function parseClock(text: string): { h: number; m: number } | null {
  const m = /^([01]?\d|2[0-3]):([0-5]\d)$/.exec(text.trim())
  return m ? { h: Number(m[1]), m: Number(m[2]) } : null
}

/** The next time the wall clock reads `hh:mm` (local time): looked for minute by minute, so a repeated hour (clocks turned back) gives the first reading that is still
 *  ahead, and a reading that does not exist (clocks turned forward) gives the next one that does. null for text that is no clock time. */
export function untilClock(now: number, hhmm: string): number | null {
  const c = parseClock(hhmm)
  if (!c) return null
  const MINUTE = 60_000
  const first = (Math.floor(now / MINUTE) + 1) * MINUTE // a reading in the minute we are in is already past
  for (let t = first; t <= first + 26 * 60 * MINUTE; t += MINUTE) {
    const d = new Date(t)
    if (d.getHours() === c.h && d.getMinutes() === c.m) return t
  }
  return null
}

/** A name wwd accepts as one path segment (letters, digits, dashes) and no other pause has: the millisecond it was made, in base 36, and a few random characters
 *  (two Apps, or two presses in one millisecond, must not be the same pause: wwd would overwrite one with the other). `rand` is Math.random(). */
export const pauseName = (now: number, rand: number = Math.random()): string =>
  `app-pause-${Math.floor(now).toString(36)}-${Math.floor(rand * 36 ** 4).toString(36).padStart(4, '0')}`

/** The classes a pause can let through (wwd's `allow.classes`) and the marks it can carry (`flags`). */
export const PAUSE_CLASSES = ['release', 'merge', 'dev'] as const
export const PAUSE_FLAGS = ['release', 'expect_pressure'] as const

/** What to send as `allow`: nothing when no class is picked. wwd reads an `allow` that is there as "only the listed", so an empty one would hold everything. */
export const allowOf = (classes: readonly string[]): { classes: string[]; repos: string[] } | undefined =>
  classes.length === 0 ? undefined : { classes: [...classes], repos: [] }

export const holdsOf = (alsoLight: boolean): PoolHold[] => (alsoLight ? ['heavy', 'light'] : ['heavy'])

/** The custom end times last used, newest first, each once, at most `RECENT_MAX`. */
export const RECENT_MAX = 3
export function rememberClock(recent: readonly string[], hhmm: string): string[] {
  const c = parseClock(hhmm)
  if (!c) return [...recent]
  const norm = `${String(c.h).padStart(2, '0')}:${String(c.m).padStart(2, '0')}`
  return [norm, ...recent.filter((r) => r !== norm)].slice(0, RECENT_MAX)
}

const KEY = 'pdx:pool:recent-pause-clocks'
/** Per device, so localStorage; a browser that refuses it just remembers nothing. */
export function loadRecentClocks(): string[] {
  try {
    const raw: unknown = JSON.parse(localStorage.getItem(KEY) ?? '[]')
    return Array.isArray(raw) ? raw.filter((r): r is string => typeof r === 'string' && parseClock(r) !== null).slice(0, RECENT_MAX) : []
  } catch { return [] }
}
export function saveRecentClocks(recent: readonly string[]): void {
  try { localStorage.setItem(KEY, JSON.stringify(recent)) } catch { /* nothing to remember with */ }
}
