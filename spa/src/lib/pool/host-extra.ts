// spa/src/lib/pool/host-extra.ts — (moved from fleet-bridge.ts, MON-6e) what the pool knows of a host beyond the readings a cell draws: worker, headroom, owner, zone `unknown`, PSI, the check time. The host page reads it without a fleet view.
import type { PoolHostState } from './host-state'
import type { HeadroomReasons, PoolHost, PoolLiveness, PoolLoad, PoolOwner, PoolZone, Slots } from './types'

export interface PoolCellExtra {
  state: PoolLiveness
  /** The pool's zone word: `unknown` is its own (the fleet's zones have no such). Null while the host is not ok. */
  zone: PoolZone | null
  /** The daemon's load score and what drives it; null while it has no `pool.assess.v1` (the ring then keeps the fleet's score). */
  load: PoolLoad | null
  worker: { enabled: boolean; slots: Slots; running: Slots } | null
  headroom: Slots | null
  /** Why each class is at 0, in spec order; null while the daemon does not send them (0 is then shown without a reason). */
  reasons: HeadroomReasons | null
  sessionsRoom: number | null
  owner: PoolOwner | null
  /** Linux only. */
  psi: { some10: number; full10: number } | null
  /** When the probe last ran (`liveness_at`, ms); null for an ok host, the local host and a host the pool does not list. */
  checkedAt: number | null
  /** When the pool last read facts from the host (ms); null if it never did. */
  lastFactsAt: number | null
  /** wwd answers but its facts stopped (spec W3). */
  stalled: boolean
}

/** A host the pool does not list (`unlistedState` in fleet-bridge): `unknown`, nothing read. */
type Unlisted = { state: 'unknown'; lastFactsAt: number | null }

export function hostExtraOf(st: PoolHostState | Unlisted, pool: PoolHost | null): PoolCellExtra {
  if (st.state === 'ok' && 'facts' in st) {
    const f = st.facts
    return {
      state: 'ok', zone: f.derived.zone, load: pool?.load ?? null,
      worker: { enabled: f.worker.enabled, slots: f.worker.slots, running: f.worker.running },
      headroom: f.derived.headroom, reasons: pool?.headroom_reasons ?? null, sessionsRoom: f.derived.sessions_room, owner: f.owner,
      psi: f.mem.psi ? { some10: f.mem.psi.some10, full10: f.mem.psi.full10 } : null, checkedAt: null, lastFactsAt: f.at, stalled: false,
    }
  }
  const none = { load: null, worker: null, headroom: null, reasons: null, sessionsRoom: null, owner: null, psi: null, zone: null }
  const rest = 'pool' in st ? { checkedAt: st.checkedAt, stalled: st.stalled } : { checkedAt: null, stalled: false }
  return { ...none, state: st.state, lastFactsAt: st.lastFactsAt, ...rest }
}
