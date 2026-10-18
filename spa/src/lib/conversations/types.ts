// spa/src/lib/conversations/types.ts — the conversation wire form (U1 spec §8.1 model, §8.2 API; U3 plan D4). JSON,
// snake_case, times as integer milliseconds. Additive evolution only: an unknown item `type` is skipped, an unknown enum
// value is treated as unknown, never an error — so the unions below end in `string` where the daemon may add values.

export type UserSource = 'user' | 'peer' | 'queued' | 'schedule' | 'background' | 'bash' | 'command_output' | (string & {})
export type Outcome = 'done' | 'interrupted' | 'failed' | 'running' | (string & {})
export type StepKind = 'edit' | 'execute' | 'read' | 'search' | 'fetch' | 'task' | 'other' | (string & {})
export type StepStatus = 'running' | 'done' | 'failed' | 'denied' | (string & {})
export type SystemKind = 'interrupted' | 'compacted' | 'handoff' | 'model_changed' | 'resumed' | 'command_output' | 'notice' | (string & {})

export interface ItemBase {
  id: string
  at: number
  /** The item's 0-based position in its turn's FULL item list (API answers only; stable within an epoch). */
  index: number
}

export interface UserItem extends ItemBase {
  type: 'user'
  text: string
  truncated?: boolean
  source: UserSource
  from?: { kind: string; name?: string; unverified?: boolean }
  images?: Array<{ media_type: string; bytes: number }>
  /** Set by the daemon when it knows which App request sent this message (U3-2). */
  client_msg_id?: string | null
}

export interface AgentTextItem extends ItemBase {
  type: 'agent_text'
  markdown: string
  truncated?: boolean
  streaming?: boolean
}

export interface ThinkingItem extends ItemBase {
  type: 'thinking'
  text?: string
  truncated?: boolean
  duration_ms?: number
}

export interface StepOutput {
  text: string
  total_lines: number
  total_bytes: number
  truncated: boolean
  keep?: 'head' | 'tail'
  images?: Array<{ media_type: string; bytes: number }>
}

export interface DiffHunk {
  old_start: number
  old_lines: number
  new_start: number
  new_lines: number
  lines: string[]
}

export interface StepDiff {
  path: string
  added: number
  removed: number
  exact: boolean
  hunks?: DiffHunk[]
  truncated?: boolean
  created?: boolean
}

export interface StepQuestionItem {
  question: string
  header?: string
  multiple: boolean
  options: Array<{ label: string; description?: string }>
}

export interface StepItem extends ItemBase {
  type: 'step'
  kind: StepKind
  tool: string
  status: StepStatus
  denial?: string
  summary: string
  started_at: number
  duration_ms?: number
  input: unknown
  input_truncated?: boolean
  input_partial?: boolean
  output?: StepOutput
  diff?: StepDiff
  command?: { text: string; description?: string; exit_code?: number; background_task_id?: string }
  subagent?: { agent_id: string; description?: string; type?: string; async?: boolean }
  /** Never inlined by snapshots: loaded on demand (`fetchSubagent`) and kept by the store. */
  children?: Array<DistributiveOmit<ConversationItem, 'index'>>
  question?: { questions: StepQuestionItem[]; answers?: string[][] }
  read?: { offset?: number; limit?: number }
  search?: { where: string }
}

export interface SystemItem extends ItemBase {
  type: 'system'
  kind: SystemKind
  detail?: unknown
}

/** An item type this build does not know: kept so the turn's `index` positions stay honest, drawn as nothing. */
export interface UnknownItem extends ItemBase {
  type: string
}

/** An item as the §8.1 model has it, without the API's placement `index` (a subagent's items are this). */
export type DistributiveOmit<T, K extends PropertyKey> = T extends unknown ? Omit<T, K> : never

export type KnownItem = UserItem | AgentTextItem | ThinkingItem | StepItem | SystemItem
export type ConversationItem = KnownItem | UnknownItem

