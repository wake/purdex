// spa/src/lib/journal/parse.ts — the trust boundary of the journal wire. A malformed row (an unknown kind included) is
// dropped whole, never half-read, with one warning per kind of malformation per page load; a malformed envelope is `null`
// (the caller turns it into a bad_response). Nothing here is lenient: a field of the wrong type is a malformed row.
import {
  CHANGED_WHAT, ENTRY_KINDS, MEMBER_TASK_STATES, ROADMAP_STATUSES, TASK_STATES,
  type ChangedEvent, type ChangedWhat, type ConfirmItem, type EntryKind, type JournalEntry, type JournalPage, type JournalSessions, type SessionLink, type JournalMember, type JournalProject, type JournalTask, type MemberTaskState,
  type JournalUnit, type RemoteLead, type RoadmapFeature, type RoadmapStage, type RoadmapStatus, type TaskState,
} from './types'

type Rec = Record<string, unknown>
const isRec = (v: unknown): v is Rec => typeof v === 'object' && v !== null && !Array.isArray(v)
const isStr = (v: unknown): v is string => typeof v === 'string'
const isNonEmpty = (v: unknown): v is string => typeof v === 'string' && v !== ''
const isMs = (v: unknown): v is number => typeof v === 'number' && Number.isFinite(v) && v >= 0
const isCount = (v: unknown): v is number => typeof v === 'number' && Number.isSafeInteger(v) && v >= 0

const warned = new Set<string>()
export function warnOnce(what: string): void {
  if (warned.has(what)) return
  warned.add(what)
  console.warn(`[journal] ${what}`)
}
/** Test hook. */
export function resetWarned(): void { warned.clear() }

function strList(v: unknown): string[] | null {
  if (v === undefined || v === null) return []
  if (!Array.isArray(v) || !v.every(isStr)) return null
  return v as string[]
}

function parseItems(v: unknown): ConfirmItem[] | null {
  if (v === undefined || v === null) return []
  if (!Array.isArray(v)) return null
  const out: ConfirmItem[] = []
  for (const it of v) {
    if (!isRec(it) || !isStr(it.text) || typeof it.done !== 'boolean') return null
    out.push({ text: it.text, done: it.done })
  }
  return out
}

/** One entry, or the reason it is not one. */
export function parseEntry(v: unknown): JournalEntry | string {
  if (!isRec(v)) return 'entry is not an object'
  if (!isNonEmpty(v.id)) return 'entry.id is not a non-empty string'
  if (!isStr(v.kind) || !(ENTRY_KINDS as readonly string[]).includes(v.kind)) return `entry.kind is not a known kind`
  if (!isMs(v.at)) return 'entry.at is not a ms number'
  if (!isStr(v.actor)) return 'entry.actor is not a string'
  if (!isStr(v.text)) return 'entry.text is not a string'
  for (const k of ['by', 'detail']) if (v[k] !== undefined && v[k] !== null && !isStr(v[k])) return `entry.${k} is not a string`
  const refs = strList(v.refs)
  if (!refs) return 'entry.refs is not a list of strings'
  const items = parseItems(v.items)
  if (!items) return 'entry.items is not a list of {text, done}'
  return {
    id: v.id, kind: v.kind as EntryKind, by: (v.by as string | undefined) ?? '', at: v.at, actor: v.actor, text: v.text, refs, items,
    detail: (v.detail as string | undefined) ?? '',
  }
}

function parseEntryList(v: unknown): JournalEntry[] | null {
  if (!Array.isArray(v)) return null
  const out: JournalEntry[] = []
  for (const raw of v) {
    const e = parseEntry(raw)
    if (typeof e === 'string') { warnOnce(`dropping a row: ${e}`); continue }
    out.push(e)
  }
  return out
}

function parseUnit(v: unknown): JournalUnit | null {
  if (!isRec(v) || (v.kind !== 'team' && v.kind !== 'solo') || !isNonEmpty(v.id) || !isStr(v.label)) return null
  return { kind: v.kind, id: v.id, label: v.label }
}

