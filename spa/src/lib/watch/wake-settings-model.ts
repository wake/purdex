// spa/src/lib/watch/wake-settings-model.ts — the pure rules of editing the wake settings (plan §4.4): a field's chain of unsent changes and how it is walked against what the host
// says now, which field an error path points at, which words an error code gets, the times list, and the alert-kind rows. No store, no network, no screen.
import type { InvalidField, KnownKind } from './wake-settings'

// ---------- chains ----------

export type Phase = 'queued' | 'sent' | 'unsent'
/** One change to one whole field (an array is one value). `base` is the value the change expects the host to have; `value` is what it writes. */
export interface Link { base: unknown; value: unknown; phase: Phase; retryable?: boolean; uncertain?: boolean }

export function deepEqual(a: unknown, b: unknown): boolean {
  if (a === b) return true
  if (typeof a !== 'object' || typeof b !== 'object' || a === null || b === null) return false
  if (Array.isArray(a) !== Array.isArray(b)) return false
  const ka = Object.keys(a)
  if (ka.length !== Object.keys(b).length) return false
  return ka.every((k) => deepEqual((a as Record<string, unknown>)[k], (b as Record<string, unknown>)[k]))
}

export interface ChainWalk {
  /** Links that still stand, in order; the host is expected to have `base` of the first. */
  kept: Link[]
  /** Links the host already shows as done. */
  done: Link[]
  /** The links from the first one the host disagrees with to the end, folded into one: `theirs` is what the host has, `mine` the chain's last value. */
  conflict: { theirs: unknown; mine: unknown; links: Link[] } | null
}

/** Walk a field's chain against the host's value: a link whose `base` is what is expected stands (and its `value` is expected next); one whose `value` is what is expected is done; anything else is a conflict, with all that follows. */
export function walkChain(serverValue: unknown, links: Link[]): ChainWalk {
  const kept: Link[] = []
  const done: Link[] = []
  let expected = serverValue
  for (let i = 0; i < links.length; i++) {
    const link = links[i]
    if (deepEqual(link.base, expected)) {
      kept.push(link)
      expected = link.value
    } else if (deepEqual(link.value, expected)) {
      done.push(link)
    } else {
      const rest = links.slice(i)
      return { kept, done, conflict: { theirs: serverValue, mine: rest[rest.length - 1].value, links: rest } }
    }
  }
  return { kept, done, conflict: null }
}

/** Fold a chain that is not in flight into one link; one that ends where it began disappears. A chain with a link in flight, or whose first link is `uncertain`, is left as it is (the host may hold the first value). */
export function collapseChain(links: Link[]): Link[] {
  if (links.length < 2 || links.some((l) => l.phase === 'sent') || links[0].uncertain) return links
  const first = links[0]
  const last = links[links.length - 1]
  if (deepEqual(first.base, last.value)) return []
  return [{ ...first, value: last.value }]
}

// ---------- fields and error words ----------

const PATROL_SITUATIONS = ['resting', 'present', 'away', 'busy', 'calm']
export const FIELD_IDS: readonly string[] = [
  'enabled', 'auto_start',
  'patrol.prompt', ...PATROL_SITUATIONS.map((s) => `patrol.interval_min.${s}`), 'patrol.max_gap_hours', 'patrol.times', 'patrol.tz',
  'disk_low.enabled', 'disk_low.prompt', 'disk_low.below_gb',
  'wwd_alert.enabled', 'wwd_alert.prompt', 'wwd_alert.levels', 'wwd_alert.kinds_off',
  'unattended_changed.enabled', 'unattended_changed.prompt',
  'user_back.enabled', 'user_back.prompt', 'user_back.away_min',
]

/** The field a host path (`rules.patrol.times[0]`) belongs to: without `rules.` and without the index, so a member points at the whole array. `null` for a path that is no field here. */
export function fieldIdOfPath(path: string): string | null {
  const id = path.replace(/\[\d+\]/g, '').replace(/^rules\./, '')
  return FIELD_IDS.includes(id) ? id : null
}

