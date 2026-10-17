// spa/src/lib/relay/fresh-ws.ts — the daemon's `relay.fresh` host event (relay v2 contract 5): a snapshot to each new
// subscriber, a changed after every change, each carrying the WHOLE list. Called from useMultiHostEventWs with the per-host
// closure's hostId, after its connection-key check. A host that does not list `relay.half.v1` (or whose info has not been
// read yet: the GET on the flag turning true brings what this frame would have) is not listened to. A malformed frame is
// dropped whole with one warning; the store keeps what it had.
import { selectRelayHalfV1, useNexHostStore } from '../../stores/useNexHostStore'
import { useRelayFreshStore } from '../../stores/useRelayFreshStore'
import { parseRelayFreshEvent } from './fresh'

export function handleRelayFreshEvent(hostId: string, value: unknown): void {
  if (!selectRelayHalfV1(hostId)(useNexHostStore.getState())) return
  const ev = parseRelayFreshEvent(value)
  if (typeof ev === 'string') {
    console.warn(`[relay-fresh] ignoring frame: ${ev}`)
    return
  }
  useRelayFreshStore.getState().apply(hostId, ev.sessions)
}
