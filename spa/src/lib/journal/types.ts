// spa/src/lib/journal/types.ts — the workbook journal wire shapes (daemon plan §8, J-1e; spec §5–§7) in ONE place, so that
// matching the real J-1e answer later means editing this file and parse.ts only. J-1e is not built yet: every shape below is
// the contract as the plans state it (assumptions are marked ASSUMED), and the fixtures in the tests are written from it.
export const JOURNAL_CAPABILITY = 'journal.v1'

export const ENTRY_KINDS = ['request', 'task_new', 'task_done', 'exec', 'exec_done', 'comm', 'confirm'] as const
export type EntryKind = (typeof ENTRY_KINDS)[number]

export const ROADMAP_STATUSES = ['未開始', '進行中', '審查中', '完成', '暫停'] as const
export type RoadmapStatus = (typeof ROADMAP_STATUSES)[number]

export const TASK_STATES = ['open', 'done', 'dropped'] as const
export type TaskState = (typeof TASK_STATES)[number]

/** What a `journal.changed` event says changed. */
export const CHANGED_WHAT = ['entries', 'tasks', 'roadmap'] as const
export type ChangedWhat = (typeof CHANGED_WHAT)[number]

export interface JournalUnit { kind: 'team' | 'solo'; id: string; label: string }
/** ASSUMED: the daemon plan names only `remote_url?` inside `project`; `name` is kept when sent. */
export interface JournalProject { name: string; remoteUrl: string }
export interface ConfirmItem { text: string; done: boolean }

export interface JournalEntry {
  /** Opaque and unique within the unit (`wb:4812:1`). */
  id: string
  kind: EntryKind
  /** Who wrote it down for request-like kinds (`user` / `leader` / `member`); '' when the daemon sends none. */
  by: string
  /** Epoch milliseconds (ASSUMED: ms, like the workbook wire). */
  at: number
  actor: string
  text: string
  /** ASSUMED: display strings as written (`#2829`, `PR #2829`, a task number). */
  refs: string[]
  /** 待確認 only: one per question. */
  items: ConfirmItem[]
  /** 待確認 only: the original question text. */
  detail: string
  pinned: boolean
}

export interface JournalPage {
  unit: JournalUnit
  project: JournalProject
  personName: string
  /** Oldest first (the daemon sorts by (at, id); the store sorts again). */
  entries: JournalEntry[]
  /** Newest first. */
  pinned: JournalEntry[]
  /** There are older months before the oldest entry of this page. */
  more: boolean
  openConfirms: number
}

export interface JournalTask {
  /** Display number (`12`, or `7bcf08-12` for a team task). */
  number: string
  title: string
  state: TaskState
  owner: string
  refs: string[]
  detail: string
}

export interface RoadmapStage { stage: string; description: string; status: RoadmapStatus }
export interface RoadmapFeature { name: string; stages: RoadmapStage[] }

/** A `journal.changed` frame. ASSUMED: `unit` is `{kind, id}`; `project` is not needed to decide what to mark. */
export interface ChangedEvent { unitKind: 'team' | 'solo'; unitId: string; what: ChangedWhat }

/** `409 {lead_host_id, lead_session_id}`: this session is a member whose lead lives on another daemon. */
export interface RemoteLead { leadHostId: string; leadSessionId: string }

export const unitKeyOf = (u: Pick<JournalUnit, 'kind' | 'id'>): string => `${u.kind}:${u.id}`
