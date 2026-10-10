// spa/src/lib/workbook/types.ts — the session workbook on the wire (spec §9; daemon internal/module/workbook/api.go).
// Every time is unix milliseconds. Capabilities: `workbook.v1` (entries, status) and `workbook.v2` (WA-1b).

export const WORKBOOK_V1_CAPABILITY = 'workbook.v1'
export const WORKBOOK_V2_CAPABILITY = 'workbook.v2'

export const WORKBOOK_PROVIDER = 'claude'

export type EntryState = 'pending' | 'ok' | 'failed' | 'skipped'
/** v2: an entry is a turn's, or a manual refresh's. A v1 daemon sends no kind: every entry is a turn. */
export type EntryKind = 'turn' | 'refresh'

export interface TodoRef { id: number; title: string }
/** v2: what an entry did to the todo list (the mixed view). Empty on a v1 daemon. */
export interface TodoChanges { added: TodoRef[]; done: TodoRef[]; dropped: TodoRef[] }
export interface Usage { in: number; out: number; cacheRead: number }

export interface WorkbookEntry {
  id: number
  convKey: string
  sessionId: string
  turnId: string
  turnAt: number
  state: EntryState
  reason: string
  thing: string
  push: string
  entry: string
  thingDone: boolean
  createdAt: number
  updatedAt: number
  /** v2 (defaults: 'turn', zero usage, no changes — a v1 daemon sends none of them). */
  kind: EntryKind
  usage: Usage
  todoChanges: TodoChanges
}

export type TodoState = 'open' | 'done' | 'dropped'

/** v2: one todo of the conversation's list (read-only; times in ms, closed* are 0 while open). */
export interface WorkbookTodo {
  id: number
  title: string
  detail: string
  state: TodoState
  closedBy: string
  createdAt: number
  closedAt: number
  addedEntryId: number
  closedEntryId: number
}

/** The conversation answer's todos: `open` oldest first, `done` newest first (the newest 20). */
export interface TodoLists { open: WorkbookTodo[]; done: WorkbookTodo[] }

export interface ConversationPage {
  convKey: string
  status: string
  statusAt: number
  entries: WorkbookEntry[]
  /** null: the daemon sent none (v1, or a malformed block). */
  todos: TodoLists | null
  /** null: the daemon sent none (v1). */
  refreshAvailable: boolean | null
}

export interface EntryEvent { convKey: string; sessionId: string; entry: WorkbookEntry }
export interface StatusEvent { convKey: string; sessionId: string; status: string; updatedAt: number }
export interface TodosEvent { convKey: string; sessionId: string; todos: WorkbookTodo[] }
export interface RefreshAvailableEvent { convKey: string; available: boolean }
