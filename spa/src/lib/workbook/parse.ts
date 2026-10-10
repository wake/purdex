// spa/src/lib/workbook/parse.ts — the trust boundary of the workbook wire. A malformed entry is dropped whole (never
// half-read), and one kind of malformation warns once per page load of the app, not once per frame.
import type {
  ConversationPage, EntryEvent, EntryKind, EntryState, RefreshAvailableEvent, StatusEvent, TodoChanges, TodoLists, TodosEvent, TodoState, Usage,
  WorkbookEntry, WorkbookTodo,
} from './types'

type Rec = Record<string, unknown>
const isRec = (v: unknown): v is Rec => typeof v === 'object' && v !== null && !Array.isArray(v)
const isStr = (v: unknown): v is string => typeof v === 'string'
const isMs = (v: unknown): v is number => typeof v === 'number' && Number.isFinite(v) && v >= 0
const isId = (v: unknown): v is number => typeof v === 'number' && Number.isSafeInteger(v) && v > 0
const STATES: readonly string[] = ['pending', 'ok', 'failed', 'skipped']
const KINDS: readonly string[] = ['turn', 'refresh']
const TODO_STATES: readonly string[] = ['open', 'done', 'dropped']

const warned = new Set<string>()
export function warnOnce(what: string): void {
  if (warned.has(what)) return
  warned.add(what)
  console.warn(`[workbook] ${what}`)
}
/** Test hook: forget what was already warned. */
export function resetWarned(): void { warned.clear() }

function parseUsage(v: unknown): Usage | string {
  if (v === undefined || v === null) return { in: 0, out: 0, cacheRead: 0 }
  if (!isRec(v)) return 'entry.usage is not an object'
  for (const k of ['in', 'out', 'cache_read']) {
    if (v[k] !== undefined && !isMs(v[k])) return `entry.usage.${k} is not a count`
  }
  return { in: (v.in as number | undefined) ?? 0, out: (v.out as number | undefined) ?? 0, cacheRead: (v.cache_read as number | undefined) ?? 0 }
}

function parseRefs(v: unknown, what: string): { id: number; title: string }[] | string {
  if (v === undefined || v === null) return []
  if (!Array.isArray(v)) return `entry.todo_changes.${what} is not a list`
  const out: { id: number; title: string }[] = []
  for (const r of v) {
    if (!isRec(r) || !isId(r.id) || !isStr(r.title)) return `entry.todo_changes.${what} holds a malformed item`
    out.push({ id: r.id, title: r.title })
  }
  return out
}

function parseTodoChanges(v: unknown): TodoChanges | string {
  if (v === undefined || v === null) return { added: [], done: [], dropped: [] }
  if (!isRec(v)) return 'entry.todo_changes is not an object'
  const added = parseRefs(v.added, 'added')
  if (typeof added === 'string') return added
  const done = parseRefs(v.done, 'done')
  if (typeof done === 'string') return done
  const dropped = parseRefs(v.dropped, 'dropped')
  if (typeof dropped === 'string') return dropped
  return { added, done, dropped }
}

/** One todo (v2), or the reason it is not one. */
export function parseTodo(v: unknown): WorkbookTodo | string {
  if (!isRec(v)) return 'todo is not an object'
  if (!isId(v.id)) return 'todo.id is not a positive integer'
  if (!isStr(v.title)) return 'todo.title is not a string'
  if (!isStr(v.state) || !TODO_STATES.includes(v.state)) return 'todo.state is not a known state'
  if (!isMs(v.created_at)) return 'todo.created_at is not a ms number'
  if (v.closed_at !== undefined && !isMs(v.closed_at)) return 'todo.closed_at is not a ms number'
  for (const k of ['detail', 'closed_by']) {
    if (v[k] !== undefined && !isStr(v[k])) return `todo.${k} is not a string`
  }
  for (const k of ['added_entry_id', 'closed_entry_id']) {
    if (v[k] !== undefined && !isMs(v[k])) return `todo.${k} is not an id`
  }
  return {
    id: v.id, title: v.title, state: v.state as TodoState, detail: (v.detail as string | undefined) ?? '', closedBy: (v.closed_by as string | undefined) ?? '',
    createdAt: v.created_at, closedAt: (v.closed_at as number | undefined) ?? 0,
    addedEntryId: (v.added_entry_id as number | undefined) ?? 0, closedEntryId: (v.closed_entry_id as number | undefined) ?? 0,
  }
}

