// spa/src/lib/pool/push-switch.ts — the one 「推播」 switch on the 設定 tab (monitoring tabs spec M37, M50; plan MON-9a-2b). It is the `wwd_alerts.push` row of EVERY App host that fronts a pool host, not only the connected ones:
// a connected host is read and written now; one that is offline is put in a queue and written when it connects again. The queue is memory only, so closing the App drops it (the next start shows "部分開啟" if the hosts disagree).
import { fetchHostConfig, HostConfigConflictError, putHostConfig } from '../host-config-api'
import { useHostStore } from '../../stores/useHostStore'

export interface PushTarget { hostConfigId: string; alias: string }
export interface PushHost extends PushTarget {
  connected: boolean
  /** null: not known (offline, unreadable, or a shape this App does not know). */
  on: boolean | null
  revision: number | null
  error: string | null
}
export interface PushResult { hostConfigId: string; ok: boolean; queued?: boolean; error?: string }
export type PushState = 'on' | 'off' | 'partial' | 'unknown'

const isObj = (v: unknown): v is Record<string, unknown> => typeof v === 'object' && v !== null && !Array.isArray(v)
const messageOf = (e: unknown): string => (e instanceof Error ? e.message : String(e))
const isConnected = (id: string): boolean => useHostStore.getState().runtime[id]?.status === 'connected'

/** `{ items: { push }, revision }`, or null for anything else. */
function parseRow(v: unknown): { push: boolean; revision: number } | null {
  if (!isObj(v) || !isObj(v.items) || typeof v.items.push !== 'boolean' || typeof v.revision !== 'number') return null
  return { push: v.items.push, revision: v.revision }
}

/** The App hosts to write: each one that fronts a pool host, once, in host order; the alias is that of the first pool host it fronts. */
export function pushTargets(owners: Record<string, string>, poolHosts: readonly { host_id: string; alias: string }[]): PushTarget[] {
  // Walk the pool's hosts, not the owners: `owners` remembers every daemon ever seen to front a wwd, including ones whose pool host is no longer listed.
  const aliasOf = new Map<string, string>()
  for (const h of poolHosts) {
    const owner = owners[h.host_id]
    if (owner !== undefined && !aliasOf.has(owner)) aliasOf.set(owner, h.alias)
  }
  const order = useHostStore.getState().hostOrder
  const ids = [...aliasOf.keys()]
  const rank = (id: string) => { const i = order.indexOf(id); return i < 0 ? order.length : i }
  return ids.sort((a, b) => rank(a) - rank(b)).map((id) => ({ hostConfigId: id, alias: aliasOf.get(id) ?? id }))
}

export async function readPush(targets: readonly PushTarget[]): Promise<PushHost[]> {
  return Promise.all(targets.map(async (t): Promise<PushHost> => {
    if (!isConnected(t.hostConfigId)) return { ...t, connected: false, on: null, revision: null, error: null }
    try {
      const body = await fetchHostConfig(t.hostConfigId)
      const row = parseRow(isObj(body) ? body.wwdAlerts : null)
      return { ...t, connected: true, on: row?.push ?? null, revision: row?.revision ?? null, error: null }
    } catch (e) {
      return { ...t, connected: true, on: null, revision: null, error: messageOf(e) }
    }
  }))
}

/** Hosts that cannot be read count for neither side; nothing readable at all is unknown. */
export function pushStateOf(hosts: readonly PushHost[]): PushState {
  const known = hosts.filter((h) => h.on !== null)
  if (known.length === 0) return 'unknown'
  if (known.every((h) => h.on === true)) return 'on'
  if (known.every((h) => h.on === false)) return 'off'
  return 'partial'
}

/** One host: read the revision, write; a 409 whose copy already has the value is done, any other 409 is sent once more on the revision it carries. */
async function writeOne(id: string, on: boolean): Promise<{ ok: boolean; error?: string }> {
  try {
    const cur = parseRow((await fetchHostConfig(id) as Record<string, unknown> | null)?.wwdAlerts)
    try {
      await putHostConfig(id, 'wwd_alerts', { push: on }, cur?.revision ?? 0)
    } catch (e) {
      if (!(e instanceof HostConfigConflictError)) throw e
      const now = parseRow({ items: (e.current as { items?: unknown }).items, revision: e.current.revision })
      if (now === null) throw e
      if (now.push === on) return { ok: true }
      await putHostConfig(id, 'wwd_alerts', { push: on }, now.revision)
    }
    return { ok: true }
  } catch (e) {
    return { ok: false, error: messageOf(e) }
  }
}

