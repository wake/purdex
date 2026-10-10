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
  client_msg_id?: string
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
  children?: ConversationItem[]
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
  interrupt?: string
  steer?: string
  reasons?: Record<string, string>
}

export interface Header {
  title: string
  /** running | waiting | idle | error | ended | unknown */
  status: string
  /** "terminal" while a pane runs the session. */
  backend: string
  usage?: { model?: string; effort?: string }
  live: boolean
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