/** A list of todos: the malformed ones are dropped (warning once per kind of malformation); a non-array is `[]`. */
export function parseTodos(v: unknown): WorkbookTodo[] {
  if (!Array.isArray(v)) return []
  const out: WorkbookTodo[] = []
  for (const raw of v) {
    const t = parseTodo(raw)
    if (typeof t === 'string') warnOnce(`dropping a todo: ${t}`)
    else out.push(t)
  }
  return out
}

function parseTodoLists(v: unknown): TodoLists | null {
  if (v === undefined) return null // a v1 daemon
  if (!isRec(v) || (v.open !== undefined && !Array.isArray(v.open)) || (v.done !== undefined && !Array.isArray(v.done))) {
    warnOnce('ignoring the conversation answer\'s todos: not the wire shape')
    return null
  }
  return { open: parseTodos(v.open), done: parseTodos(v.done) }
}

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
  // v2 fields: every one may be absent (a v1 daemon), none may be present and wrong.
  if (v.kind !== undefined && !KINDS.includes(v.kind as string)) return 'entry.kind is not a known kind'
  const usage = parseUsage(v.usage)
  if (typeof usage === 'string') return usage
  const todoChanges = parseTodoChanges(v.todo_changes)
  if (typeof todoChanges === 'string') return todoChanges
  return {
    kind: (v.kind as EntryKind | undefined) ?? 'turn', usage, todoChanges,
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
  // One conversation per answer. Checked on the RAW entries, before malformed ones are dropped: an entry that names another
  // conversation taints the whole answer even if it is also malformed in some other field.
  if (Array.isArray(v.entries) && v.entries.some((e) => isRec(e) && e.conv_key !== undefined && e.conv_key !== v.conv_key)) {
    warnOnce('rejecting a conversation answer: an entry.conv_key differs from the envelope')
    return null
  }
  const entries = parseEntries(v.entries)
  let refreshAvailable: boolean | null = null
  if (typeof v.refresh_available === 'boolean') refreshAvailable = v.refresh_available
  else if (v.refresh_available !== undefined) warnOnce('ignoring the conversation answer\'s refresh_available: not a boolean')
  return {
    convKey: v.conv_key, status: (v.status as string | undefined) ?? '', statusAt: (v.status_at as number | undefined) ?? 0, entries,
    todos: parseTodoLists(v.todos), refreshAvailable,
  }
}

/** The todos route's answer `{todos: [...]}`; null when it is not that shape. */
export function parseTodosPage(v: unknown): WorkbookTodo[] | null {
  return isRec(v) && Array.isArray(v.todos) ? parseTodos(v.todos) : null
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

export function parseTodosEvent(value: unknown): TodosEvent | string {
  const o = parseJson(value)
  if (!isRec(o) || !isStr(o.conv_key) || o.conv_key === '' || !isStr(o.session_id) || !Array.isArray(o.todos)) return 'workbook.todos: not the wire shape'
  return { convKey: o.conv_key, sessionId: o.session_id, todos: parseTodos(o.todos) }
}

export function parseRefreshAvailableEvent(value: unknown): RefreshAvailableEvent | string {
  const o = parseJson(value)
  if (!isRec(o) || !isStr(o.conv_key) || o.conv_key === '' || typeof o.available !== 'boolean') return 'workbook.refresh_available: not the wire shape'
  return { convKey: o.conv_key, available: o.available }
}

export function parseStatusEvent(value: unknown): StatusEvent | string {
  const o = parseJson(value)
  if (!isRec(o) || !isStr(o.conv_key) || o.conv_key === '' || !isStr(o.session_id) || !isStr(o.status) || !isMs(o.updated_at)) {
    return 'workbook.status: not the wire shape'
  }
  return { convKey: o.conv_key, sessionId: o.session_id, status: o.status, updatedAt: o.updated_at }
}