function parseProject(v: unknown): JournalProject | null {
  if (!isRec(v)) return null
  for (const k of ['name', 'remote_url']) if (v[k] !== undefined && v[k] !== null && !isStr(v[k])) return null
  return { name: (v.name as string | undefined) ?? '', remoteUrl: (v.remote_url as string | undefined) ?? '' }
}

/** `GET /api/journal` → a page, or null when the envelope is not the wire shape. */
export function parseJournalPage(v: unknown): JournalPage | null {
  if (!isRec(v)) return null
  const unit = parseUnit(v.unit)
  const project = parseProject(v.project)
  if (!unit || !project) return null
  if (v.person_name !== undefined && v.person_name !== null && !isStr(v.person_name)) return null
  if (typeof v.more !== 'boolean' || !isCount(v.open_confirms)) return null
  const entries = parseEntryList(v.entries)
  const pinned = parseEntryList(v.pinned === undefined ? [] : v.pinned)
  const confirms = v.confirms === undefined || v.confirms === null ? null : parseEntryList(v.confirms)
  if (!entries || !pinned || (v.confirms !== undefined && v.confirms !== null && !confirms)) return null
  // `page` and `truncated` are omitempty: absent is '' / false, never an error and never a hint about the daemon's age.
  return {
    unit, project, personName: (v.person_name as string | undefined) ?? '', entries, confirms, pinned, more: v.more, openConfirms: v.open_confirms,
    page: isStr(v.page) ? v.page : '', truncated: v.truncated === true,
  }
}

const VIAS = ['root', 'relay', 'clear'] as const

/** `GET /api/journal/sessions` → the chain oldest first; null when the envelope is not the wire shape. */
export function parseSessions(v: unknown): JournalSessions | null {
  if (!isRec(v) || !Array.isArray(v.sessions)) return null
  const sessions: SessionLink[] = []
  for (const raw of v.sessions) {
    if (!isRec(raw) || !isNonEmpty(raw.session_id) || !isMs(raw.started_at) || !isMs(raw.ended_at) || typeof raw.current !== 'boolean'
      || !isStr(raw.via) || !(VIAS as readonly string[]).includes(raw.via)) { warnOnce('dropping a session: not {session_id, started_at, ended_at, via, current}'); continue }
    sessions.push({ sessionId: raw.session_id, startedAt: raw.started_at, endedAt: raw.ended_at, via: raw.via as SessionLink['via'], current: raw.current })
  }
  return { sessions, incomplete: v.incomplete === true }
}

function parseTask(v: unknown): JournalTask | string {
  if (!isRec(v)) return 'task is not an object'
  const number = typeof v.number === 'number' && Number.isSafeInteger(v.number) ? String(v.number) : v.number
  if (!isNonEmpty(number)) return 'task.number is not a number or a non-empty string'
  if (!isStr(v.title)) return 'task.title is not a string'
  if (!isStr(v.state) || !(TASK_STATES as readonly string[]).includes(v.state)) return 'task.state is not a known state'
  for (const k of ['owner', 'detail']) if (v[k] !== undefined && v[k] !== null && !isStr(v[k])) return `task.${k} is not a string`
  const refs = strList(v.refs)
  if (!refs) return 'task.refs is not a list of strings'
  return { number, title: v.title, state: v.state as TaskState, owner: (v.owner as string | undefined) ?? '', refs, detail: (v.detail as string | undefined) ?? '' }
}

/** `GET /api/journal/tasks` → `{tasks: […]}`; null when the envelope is not the wire shape. */
export function parseTasks(v: unknown): JournalTask[] | null {
  if (!isRec(v) || !Array.isArray(v.tasks)) return null
  const out: JournalTask[] = []
  for (const raw of v.tasks) {
    const t = parseTask(raw)
    if (typeof t === 'string') { warnOnce(`dropping a task: ${t}`); continue }
    out.push(t)
  }
  return out
}

function parseStage(v: unknown): RoadmapStage | string {
  if (!isRec(v) || !isStr(v.stage) || !isStr(v.description)) return 'stage is not {stage, description, status}'
  if (!isStr(v.status) || !(ROADMAP_STATUSES as readonly string[]).includes(v.status)) return 'stage.status is not a known status'
  return { stage: v.stage, description: v.description, status: v.status as RoadmapStatus }
}

