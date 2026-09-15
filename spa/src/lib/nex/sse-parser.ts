// spa/src/lib/nex/sse-parser.ts — the subset of text/event-stream Nexen
// emits (api/sse.go): `id:`, `event:`, `data:` (multi-line), `:` comments
// as keepalives, blank line dispatches. A frame with no `id:` line is a
// transient frame (capability-matrix §3.5) and is reported with id null —
// that distinction is the whole reason a native EventSource is not used
// (it would also be unable to send Last-Event-ID as a header).

export interface NexSseFrame {
  id: string | null
  event: string
  data: string
}

export class SseParser {
  private buffer = ''
  private id: string | null = null
  private event = ''
  private data: string[] = []

  push(chunk: string): NexSseFrame[] {
    this.buffer += chunk
    const out: NexSseFrame[] = []
    let nl: number
    while ((nl = this.buffer.indexOf('\n')) >= 0) {
      let line = this.buffer.slice(0, nl)
      this.buffer = this.buffer.slice(nl + 1)
      if (line.endsWith('\r')) line = line.slice(0, -1)
      if (line === '') {
        const frame = this.dispatch()
        if (frame) out.push(frame)
        continue
      }
      if (line.startsWith(':')) continue
      const colon = line.indexOf(':')
      const field = colon >= 0 ? line.slice(0, colon) : line
      let value = colon >= 0 ? line.slice(colon + 1) : ''
      if (value.startsWith(' ')) value = value.slice(1)
      switch (field) {
        case 'id': this.id = value; break
        case 'event': this.event = value; break
        case 'data': this.data.push(value); break
        default: break // retry:, unknown fields — not used
      }
    }
    return out
  }

  private dispatch(): NexSseFrame | null {
    const frame = this.data.length === 0
      ? null
      : { id: this.id, event: this.event || 'message', data: this.data.join('\n') }
    this.id = null
    this.event = ''
    this.data = []
    return frame
  }
}
