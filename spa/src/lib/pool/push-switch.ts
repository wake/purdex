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
const queue = new Map<string, boolean>()
const flushing = new Set<string>()
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
async function flush(): Promise<void> {
  for (const [id, on] of [...queue]) {
    if (!isConnected(id) || flushing.has(id)) continue
    flushing.add(id)
    let want = on
    for (;;) {
      const r = await writeOne(id, want)
      const now = queue.get(id)
      if (!r.ok) break // stays queued; the next connection change tries it again, not this loop
      if (now === want) { queue.delete(id); changed(); break }
      if (now === undefined) break
      want = now // a newer press for this host landed while it was being written: send that one too
    }
    flushing.delete(id)
  }
  stopWhenEmpty()
}
function enqueue(id: string, on: boolean): void {
  queue.set(id, on)
  changed()
  unsubscribe ??= useHostStore.subscribe((s, prev) => { if (s.runtime !== prev.runtime) void flush() })
}

/** Write every host: connected ones now, one after another (a failure does not stop the next); offline ones go into the queue. */
export async function writePushAll(on: boolean, hosts: readonly PushHost[]): Promise<PushResult[]> {
  const out: PushResult[] = []
  for (const h of hosts) {
    // The connection as it is now, not as it was when the switch was read.
    if (!isConnected(h.hostConfigId)) { enqueue(h.hostConfigId, on); out.push({ hostConfigId: h.hostConfigId, ok: false, queued: true }); continue }
    queue.delete(h.hostConfigId) // a press that reaches a connected host replaces what was waiting for it
    if (queue.size === 0) stopWhenEmpty()
    const r = await writeOne(h.hostConfigId, on)
    out.push({ hostConfigId: h.hostConfigId, ...r })
  }
  changed()
  return out
}

/** Tests only. */
export function clearPushQueue(): void {
  queue.clear(); flushing.clear(); stopWhenEmpty(); changed()
}
