// spa/src/lib/team/unattended-ws.ts — the daemon's `team.unattended` host event (unattended spec D-U23-6; plan PU-2a):
//   {op:"snapshot", state}  to each new subscriber;
//   {op:"changed",  state}  after every change, from any client (every window follows).
// Called from useMultiHostEventWs with the per-host closure's hostId. This is the trust boundary: the frame is
// checked whole (`isUnattendedState`, types.ts) and a malformed one is dropped with one warning — the store keeps
// what it had rather than read a broken frame as "off". A good frame also proves the daemon supports the switch.
import { useUnattendedStore } from '../../stores/useUnattendedStore'
import { isUnattendedState, type UnattendedEventValue } from './types'

function parse(value: unknown): UnattendedEventValue | string {
  let o: unknown = value
  if (typeof value === 'string') {
    try {
      o = JSON.parse(value)
    } catch {
      return 'value is not JSON'
    }
  }
  if (typeof o !== 'object' || o === null || Array.isArray(o)) return 'value is not an object'
  const { op, state } = o as Record<string, unknown>
  if (op !== 'snapshot' && op !== 'changed') return `unknown op ${JSON.stringify(op)}`
  if (!isUnattendedState(state)) return `${op}: state is not the wire shape`
  return { op, state }
}

export function handleUnattendedEvent(hostId: string, value: unknown): void {
  const ev = parse(value)
  if (typeof ev === 'string') {
    console.warn(`[unattended-ws] ignoring frame: ${ev}`)
    return
  }
  const store = useUnattendedStore.getState()
  store.applyState(hostId, ev.state)
  store.setSupport(hostId, 'yes')
}
