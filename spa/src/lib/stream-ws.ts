// spa/src/lib/stream-ws.ts

// --- Message Types ---
// Moved to ./nex/message-types in P-D.3; re-exported here so existing
// consumers keep compiling until they are unwired.

export type {
  ContentBlock,
  AssistantMessage,
  UserMessage,
  ResultMessage,
  SystemMessage,
  ControlRequest,
  StreamEvent,
  StreamMessage,
} from './nex/message-types'
import type { StreamMessage } from './nex/message-types'

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
