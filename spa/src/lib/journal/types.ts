// spa/src/lib/journal/types.ts — the workbook journal wire shapes (daemon plan §8, J-1e; spec §5–§7) in ONE place, so that
// matching the real J-1e answer later means editing this file and parse.ts only. J-1e is not built yet: every shape below is
// the contract as the plans state it (assumptions are marked ASSUMED), and the fixtures in the tests are written from it.
export const JOURNAL_CAPABILITY = 'journal.v1'
/** Hand checks (`PUT /api/journal/confirm`) and the page's `confirms[]` band (JV-1a). */
export const JOURNAL_CONFIRM_CAPABILITY = 'journal.confirm.v1'
/** Session pages (`GET /api/journal/sessions`, `?page=`): the journal is read one leader session at a time (JV-1b). */
export const JOURNAL_SESSIONS_CAPABILITY = 'journal.sessions.v1'
/** `GET /api/journal/members` (JV-1c; journal-v5 spec §4.3): what the team panel's member rows show on their 2nd and 3rd line. */
export const JOURNAL_MEMBERS_CAPABILITY = 'journal.members.v1'

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
}

export interface JournalPage {
  unit: JournalUnit
  project: JournalProject
  personName: string
  /** Oldest first (the daemon sorts by (at, id); the store sorts again). */
  entries: JournalEntry[]
  /** The open 待確認 of the whole unit, newest first (`confirms[]`); null = the daemon sent none, which reads as no open 待確認. */
  confirms: JournalEntry[] | null
  /** There are older months before the oldest entry of this page. */
  more: boolean
  openConfirms: number
  /** Session pages only: the leader session the entries belong to; '' = the daemon sent none (omitempty). */
  page: string
  /** Session pages only: the page held more than the daemon's cap and the newest are kept (omitempty, absent = false). */
  truncated: boolean
}

/** One leader session of the unit's chain (`GET /api/journal/sessions`); times are epoch ms, `endedAt` 0 on the current one. */
export interface SessionLink { sessionId: string; startedAt: number; endedAt: number; via: 'root' | 'relay' | 'clear'; current: boolean }
export interface JournalSessions {
  /** Oldest first. */
  sessions: SessionLink[]
  /** A predecessor reader is not registered: sessions older than the first listed may be missing. */
  incomplete: boolean
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

/** The daemon's `currentTaskOf` states: in progress before pending. */
export const MEMBER_TASK_STATES = ['in_progress', 'pending'] as const
export type MemberTaskState = (typeof MEMBER_TASK_STATES)[number]

/** One current member of the leader's team, as the leader's journal sees it (spec §4.3). Members run no journal of their own. */
export interface JournalMember {
  ref: string
  /** The member's session: what a panel row is matched on. */
  sessionId: string
  title: string
  /** The member's current task (`currentTaskOf`); null = none. */
  task: { number: string; title: string; state: MemberTaskState } | null
  /** The member's newest report to the leader; `at` is epoch ms (ASSUMED, like the journal entries'), `text` its first line. null = none. */
  last: { at: number; kind: string; text: string } | null
}

/** `409 {lead_host_id, lead_session_id}`: this session is a member whose lead lives on another daemon. */
export interface RemoteLead { leadHostId: string; leadSessionId: string }

export const unitKeyOf = (u: Pick<JournalUnit, 'kind' | 'id'>): string => `${u.kind}:${u.id}`