export interface TurnHeader {
  id: string
  index: number
  started_at: number
  ended_at?: number
  outcome: Outcome
  error?: { kind: string; message: string }
  duration_ms?: number
  /** What the turn's assistant messages have written so far (main thread; c9d9c7-67). Absent while none has reported any, and from an older daemon. */
  output_tokens?: number
  /** The turn was too long to send whole: its oldest N items are not in the document (v1 has no paging inside a turn). */
  omitted_items?: number
}

export interface Turn extends TurnHeader {
  items: ConversationItem[]
}

/** The conversation's capability table; a field that is absent is unsupported (fail-closed), see `reasons`. */
export interface Capabilities {
  source?: string
  text_streaming?: string
  thinking?: string
  answer_question?: string
  answer_permission?: string
  answer_plan?: string
  usage?: string
  todo?: string
  subagent?: string
  background_tasks?: string
  peer_inbound?: string
  send?: string
  /** `keys`: the daemon can send Claude Code's queue now (`POST …/send-now`, host capability `sessions.send_now.v1`); `not_wired`: it cannot. */
  send_now?: string
  interrupt?: string
  steer?: string
  reasons?: Record<string, string>
}

/** A message waiting in Claude Code's own queue (the terminal's: typed while a turn ran, not yet read). Absent / empty when there is none. */
export interface HeaderQueued {
  text: string
  /** ms */
  at: number
  /** user | peer (a background-task notice is left out by the daemon) */
  source: 'user' | 'peer' | (string & {})
  /** The sender's display name (peer only). */
  from?: string
}

/** The work Claude Code still runs in the background (its last Stop hook's background_tasks), by kind. A kind that is absent is none. */
export interface HeaderBackground {
  shell?: number
  subagent?: number
  /** monitor, workflow, cron wake-ups */
  other?: number
}

export interface Header {
  title: string
  /** running | waiting | idle | error | ended | unknown */
  status: string
  /** "terminal" while a pane runs the session. */
  backend: string
  usage?: { model?: string; effort?: string }
  live: boolean
  /** Claude Code's own queue (B-read); an older daemon sends none. Drawn only while the status is running / waiting. */
  queued?: HeaderQueued[]
  /** Background work still running (c9d9c7-67 PR2); absent when none, and from an older daemon. The deck says so while the conversation lives. */
  background?: HeaderBackground
}

export interface Window {
  first_index: number
  last_index: number
  total_turns: number
  has_more_before: boolean
}

export interface Snapshot {
  reset?: boolean
  conversation: {
    key: { host_id: string; provider: string; session_id: string }
    backend: string
    provider: string
    title: string
    status: string
    capabilities: Capabilities
    usage?: { model?: string; effort?: string }
    turns: Turn[]
  }
  header: Header
  window: Window
  cursor: string
}

export interface Change {
  turn: TurnHeader
  items: ConversationItem[]
}

export interface Increment {
  changes: Change[]
  header: Header
  cursor: string
}

/** The open approvals of a conversation, as the daemon sends them (the team module's approval record). */
export interface ConversationApproval {
  id: string
  [field: string]: unknown
}

export type Frame =
  | { type: 'conversation.snapshot'; seq: number; value: Snapshot }
  | { type: 'conversation.changes'; seq: number; value: Increment }
  | { type: 'conversation.header'; seq: number; value: { header: Header; cursor: string } }
  | { type: 'conversation.reset'; seq: number; value: unknown }
  | { type: 'conversation.capabilities'; seq: number; value: { capabilities: Capabilities } }
  | { type: 'approvals.snapshot'; seq: number; value: { approvals: ConversationApproval[] } }
  | { type: 'approval'; seq: number; value: { op: 'opened' | 'closed'; approval: ConversationApproval } }
  | { type: string; seq: number; value: unknown }

export const isKnownItem = (it: ConversationItem): it is KnownItem =>
  it.type === 'user' || it.type === 'agent_text' || it.type === 'thinking' || it.type === 'step' || it.type === 'system'
