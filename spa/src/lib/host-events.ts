// spa/src/lib/host-events.ts

export interface HostEvent {
  type: 'handoff' | 'relay' | 'hook' | 'sessions' | 'tmux'
  session: string
  value: string
}

export interface EventConnection {
  close: () => void
  reconnect: () => void
  reconnectWithTicket: (ticket?: string) => void
}

export function connectHostEvents(
  url: string,
  onEvent: (event: HostEvent) => void,
  onClose?: () => void,
  onOpen?: () => void,
  getTicket?: () => Promise<string>,
  autoReconnect = true,
  lazy = false,
): EventConnection {
  let ws: WebSocket
  let retryMs = 1000
  let closed = false
  let connecting = false
  let pendingTicket: string | undefined

  async function connect() {
    if (connecting) return
    connecting = true
    try {
      let wsUrl = url
      const ticket = pendingTicket ?? (getTicket ? await getTicket().catch(() => null) : null)
      pendingTicket = undefined

      if (ticket) {
        const u = new URL(wsUrl)
        u.searchParams.set('ticket', ticket)
        wsUrl = u.toString()
      } else if (getTicket) {
        if (!closed) onClose?.()
        return
      }

      ws = new WebSocket(wsUrl)
      ws.onopen = () => { retryMs = 1000; onOpen?.() }
      ws.onmessage = (e) => {
        try {
          const event = JSON.parse(e.data) as HostEvent
          onEvent(event)
        } catch { /* ignore parse errors */ }
      }
      ws.onerror = () => {}
      ws.onclose = () => {
        if (closed) return
        onClose?.()
        if (autoReconnect) {
          setTimeout(() => { if (!closed) connect() }, retryMs)
          retryMs = Math.min(retryMs * 2, 30000)
        }
      }
    } finally {
      connecting = false
    }
  }

  if (!lazy) connect()
  return {
    close: () => { closed = true; ws?.close() },
    reconnect: () => {
      if (!closed) {
        retryMs = 1000
        if (ws) { ws.onclose = null; ws.close() }  // 清除 onclose 防止 double-trigger
        connect()
      }
    },
    reconnectWithTicket: (ticket) => {
      if (!closed) {
        pendingTicket = ticket
        retryMs = 1000
        if (ws) { ws.onclose = null; ws.close() }  // 清除 onclose 防止 double-trigger
        connect()
      }
    },
  }
}
