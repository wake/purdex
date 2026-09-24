// spa/src/lib/nex/message-types.ts — Claude Code `stream-json` message shapes
// (assistant / user / result / system / control_request / stream_event) as
// consumed by the exec pane: event-reducer, ConversationMessages,
// ToolUseBlock, useExecutionStore. Moved verbatim in P-D.3 from the
// Stream-mode WS client module that the same phase deleted; P-B4 added
// the optional cost fields on ResultMessage (spec §4.5).

export interface ContentBlock {
  type: 'text' | 'tool_use' | 'tool_result' | 'thinking'
  text?: string
  id?: string
  name?: string
  input?: Record<string, unknown>
  content?: string
  is_error?: boolean
  thinking?: string
  tool_use_id?: string
}

export interface AssistantMessage {
  type: 'assistant'
  /** CC `parent_tool_use_id` — non-null on a subagent's own frames. */
  parent_tool_use_id?: string | null
  message: {
    id?: string
    role: 'assistant'
    content: ContentBlock[]
    stop_reason: string | null
  }
}

export interface UserMessage {
  type: 'user'
  /** CC `parent_tool_use_id` — non-null on a subagent's own frames. */
  parent_tool_use_id?: string | null
  message: {
    role: 'user'
    content: ContentBlock[]
    stop_reason: string | null
  }
}

export interface ResultMessage {
  type: 'result'
  subtype: string
  total_cost_usd?: number
  session_id?: string
  /** CC `duration_ms` — wall-clock time of the whole turn (queueing included). */
  duration_ms?: number
  /** CC `duration_api_ms` — time spent inside API calls; ≤ `duration_ms`. */
  duration_api_ms?: number
  /** CC `ttft_ms` — time to first token of the turn. */
  ttft_ms?: number
  /** CC `is_error` — true when the turn ended in an error (subtype may still be absent). */
  is_error?: boolean
  /** CC `num_turns` — API round-trips within this one Nexen turn (not the turn count). */
  num_turns?: number
  /** CC `parent_tool_use_id` — non-null on a subagent's own result frame; null/absent on the parent's. */
  parent_tool_use_id?: string | null
  /** CC `usage` — token totals across all models for this turn (snake_case keys). */
  usage?: {
    input_tokens?: number
    output_tokens?: number
    cache_read_input_tokens?: number
    cache_creation_input_tokens?: number
  }
  /** CC `modelUsage` — per-model split keyed by model id (camelCase keys); `canonicalModel` strips the date suffix. */
  modelUsage?: Record<string, {
    inputTokens?: number
    outputTokens?: number
    cacheReadInputTokens?: number
    cacheCreationInputTokens?: number
    costUSD?: number
    canonicalModel?: string
  }>
}

export interface SystemMessage {
  type: 'system'
  subtype: string
  session_id?: string
  tools?: string[]
  model?: string
  permissionMode?: string
  [key: string]: unknown
}

export interface ControlRequest {
  type: 'control_request'
  request_id: string
  request: {
    subtype: string
    tool_name?: string
    input?: Record<string, unknown>
    tool_use_id?: string
  }
}

export interface StreamEvent {
  type: 'stream_event'
  event: {
    type: string
    delta?: { type: string; text?: string }
    [key: string]: unknown
  }
}

export type StreamMessage =
  | AssistantMessage
  | UserMessage
  | ResultMessage
  | SystemMessage
  | ControlRequest
  | StreamEvent
  | { type: string; [key: string]: unknown }
