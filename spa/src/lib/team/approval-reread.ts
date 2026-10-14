// spa/src/lib/team/approval-reread.ts — the App reads a card itself when it ran out while open (relay v2 App contract 3,
// `relay.unanswered.v1`). The daemon sends no event op for that, and a SOLO session's pause leaves the roster JSON unchanged, so
// no roster event arrives and the 30 s paused poll only runs with 無人值守 on (when there are no cards). Without this the most common
// card, a solo one, would sit at 0:00 until the next reconnect snapshot.
//
// Per open card, only on a host that lists `relay.unanswered.v1`: about 2 s after `deadline_at`, if the card is still held and not
// yet unanswered by either rule (approval-unanswered.ts), one `GET /api/team/approvals/{id}`.
//   - open and `unanswered_at > 0` → take `unanswered_at` and `deadline_at` (the same merge a snapshot does), done;
//   - closed, or 404 → nothing: the closed event or the next snapshot takes it out;
//   - open without `unanswered_at`, or the read failed → once more 15 s later, then never again (the pause data or the next
//     snapshot decides).
// At most two reads per card; a failed read is dropped quietly. A closed card, a host that stops listing the capability, a card
// that is now unanswered, and `stop()` all cancel the pending timer. The capability can be learned after the deadline: the read
// then happens as soon as it is known.
import { useApprovalStore } from '../../stores/useApprovalStore'
import { useUnattendedStore } from '../../stores/useUnattendedStore'
import { usePausedStore } from '../../stores/usePausedStore'
import { useTeamRosterStore } from '../../stores/useTeamRosterStore'
import { ApprovalApiError, fetchApproval } from './approval-api'
import { isCardUnanswered } from './approval-unanswered'
import { isUnanswered } from './types'

/** How long after `deadline_at` the first read waits (the daemon marks the card at its deadline). */
export const REREAD_AFTER_DEADLINE_MS = 2_000
/** The one retry, when the first read found the card open without `unanswered_at`. */
export const REREAD_RETRY_MS = 15_000
const MAX_READS = 2

interface Tracker {
  timer: ReturnType<typeof setTimeout> | null
}

export function startUnansweredReread(): () => void {
  const trackers = new Map<string, Tracker>()
  // Reads made per card. Kept apart from the tracker on purpose: a host that loses and regains the capability (a flapping
  // connection) drops its trackers, but the card must not get a fresh allowance, since every read renews the row's lease. It goes
  // when the card leaves the store or becomes unanswered.
  const readsOf = new Map<string, number>()
  const readsMade = (key: string): number => readsOf.get(key) ?? 0

  const supported = (hostId: string): boolean => useUnattendedStore.getState().byHost[hostId]?.unansweredSupport === 'yes'
  const cancel = (key: string) => {
    const t = trackers.get(key)
    if (t?.timer != null) clearTimeout(t.timer)
    trackers.delete(key)
  }

  const read = async (key: string, t: Tracker) => {
    t.timer = null
    const entry = useApprovalStore.getState().entries[key]
    if (trackers.get(key) !== t || entry === undefined || !supported(entry.hostId)) return
    const { hostId, approval } = entry
    // The pause data got there first (rule (b)): nothing left to ask.
    const src = { paused: usePausedStore.getState().byHost, roster: useTeamRosterStore.getState().byHost }
    if (isCardUnanswered(hostId, approval, Date.now(), src)) return
    readsOf.set(key, readsMade(key) + 1)
    let again = true
    try {
      const fresh = await fetchApproval(hostId, approval.id)
      if (trackers.get(key) !== t) return // cancelled meanwhile
      if (fresh.state !== 'open') again = false
      else if (isUnanswered(fresh)) {
        useApprovalStore.getState().applyUnanswered(hostId, fresh)
        again = false
      }
    } catch (e) {
      if (trackers.get(key) !== t) return
      if (e instanceof ApprovalApiError && e.status === 404) again = false
      // Any other failure is dropped quietly; it counts as a read and is retried like an open answer.
    }
    if (again && readsMade(key) < MAX_READS) t.timer = setTimeout(() => void read(key, t), REREAD_RETRY_MS)
  }

  const reconcile = () => {
    const entries = useApprovalStore.getState().entries
    for (const key of [...trackers.keys()]) {
      const e = entries[key]
      if (e === undefined || !supported(e.hostId) || isUnanswered(e.approval)) cancel(key)
    }
    for (const key of [...readsOf.keys()]) {
      const e = entries[key]
      if (e === undefined || isUnanswered(e.approval)) readsOf.delete(key)
    }
    for (const [key, e] of Object.entries(entries)) {
      if (trackers.has(key) || !supported(e.hostId) || isUnanswered(e.approval) || readsMade(key) >= MAX_READS) continue
      const t: Tracker = { timer: null }
      trackers.set(key, t)
      t.timer = setTimeout(() => void read(key, t), Math.max(0, e.approval.deadline_at + REREAD_AFTER_DEADLINE_MS - Date.now()))
    }
  }

  reconcile()
  const unsubApprovals = useApprovalStore.subscribe((s, prev) => { if (s.entries !== prev.entries) reconcile() })
  const unsubSupport = useUnattendedStore.subscribe((s, prev) => { if (s.byHost !== prev.byHost) reconcile() })
  return () => {
    unsubApprovals()
    unsubSupport()
    for (const key of [...trackers.keys()]) cancel(key)
  }
}
