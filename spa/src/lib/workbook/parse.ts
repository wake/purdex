// spa/src/lib/workbook/parse.ts — the trust boundary of the workbook wire. A malformed entry is dropped whole (never
// half-read), and one kind of malformation warns once per page load of the app, not once per frame.
import type { ConversationPage, EntryEvent, EntryState, StatusEvent, WorkbookEntry } from './types'

type Rec = Record<string, unknown>
const isRec = (v: unknown): v is Rec => typeof v === 'object' && v !== null && !Array.isArray(v)
const isStr = (v: unknown): v is string => typeof v === 'string'
const isMs = (v: unknown): v is number => typeof v === 'number' && Number.isFinite(v) && v >= 0
const isId = (v: unknown): v is number => typeof v === 'number' && Number.isSafeInteger(v) && v > 0
const STATES: readonly string[] = ['pending', 'ok', 'failed', 'skipped']

const warned = new Set<string>()
export function warnOnce(what: string): void {
  if (warned.has(what)) return
  warned.add(what)
  console.warn(`[workbook] ${what}`)
}
/** Test hook: forget what was already warned. */
export function resetWarned(): void { warned.clear() }

/** One entry, or the reason it is not one. Optional text fields default to ''; the times are ms (never s). */
export function parseEntry(v: unknown): WorkbookEntry | string {
  if (!isRec(v)) return 'entry is not an object'
  if (!isId(v.id)) return 'entry.id is not a positive integer'
  if (!isStr(v.conv_key) || v.conv_key === '') return 'entry.conv_key is not a string'
  if (!isStr(v.session_id)) return 'entry.session_id is not a string'
  if (!isStr(v.state) || !STATES.includes(v.state)) return 'entry.state is not a known state'
  if (!isMs(v.created_at) || !isMs(v.updated_at)) return 'entry times are not ms numbers'
  if (v.turn_at !== undefined && !isMs(v.turn_at)) return 'entry.turn_at is not a ms number'
  for (const k of ['turn_id', 'reason', 'thing', 'push', 'entry']) {
    if (v[k] !== undefined && !isStr(v[k])) return `entry.${k} is not a string`
  }
  if (v.thing_done !== undefined && typeof v.thing_done !== 'boolean') return 'entry.thing_done is not a boolean'
  return {
    id: v.id, convKey: v.conv_key, sessionId: v.session_id, turnId: (v.turn_id as string | undefined) ?? '',
    turnAt: (v.turn_at as number | undefined) ?? 0, state: v.state as EntryState, reason: (v.reason as string | undefined) ?? '',
    thing: (v.thing as string | undefined) ?? '', push: (v.push as string | undefined) ?? '',
    entry: (v.entry as string | undefined) ?? '', thingDone: v.thing_done === true,
    createdAt: v.created_at, updatedAt: v.updated_at,
  }
}

/** A list of entries: the malformed ones are dropped (warning once per kind of malformation); a non-array is `[]`. */
export function parseEntries(v: unknown): WorkbookEntry[] {
  if (!Array.isArray(v)) return []
  const out: WorkbookEntry[] = []
  for (const raw of v) {
    const e = parseEntry(raw)
    if (typeof e === 'string') warnOnce(`dropping an entry: ${e}`)
    else out.push(e)
  }
  return out
}

/** The conversation answer; null when the envelope itself is not the wire shape. */
export function parseConversation(v: unknown): ConversationPage | null {
  if (!isRec(v) || !isStr(v.conv_key) || v.conv_key === '') return null
  if (v.status !== undefined && !isStr(v.status)) return null
  if (v.status_at !== undefined && !isMs(v.status_at)) return null
  return {
    convKey: v.conv_key, status: (v.status as string | undefined) ?? '', statusAt: (v.status_at as number | undefined) ?? 0,
    entries: parseEntries(v.entries),
  }
}

export function parseJson(value: unknown): unknown {
  if (typeof value !== 'string') return value
  try { return JSON.parse(value) } catch { return undefined }
}

export function parseEntryEvent(value: unknown): EntryEvent | string {
  const o = parseJson(value)
  if (!isRec(o) || !isStr(o.conv_key) || !isStr(o.session_id)) return 'workbook.entry: envelope is not the wire shape'
  const entry = parseEntry(o.entry)
  if (typeof entry === 'string') return `workbook.entry: ${entry}`
  if (entry.convKey !== o.conv_key) return 'workbook.entry: entry.conv_key differs from the envelope'
  return { convKey: o.conv_key, sessionId: o.session_id, entry }
}

export function parseStatusEvent(value: unknown): StatusEvent | string {
  const o = parseJson(value)
  if (!isRec(o) || !isStr(o.conv_key) || o.conv_key === '' || !isStr(o.session_id) || !isStr(o.status) || !isMs(o.updated_at)) {
    return 'workbook.status: not the wire shape'
  }
  return { convKey: o.conv_key, sessionId: o.session_id, status: o.status, updatedAt: o.updated_at }
}
