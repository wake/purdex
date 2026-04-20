// Side-channel bus for the statusline self-test panel.
// The WS dispatcher (agent-ws-dispatch.ts) calls emit() when an agent.status
// event arrives with a `__pdx_test_` session nonce. The useStatuslineTest
// hook subscribes for its specific nonce and marks stage 4 on receipt.
// Kept deliberately small — no RxJS, no Zustand — because test traffic is
// short-lived and we don't want to persist anything.
import { debugStatuslineTest } from './statusline-test-debug'

export interface StatuslineTestReceivedEvent {
  nonce: string
  hostId: string
  raw: Record<string, unknown>
}

type Handler = (ev: StatuslineTestReceivedEvent) => void

class Bus {
  private handlers = new Map<string, Set<Handler>>()

  subscribe(nonce: string, handler: Handler): () => void {
    let set = this.handlers.get(nonce)
    if (!set) {
      set = new Set()
      this.handlers.set(nonce, set)
    }
    set.add(handler)
    debugStatuslineTest('bus.subscribe', { nonce, subscriberCount: set.size })
    return () => {
      const current = this.handlers.get(nonce)
      if (!current) return
      current.delete(handler)
      debugStatuslineTest('bus.unsubscribe', { nonce, remaining: current.size })
      if (current.size === 0) this.handlers.delete(nonce)
    }
  }

  emit(ev: StatuslineTestReceivedEvent): void {
    const set = this.handlers.get(ev.nonce)
    debugStatuslineTest('bus.emit', { nonce: ev.nonce, subscriberCount: set?.size ?? 0 })
    if (!set) return
    for (const h of set) h(ev)
  }

  // Test-only helper.
  reset(): void {
    this.handlers.clear()
  }
}

export const statuslineTestBus = new Bus()
