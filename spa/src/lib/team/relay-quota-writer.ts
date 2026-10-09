// spa/src/lib/team/relay-quota-writer.ts — the stepper's write path (plan RQ-A Task 4; the store is relay-quota.ts).
//
// Per (host, root, field): a click sets the field's DESIRED value at once (the stepper shows it); clicks coalesce for
// 300 ms, the last value wins; at most one PUT is in flight per key; when it settles and the desired value moved, the new
// value is sent. The PUT body carries only that field (and the app client), so the other field keeps whatever the daemon
// has. A failure drops the desired value (the stepper falls back to the confirmed one) and toasts; `pending_lineage` (the
// value went to a provisional root that will be orphaned) drops it, toasts, does not take the provisional numbers, and
// re-reads the host 5 s later when the rows carry the real root.
import { hostExistsNow } from './quota-host'
import { ApprovalApiError } from './approval-api'
import { fieldKey, useRelayQuotaStore } from './relay-quota'
import { putRelayQuota } from './unattended-api'
import { hostLabel, hostLookOf } from '../host-look'
import { useI18nStore } from '../../stores/useI18nStore'
import { useUndoToast } from '../../stores/useUndoToast'
import type { RelayQuotaField, RelayQuotaView } from './types'

export const DEBOUNCE_MS = 300
export const LINEAGE_REFETCH_MS = 5_000

export interface QuotaTarget {
  hostId: string
  /** The clicked row's session: the daemon resolves the chain root from it. */
  sessionId: string
  /** The row's chain root (the key the numbers are kept under). */
  root: string
  /** The row's name for the toasts. */
  label: string
}

export interface WriterDeps {
  put: (hostId: string, sessionId: string, field: RelayQuotaField, value: number) => Promise<RelayQuotaView>
  toast: (message: string) => void
  message: (key: string, params: Record<string, string>) => string
  hostLabel: (hostId: string) => string
  /** Re-read the host's unattended view (the panel registers the real one; see `registerRefetch`). */
  refetch: (hostId: string) => void
  hostExists: (hostId: string) => boolean
}

const defaultDeps = (): WriterDeps => ({
  put: putRelayQuota,
  toast: (m) => useUndoToast.getState().show(m),
  message: (key, params) => useI18nStore.getState().t(key, params),
  hostLabel: (hostId) => hostLabel(hostId, hostLookOf(hostId)),
  refetch: (hostId) => refetchers.get(hostId)?.(),
  hostExists: hostExistsNow,
})

let deps: WriterDeps = defaultDeps()
const refetchers = new Map<string, () => void>()
interface Slot {
  timer?: ReturnType<typeof setTimeout>
  /** The row whose click was last: what the PUT and the toasts name. */
  target: QuotaTarget
}
const slots = new Map<string, Slot>()
const lineageTimers = new Set<ReturnType<typeof setTimeout>>()

/** The panel registers how to re-read one host's view; returns the disposer. */
export function registerRefetch(hostId: string, fn: () => void): () => void {
  refetchers.set(hostId, fn)
  return () => { if (refetchers.get(hostId) === fn) refetchers.delete(hostId) }
}

/** Test seam: replace some of the dependencies. */
export function configureWriter(over: Partial<WriterDeps>): void {
  deps = { ...deps, ...over }
}

export function resetWriter(): void {
  for (const s of slots.values()) if (s.timer !== undefined) clearTimeout(s.timer)
  slots.clear()
  for (const t of lineageTimers) clearTimeout(t)
  lineageTimers.clear()
  refetchers.clear()
  deps = defaultDeps()
}

const clamp = (v: number): number => Math.min(99, Math.max(0, Math.trunc(v)))

/** A click on a stepper: the desired value is set at once and sent after the debounce, one write in flight per field. */
export function setQuota(target: QuotaTarget, field: RelayQuotaField, value: number): void {
  const key = fieldKey(target.hostId, target.root, field)
  const store = useRelayQuotaStore.getState()
  const cur = store.writes[key]
  store.setWrite(target.hostId, target.root, field, { ...cur, desired: clamp(value) })
  const slot = slots.get(key) ?? { target }
  slot.target = target
  if (slot.timer !== undefined) clearTimeout(slot.timer)
  slot.timer = setTimeout(() => { slot.timer = undefined; flush(key, field) }, DEBOUNCE_MS)
  slots.set(key, slot)
}

function flush(key: string, field: RelayQuotaField): void {
  const slot = slots.get(key)
  if (slot === undefined) return
  const { hostId, root, sessionId } = slot.target
  const store = useRelayQuotaStore.getState()
  const w = store.writes[key]
  if (w === undefined || w.inflight !== undefined || w.desired === undefined) return // in flight: its settle sends the newer value
  const sent = w.desired
  store.setWrite(hostId, root, field, { inflight: sent })
  deps.put(hostId, sessionId, field, sent).then(
    (view) => settled(key, field, sent, view, null),
    (e: unknown) => settled(key, field, sent, null, e),
  )
}

function settled(key: string, field: RelayQuotaField, sent: number, view: RelayQuotaView | null, error: unknown): void {
  const slot = slots.get(key)
  if (slot === undefined) return // reset meanwhile
  const { hostId, root, label } = slot.target
  const store = useRelayQuotaStore.getState()
  if (!deps.hostExists(hostId)) { // removed while the PUT was out: its answer is nobody's
    store.clearWrite(hostId, root, field)
    drop(key)
    return
  }
  if (view === null) {
    store.clearWrite(hostId, root, field)
    drop(key)
    const code = error instanceof ApprovalApiError ? error.code : 'error'
    deps.toast(deps.message('unattended.quota.save_failed', { host: deps.hostLabel(hostId), session: label, code }))
    return
  }
  if (view.pending_lineage === true) {
    store.clearWrite(hostId, root, field)
    drop(key)
    deps.toast(deps.message('unattended.quota.pending_lineage', { session: label }))
    const timer = setTimeout(() => {
      lineageTimers.delete(timer)
      if (deps.hostExists(hostId)) deps.refetch(hostId)
    }, LINEAGE_REFETCH_MS)
    lineageTimers.add(timer)
    return
  }
  store.applyAnswer(hostId, view.root_session_id, { self_left: view.self_left, member_pool_left: view.member_pool_left }, view.rev)
  const after = useRelayQuotaStore.getState().writes[key]
  if (after?.desired !== undefined && after.desired !== sent) {
    // clicked again during the flight: send the new value now (the debounce timer, if still pending, finds nothing to do)
    store.setWrite(hostId, root, field, { desired: after.desired })
    if (slot.timer !== undefined) { clearTimeout(slot.timer); slot.timer = undefined }
    flush(key, field)
    return
  }
  store.clearWrite(hostId, root, field)
  drop(key)
}

function drop(key: string): void {
  const slot = slots.get(key)
  if (slot?.timer !== undefined) clearTimeout(slot.timer)
  slots.delete(key)
}