/** `GET /api/journal/roadmap` → `{features: [{name, stages}]}`; null when the envelope is not the wire shape. */
export function parseRoadmap(v: unknown): RoadmapFeature[] | null {
  if (!isRec(v) || !Array.isArray(v.features)) return null
  const out: RoadmapFeature[] = []
  for (const f of v.features) {
    if (!isRec(f) || !isNonEmpty(f.name) || !Array.isArray(f.stages)) { warnOnce('dropping a feature: not {name, stages}'); continue }
    const stages: RoadmapStage[] = []
    for (const raw of f.stages) {
      const s = parseStage(raw)
      if (typeof s === 'string') { warnOnce(`dropping a stage: ${s}`); continue }
      stages.push(s)
    }
    out.push({ name: f.name, stages })
  }
  return out
}

/** A host frame's `value` is a JSON string; anything else is taken as already parsed. */
function parseJson(value: unknown): unknown {
  if (typeof value !== 'string') return value
  try { return JSON.parse(value) } catch { return undefined }
}

/** The `journal.changed` frame's value (a JSON string or an object), or the reason it is not one. */
export function parseChangedEvent(value: unknown): ChangedEvent | string {
  const v = parseJson(value)
  if (!isRec(v)) return 'journal.changed is not an object'
  const unit = isRec(v.unit) ? v.unit : null
  if (!unit || (unit.kind !== 'team' && unit.kind !== 'solo') || !isNonEmpty(unit.id)) return 'journal.changed.unit is not {kind, id}'
  if (!isStr(v.what) || !(CHANGED_WHAT as readonly string[]).includes(v.what)) return 'journal.changed.what is not entries / tasks / roadmap'
  return { unitKind: unit.kind, unitId: unit.id, what: v.what as ChangedWhat }
}

/** The 409 body of a member whose lead is on another daemon, or null. */
export function parseRemoteLead(body: unknown): RemoteLead | null {
  if (!isRec(body) || !isNonEmpty(body.lead_host_id) || !isNonEmpty(body.lead_session_id)) return null
  return { leadHostId: body.lead_host_id, leadSessionId: body.lead_session_id }
}

function parseMember(v: unknown): JournalMember | string {
  if (!isRec(v)) return 'member is not an object'
  if (!isNonEmpty(v.session_id)) return 'member.session_id is not a non-empty string'
  for (const k of ['ref', 'title']) if (v[k] !== undefined && v[k] !== null && !isStr(v[k])) return `member.${k} is not a string`
  let task: JournalMember['task'] = null
  if (v.task !== undefined && v.task !== null) {
    const t = v.task
    const number = isRec(t) && typeof t.number === 'number' && Number.isSafeInteger(t.number) ? String(t.number) : isRec(t) ? t.number : undefined
    if (!isRec(t) || !isNonEmpty(number) || !isStr(t.title)) return 'member.task is not {number, title, state}'
    if (!isStr(t.state) || !(MEMBER_TASK_STATES as readonly string[]).includes(t.state)) return 'member.task.state is not in_progress / pending'
    task = { number, title: t.title, state: t.state as MemberTaskState }
  }
  let last: JournalMember['last'] = null
  if (v.last !== undefined && v.last !== null) {
    const l = v.last
    if (!isRec(l) || !isMs(l.at) || !isStr(l.text) || (l.kind !== undefined && l.kind !== null && !isStr(l.kind))) return 'member.last is not {at, kind, text}'
    last = { at: l.at, kind: (l.kind as string | undefined) ?? '', text: l.text }
  }
  return { ref: (v.ref as string | undefined) ?? '', sessionId: v.session_id, title: (v.title as string | undefined) ?? '', task, last }
}

/** `GET /api/journal/members` -> `{members: [...]}`; null when the envelope is not the wire shape. A malformed row is dropped whole. */
export function parseJournalMembers(v: unknown): JournalMember[] | null {
  if (!isRec(v) || !Array.isArray(v.members)) return null
  const out: JournalMember[] = []
  for (const raw of v.members) {
    const m = parseMember(raw)
    if (typeof m === 'string') { warnOnce(`dropping a member: ${m}`); continue }
    out.push(m)
  }
  return out
}
