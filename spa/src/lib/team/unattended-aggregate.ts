// spa/src/lib/team/unattended-aggregate.ts — what the title-bar button shows for 無人值守模式 across every host shown on
// the current workbench (unattended spec D-U23-5; plan PU-2b). Pure: the caller passes the shown ids, the host runtime
// and the unattended store's entries.
//
// Each shown host lands in exactly one group, in this order:
//   unreachable — not `connected`, or its support not known yet, or no state from its daemon yet;
//   unsupported — connected, and its daemon does not list `relay.unattended.v1` (too old);
//   on / off    — reachable: connected, supported, with a state (`reachable` = both, in shown order).
// A disconnected host is unreachable even when an older probe said "unsupported": what it runs now is unknown.
// The mode: `none` with no shown host; `on` / `off` when every shown host is reachable and agrees; anything else —
// mixed, unreachable, too old — is `partial`: an unreachable host may still be on and approving (D-U23-5).
// A host not shown is never counted (and so never written by the toggle).
import type { HostRuntime } from '../../stores/useHostStore'
import type { UnattendedHostEntry } from '../../stores/useUnattendedStore'

export type UnattendedMode = 'off' | 'on' | 'partial' | 'none'

export interface UnattendedAggregate {
  mode: UnattendedMode
  on: string[]
  off: string[]
  unreachable: string[]
  unsupported: string[]
  /** `on` and `off` together, in shown order: the hosts a press writes. */
  reachable: string[]
}

export function aggregateUnattended(
  shownHostIds: readonly string[],
  runtime: Readonly<Record<string, HostRuntime | undefined>>,
  byHost: Readonly<Record<string, UnattendedHostEntry | undefined>>,
): UnattendedAggregate {
  const agg: UnattendedAggregate = { mode: 'none', on: [], off: [], unreachable: [], unsupported: [], reachable: [] }
  for (const hostId of shownHostIds) {
    const entry = Object.hasOwn(byHost, hostId) ? byHost[hostId] : undefined
    if (runtime[hostId]?.status !== 'connected') agg.unreachable.push(hostId)
    else if (entry?.support === 'no') agg.unsupported.push(hostId)
    else if (entry?.support !== 'yes' || !entry.state) agg.unreachable.push(hostId)
    else {
      agg.reachable.push(hostId)
      ;(entry.state.on ? agg.on : agg.off).push(hostId)
    }
  }
  if (shownHostIds.length === 0) return agg
  const all = agg.reachable.length === shownHostIds.length
  agg.mode = all && agg.off.length === 0 ? 'on' : all && agg.on.length === 0 ? 'off' : 'partial'
  return agg
}
