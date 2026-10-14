// spa/src/lib/fleet/fleet-ws.ts — the daemon's `fleet.changed` host event (fleet spec §6): the whole payload, sent when a
// host's zone, status, trend, error code or a whole-percent number changed. Called from useMultiHostEventWs with the
// per-host closure's hostId, after its connection-key check. The store writes it only while this connection's
// `fleet.v1` answer has confirmed the capability (store.ts), so a frame of a gone connection is never applied.
import { parseFleetPayload } from './parse'
import { useFleetStore } from './store'

export function handleFleetEvent(hostId: string, value: unknown): void {
  let body: unknown = value
  if (typeof value === 'string') {
    try { body = JSON.parse(value) } catch { console.warn('[fleet-ws] ignoring frame: value is not JSON'); return }
  }
  const payload = parseFleetPayload(body)
  if (!payload) { console.warn('[fleet-ws] ignoring frame: not a fleet payload'); return }
  useFleetStore.getState().applyEvent(hostId, payload)
}
