export interface TerminalConnection {
  send: (data: string) => void
  resize: (cols: number, rows: number) => void
  close: () => void
}

export function connectTerminal(
  url: string,
  onData: (data: ArrayBuffer) => void,
  onClose: () => void,
): TerminalConnection {
  const ws = new WebSocket(url)
  ws.binaryType = 'arraybuffer'

  ws.onmessage = (e) => onData(e.data)
  ws.onclose = () => onClose()
  ws.onerror = () => onClose()

  return {
    send: (data) => {
      if (ws.readyState === WebSocket.OPEN) ws.send(data)
    },
    resize: (cols, rows) => {
      if (ws.readyState === WebSocket.OPEN) {
        ws.send(JSON.stringify({ type: 'resize', cols, rows }))
      }
    },
    close: () => ws.close(),
  }
}