const APP_MISMATCH_CODES = new Set(['missing_field', 'unknown_field', 'bad_type', 'bad_version'])
/** These codes mean the App built a document the host does not take: the whole write is a format mismatch, not a field's mistake. */
export const isAppMismatchCode = (code: string): boolean => APP_MISMATCH_CODES.has(code)
export const hasAppMismatch = (fields: InvalidField[]): boolean => fields.some((f) => isAppMismatchCode(f.code))

/** The i18n key for a refused value, from the host's code and path (the App goes by `code` only; the host's `message` is for `err.other`). */
export function errorKeyOf(code: string, path: string): string {
  const p = path.replace(/\[\d+\]/g, '')
  if (isAppMismatchCode(code)) return 'watch.wake.err.app_mismatch'
  switch (code) {
    case 'out_of_range':
      if (p.startsWith('rules.patrol.interval_min.')) return 'watch.wake.err.interval'
      if (p === 'rules.patrol.max_gap_hours') return 'watch.wake.err.max_gap'
      if (p === 'rules.disk_low.below_gb') return 'watch.wake.err.below_gb'
      if (p === 'rules.user_back.away_min') return 'watch.wake.err.away_min'
      return 'watch.wake.err.range'
    case 'bad_time': return 'watch.wake.err.bad_time'
    case 'duplicate_time': return 'watch.wake.err.duplicate_time'
    case 'too_many': return p === 'rules.wwd_alert.kinds_off' ? 'watch.wake.err.too_many_kinds' : 'watch.wake.err.too_many_times'
    case 'bad_level': return 'watch.wake.err.bad_level'
    case 'bad_kind': return 'watch.wake.err.bad_kind'
    case 'prompt_too_long': return 'watch.wake.err.prompt_too_long'
    case 'prompt_bad_char': return 'watch.wake.err.prompt_bad_char'
    case 'bad_tz': return 'watch.wake.err.bad_tz'
    default: return 'watch.wake.err.other'
  }
}

// ---------- fixed times ----------

export const MAX_TIMES = 24
const TIME = /^([01]\d|2[0-3]):[0-5]\d$/
export const isValidTime = (s: string): boolean => TIME.test(s)

export type AddTime = { ok: true; times: string[] } | { ok: false; error: 'bad_time' | 'duplicate_time' | 'too_many_times' }

/** Add a time to the list: sorted, with a bad format, a repeat or a 25th time refused here instead of sent. */
export function addTime(times: string[], raw: string): AddTime {
  const t = raw.trim()
  if (!isValidTime(t)) return { ok: false, error: 'bad_time' }
  if (times.includes(t)) return { ok: false, error: 'duplicate_time' }
  if (times.length >= MAX_TIMES) return { ok: false, error: 'too_many_times' }
  return { ok: true, times: [...times, t].sort() }
}

// ---------- alert kinds ----------

export interface KindRow { kind: string; label: string; levels: string[]; known: boolean; /** The alert is on (not in `kinds_off`). */ on: boolean; /** None of the kind's levels is on in `levels`: the row still works, it is only a hint. */ levelOff: boolean }

/** The known kinds first, then the values in `kinds_off` this App does not know (shown by their own text). */
export function kindRows(known: KnownKind[], kindsOff: string[], levels: string[]): KindRow[] {
  const rows: KindRow[] = known.map((k) => ({
    kind: k.kind, label: k.label, levels: k.levels, known: true, on: !kindsOff.includes(k.kind),
    levelOff: k.levels.length > 0 && !k.levels.some((l) => levels.includes(l)),
  }))
  const names = new Set(known.map((k) => k.kind))
  for (const off of kindsOff) {
    if (!names.has(off)) rows.push({ kind: off, label: off, levels: [], known: false, on: false, levelOff: false })
  }
  return rows
}

/** The whole new `kinds_off` for turning one kind on or off; the values around it (also those unknown here) stay. */
export function toggleKind(kindsOff: string[], kind: string, on: boolean): string[] {
  if (on) return kindsOff.filter((k) => k !== kind)
  return kindsOff.includes(kind) ? kindsOff : [...kindsOff, kind]
}
