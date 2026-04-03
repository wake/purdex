// spa/src/lib/host-events.ts

export interface HostEvent {
  type: 'handoff' | 'relay' | 'hook' | 'sessions' | 'tmux'
  session: string
  value: string
}

export interface EventConnection {
  close: () => void
}

export function connectHostEvents(
  url: string,
  onEvent: (event: HostEvent) => void,
  onClose?: () => void,
  onOpen?: () => void,
  getTicket?: () => Promise<string>,
): EventConnection {
  let ws: WebSocket
  let retryMs = 1000
  let closed = false

  async function connect() {
    let wsUrl = url
    if (getTicket) {
      try {
        const ticket = await getTicket()
        const u = new URL(wsUrl)
        u.searchParams.set('ticket', ticket)
        wsUrl = u.toString()
      } catch {
        if (!closed) setTimeout(connect, retryMs)
        retryMs = Math.min(retryMs * 2, 30000)
        return
      }
    }
    ws = new WebSocket(wsUrl)
    ws.onopen = () => { retryMs = 1000; onOpen?.() }
    ws.onmessage = (e) => {
      try {
        const event = JSON.parse(e.data) as HostEvent
        onEvent(event)
      } catch { /* ignore parse errors */ }
    }
    ws.onerror = () => { /* handled by onclose */ }
    ws.onclose = () => {
      if (closed) return
      onClose?.()
      setTimeout(() => {
        if (!closed) connect()
      }, retryMs)
      retryMs = Math.min(retryMs * 2, 30000)
    }
  }

  connect()
  return { close: () => { closed = true; ws?.close() } }
}
