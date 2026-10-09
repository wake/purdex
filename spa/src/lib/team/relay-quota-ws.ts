// spa/src/lib/team/relay-quota-ws.ts — the daemon's `team.relay_quota` host event (plan RQ-A Task 5): {op:"changed",
// root_session_id, self_left, member_pool_left, rev} after every write of a quota, from any client. Called from
// useMultiHostEventWs with the per-host closure's hostId. The frame is checked whole (relay-quota-wire.ts) and a
// malformed one is dropped with one warning; the store applies it by the not-smaller `rev` rule (relay-quota.ts).
import { parseRelayQuotaEvent } from './relay-quota-wire'
import { useRelayQuotaStore } from './relay-quota'

export function handleRelayQuotaEvent(hostId: string, value: unknown): void {
  const ev = parseRelayQuotaEvent(value)
  if (typeof ev === 'string') {
    console.warn(`[relay-quota-ws] ignoring frame: ${ev}`)
    return
  }
  useRelayQuotaStore.getState().applyEvent(hostId, ev)
}
