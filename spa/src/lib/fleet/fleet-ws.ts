// spa/src/lib/fleet/fleet-ws.ts — the daemon's `fleet.changed` host event (fleet spec §6): `{term, primary, hosts}` once a
// second from the primary. Called from useMultiHostEventWs with the per-host closure's hostId, after its connection-key
// check. The store merges it only from the current primary; a push of a higher term from another daemon makes it read both
// daemons again (reread.ts), whose answers re-choose the primary. A malformed frame is dropped with one warning.
import { parseFleetPush } from './parse'
import { readAllFleets } from './reread'
import { useFleetStore } from './store'

let warned = false
const warn = (what: string) => { if (!warned) { warned = true; console.warn(`[fleet-ws] ignoring frame: ${what}`) } }

export function handleFleetEvent(hostId: string, value: unknown): void {
  let body: unknown = value
  if (typeof value === 'string') {
    try { body = JSON.parse(value) } catch { return warn('value is not JSON') }
  }
  const push = parseFleetPush(body)
  if (!push) return warn('not a fleet payload')
  if (useFleetStore.getState().applyPush(hostId, push) === 'reread') readAllFleets()
}