// ── the queue of hosts that were offline when the switch was pressed ──
// `queue` holds the value each host is to have (the last press wins). Every write to a host, from a press or from the queue, goes through `serial`, so two writes to one host never overlap and the older
// never lands after the newer: a write always sends the value the queue holds when it STARTS.
const queue = new Map<string, boolean>()
const tails = new Map<string, Promise<unknown>>()
let unsubscribe: (() => void) | null = null
let snapshot: string[] = []
const listeners = new Set<() => void>()
function changed(): void {
  snapshot = [...queue.keys()]
  for (const l of listeners) l()
}
/** For `useSyncExternalStore`: the ids waiting in the queue; the same array until the queue changes. */
export const pendingPush = (): string[] => snapshot
export const subscribePending = (l: () => void): (() => void) => { listeners.add(l); return () => { listeners.delete(l) } }

function stopWhenEmpty(): void {
  if (queue.size === 0 && unsubscribe) { unsubscribe(); unsubscribe = null }
}
function keepWaiting(): void {
  // `runtime` is replaced on every latency or tmux update: only a host's own change to `connected` is a reason to write it.
  unsubscribe ??= useHostStore.subscribe((s, prev) => {
    if (s.runtime === prev.runtime) return
    flush((id) => prev.runtime[id]?.status !== 'connected' && s.runtime[id]?.status === 'connected')
  })
}
/** One write at a time per host, in the order they were asked for. `fn` never rejects. */
function serial<T>(id: string, fn: () => Promise<T>): Promise<T> {
  const p = (tails.get(id) ?? Promise.resolve()).then(fn)
  tails.set(id, p)
  void p.finally(() => { if (tails.get(id) === p) tails.delete(id) })
  return p
}

/**
 * Write the value the queue holds for `id` now. A write that succeeds clears the entry unless a newer press replaced the value meanwhile (that press has its own write behind this one).
 * One that fails because the host dropped goes back to waiting; one that fails while the host is still there is an error for a press, and stays queued for the queue's own retry (`byPress` false).
 */
async function run(id: string, byPress: boolean): Promise<PushResult> {
  const want = queue.get(id)
  if (want === undefined) return { hostConfigId: id, ok: true } // already applied by a write that ran before this one
  const r = await writeOne(id, want)
  if (r.ok) {
    if (queue.get(id) === want) { queue.delete(id); changed(); stopWhenEmpty() }
    return { hostConfigId: id, ok: true }
  }
  if (!isConnected(id)) { changed(); keepWaiting(); return { hostConfigId: id, ok: false, queued: true } }
  if (byPress && queue.get(id) === want) { queue.delete(id); changed(); stopWhenEmpty() }
  return { hostConfigId: id, ok: false, error: r.error }
}
/** Every waiting host that `connecting` names is written once. A write that fails waits for that host's next connection, it is not retried in a loop. */
function flush(connecting: (id: string) => boolean): void {
  for (const id of [...queue.keys()]) if (connecting(id) && isConnected(id)) void serial(id, () => run(id, false))
}

/** Write every host: connected ones now, one after another (a failure does not stop the next); offline ones go into the queue. */
export async function writePushAll(on: boolean, hosts: readonly PushHost[]): Promise<PushResult[]> {
  const out: PushResult[] = []
  for (const h of hosts) {
    const id = h.hostConfigId
    queue.set(id, on) // the press replaces whatever was waiting for this host
    // The connection as it is now, not as it was when the switch was read.
    if (!isConnected(id)) { changed(); keepWaiting(); out.push({ hostConfigId: id, ok: false, queued: true }); continue }
    out.push(await serial(id, () => run(id, true)))
  }
  changed()
  return out
}

/** Tests only. */
export function clearPushQueue(): void {
  queue.clear(); tails.clear(); stopWhenEmpty(); changed()
}
