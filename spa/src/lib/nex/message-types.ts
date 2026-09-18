// spa/src/lib/nex/message-types.ts — Claude Code `stream-json` message shapes
// (assistant / user / result / system / control_request / stream_event) as
// consumed by the exec pane: event-reducer, ConversationMessages,
// ToolUseBlock, useExecutionStore. Moved verbatim in P-D.3 from the
// Stream-mode WS client module that the same phase deleted; the
// declarations below are byte-identical to the originals.

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
  message: {
    id?: string
    role: 'assistant'
    content: ContentBlock[]
    stop_reason: string | null
  }
}

export interface UserMessage {
  type: 'user'
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
  duration_ms?: number
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
