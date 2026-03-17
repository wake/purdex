// spa/src/lib/stream-ws.ts

// --- Message Types ---

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

export function parseStreamMessage(raw: string): StreamMessage | null {
  try {
    return JSON.parse(raw) as StreamMessage
  } catch {
    return null
  }
}

// --- Connection ---

export interface StreamConnection {
  send: (msg: object) => void
  sendControlResponse: (requestId: string, response: object) => void
  interrupt: () => void
  close: () => void
}

export function connectStream(
  url: string,
  onMessage: (msg: StreamMessage) => void,
  onClose: () => void,
  onOpen?: () => void,
): StreamConnection {
  const ws = new WebSocket(url)

  ws.onopen = () => onOpen?.()
  ws.onmessage = (e) => {
    const msg = parseStreamMessage(e.data)
    if (msg) onMessage(msg)
  }
  ws.onerror = () => {}
  ws.onclose = () => onClose()

  const sendJSON = (data: object) => {
    if (ws.readyState === WebSocket.OPEN) {
      ws.send(JSON.stringify(data))
    }
  }

  return {
    send: sendJSON,
    sendControlResponse: (requestId, response) => {
      sendJSON({
        type: 'control_response',
        response: {
          subtype: 'success',
          request_id: requestId,
          response,
        },
      })
    },
    interrupt: () => {
      sendJSON({
        type: 'control_response',
        response: { subtype: 'interrupt' },
      })
    },
    close: () => ws.close(),
  }
}
