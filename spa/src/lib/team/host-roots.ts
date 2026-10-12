// spa/src/lib/team/host-roots.ts — the directories a lead host may use on OTHER paired hosts (XR-3; spec
// docs/specs/2026-10-12-host-roots-from-lead-spec-plan.md §3.2/§3.3). A folder line `alias:/abs/path` grants a folder of that host
// to the lead host (the whole lead host, every team: so the data is kept per lead host id, never per team). The lead host records the
// change and delivers it when the other host is reachable; what each host last said is read back with `GET /api/team/host-roots`.
//
// Reads: on mount, after every write, and every 10 s while a change is pending (the daemon has no event for it); one request per lead
// host at a time; an answer for a host that was removed or re-pointed meanwhile is dropped. A 404 is a lead daemon without the routes:
// the whole feature is off (`unsupported`), and `alias:/…` stays an ordinary local path.
import { create } from 'zustand'
import { ApprovalApiError, send } from './approval-api'
import { descriptorFor } from './unattended-api'
import { hostIdentityNow } from './quota-host'
import { useHostStore } from '../../stores/useHostStore'

export const HOST_ROOTS_PATH = '/api/team/host-roots'
export const HOST_ROOTS_DISMISS_PATH = '/api/team/host-roots/dismiss'
export const HOST_ROOTS_POLL_MS = 10_000

export type HostStatus = 'ok' | 'offline' | 'consent_off' | 'unsupported' | 'unauthorized' | 'unknown'
const STATUSES: readonly HostStatus[] = ['ok', 'offline', 'consent_off', 'unsupported', 'unauthorized', 'unknown']

export type ChangeState = 'pending' | 'refused' | 'dropped'

export interface HostRootChange {
  id: string
  op: 'add' | 'remove'
  path: string
  state: ChangeState
  code: string
  detail: string
}

export interface PairedHostRoots {
  hostId: string
  alias: string
  status: HostStatus
  roots: string[]
  changes: HostRootChange[]
}

const isRecord = (v: unknown): v is Record<string, unknown> => typeof v === 'object' && v !== null && !Array.isArray(v)
const str = (v: unknown): string => (typeof v === 'string' ? v : '')

function parseChange(v: unknown): HostRootChange | null {
  if (!isRecord(v)) return null
  const { id, op, path, state } = v
  if (typeof id !== 'string' || id === '' || typeof path !== 'string' || path === '') return null
  if (op !== 'add' && op !== 'remove') return null
  if (state !== 'pending' && state !== 'refused' && state !== 'dropped') return null
  return { id, op, path, state, code: str(v.code), detail: str(v.detail) }
}

function parseHost(v: unknown): PairedHostRoots | null {
  if (!isRecord(v) || typeof v.alias !== 'string' || v.alias === '') return null
  const status = STATUSES.find((s) => s === v.status) ?? 'unknown'
  const roots = Array.isArray(v.roots) ? v.roots.filter((r): r is string => typeof r === 'string' && r !== '') : []
  const changes = Array.isArray(v.changes) ? v.changes.map(parseChange).filter((c): c is HostRootChange => c !== null) : []
  return { hostId: str(v.host_id), alias: v.alias, status, roots, changes }
}

/** The answer of `GET /api/team/host-roots`: `{hosts: […]}`. Null when the outer shape is wrong; malformed rows are dropped. */
export function parseHostRoots(raw: unknown): PairedHostRoots[] | null {
  if (!isRecord(raw) || !Array.isArray(raw.hosts)) return null
  return raw.hosts.map(parseHost).filter((h): h is PairedHostRoots => h !== null)
}

/** What a typed folder line means. `local`: the team's own roots (the old path). */
export type RootInput =
  | { kind: 'local' }
  | { kind: 'remote'; host: PairedHostRoots; path: string }
  | { kind: 'unknown_host'; name: string }
  | { kind: 'not_absolute' }

/**
 * `alias:/abs/path` with a paired host's alias (any case) is a grant. `x:/…` with no such alias gets its own refusal (not the
 * misleading "not absolute"); `alias:rel` is refused as not absolute. Without the host list (no routes, not read yet) nothing is routed.
 */
export function classifyRootInput(text: string, hosts: readonly PairedHostRoots[] | null): RootInput {
  if (hosts === null) return { kind: 'local' }
  const m = /^([^:/\s]+):(.*)$/s.exec(text.trim())
  if (m === null) return { kind: 'local' }
  const [, name, rest] = m
  const host = hosts.find((h) => h.alias.toLowerCase() === name.toLowerCase())
  if (host === undefined) return rest.startsWith('/') ? { kind: 'unknown_host', name } : { kind: 'local' }
  const path = rest.trim()
  return path.startsWith('/') ? { kind: 'remote', host, path } : { kind: 'not_absolute' }
}

// ---- transport (a seam for tests) ----

export interface RootsInUseItem { title: string; ref: string; opId: string }
const asUses = (body: Record<string, unknown> | null): RootsInUseItem[] => {
  const uses = body !== null && Array.isArray(body.uses) ? body.uses : []
  return uses.filter(isRecord).map((u) => ({ title: str(u.title), ref: str(u.ref), opId: str(u.op_id) }))
}
/** 「title（ref）」, else the ref, else the first 8 characters of the spawn's op id. */
export function inUseWho(u: RootsInUseItem): string {
  if (u.title !== '' && u.ref !== '') return `${u.title}（${u.ref}）`
  if (u.title !== '') return u.title
  if (u.ref !== '') return u.ref
  return u.opId.slice(0, 8)
}

export interface HostRootsDeps {
  fetch: (hostId: string) => Promise<PairedHostRoots[] | 'unsupported'>
  post: (hostId: string, host: string, op: 'add' | 'remove', path: string) => Promise<void>
  dismiss: (hostId: string, id: string) => Promise<void>
  identity: (hostId: string) => string | null
}

const defaultDeps = (): HostRootsDeps => ({
  async fetch(hostId) {
    let raw: unknown
    try {
      raw = await send<unknown>(hostId, HOST_ROOTS_PATH, { method: 'GET', signal: AbortSignal.timeout(8000) })
    } catch (e) {
      if (e instanceof ApprovalApiError && e.status === 404) return 'unsupported'
      throw e
    }
    const hosts = parseHostRoots(raw)
    if (hosts === null) throw new ApprovalApiError(200, 'bad_response', 'the host-roots answer is not the wire shape')
    return hosts
  },
  async post(hostId, host, op, path) {
    const client = await descriptorFor(hostId)
    await send<unknown>(hostId, HOST_ROOTS_PATH, {
      method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ host, op, path, client }),
    })
  },
  async dismiss(hostId, id) {
    await send<unknown>(hostId, HOST_ROOTS_DISMISS_PATH, {
      method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ id }),
    })
  },
  identity: hostIdentityNow,
})
let deps: HostRootsDeps = defaultDeps()
export function configureHostRoots(over: Partial<HostRootsDeps>): void { deps = { ...deps, ...over } }

// ---- store (by LEAD host id) ----

/** An error shown on one row (`path` of host `alias`), or beside the input (`row === null`). */
export type HostRootsError =
  | { kind: 'in_use'; who: string[] }
  | { kind: 'unknown_host'; name: string }
  | { kind: 'bad_root'; detail: string }
  | { kind: 'not_a_root' }
  | { kind: 'failed'; code: string }

export interface RowError { hostKey: string; path: string; error: HostRootsError }
/** The stable key of a paired host (its id; the alias only when the daemon sent none): an alias can be renamed between a write and the re-read. */
export const hostKeyOf = (h: Pick<PairedHostRoots, 'hostId' | 'alias'>): string => (h.hostId !== '' ? h.hostId : h.alias)

interface LeadView {
  /** `unknown`: not read yet. `unsupported`: the lead daemon has no routes (the feature is hidden). */
  load: 'unknown' | 'ok' | 'unsupported'
  hosts: PairedHostRoots[]
  rowError: RowError | null
  /** True while a write is out (the controls are disabled). */
  writing: boolean
  /** A write finished and no read that began after it has landed yet: the view may predate it (keeps the poll running). */
  dirty: boolean
}

const EMPTY: LeadView = { load: 'unknown', hosts: [], rowError: null, writing: false, dirty: false }

interface HostRootsState {
  byLead: Record<string, LeadView>
  patch: (leadId: string, p: Partial<LeadView>) => void
  forget: (leadId: string) => void
  reset: () => void
}

export const useHostRootsStore = create<HostRootsState>()((set) => ({
  byLead: {},
  patch: (leadId, p) => set((s) => ({ byLead: { ...s.byLead, [leadId]: { ...(s.byLead[leadId] ?? EMPTY), ...p } } })),
  forget: (leadId) => set((s) => { const { [leadId]: _g, ...rest } = s.byLead; return { byLead: rest } }),
  reset: () => { reading.clear(); again.clear(); epochs.clear(); writing.clear(); set({ byLead: {} }) },
}))

export const viewOf = (s: Pick<HostRootsState, 'byLead'>, leadId: string): LeadView => s.byLead[leadId] ?? EMPTY

export const hasPending = (hosts: readonly PairedHostRoots[]): boolean => hosts.some((h) => h.changes.some((c) => c.state === 'pending'))

// ---- polling: one timer per lead host, however many editors are shown ----

interface Poll { count: number; timer: ReturnType<typeof setInterval> | null; identity: string | null }
const polls = new Map<string, Poll>()
let unsubscribe: (() => void) | null = null
let unsubscribeHosts: (() => void) | null = null

/** A timer runs for a lead host exactly while an editor is shown (`count > 0`) and a change of it is pending. */
function syncPolls(): void {
  for (const [leadId, p] of polls) {
    const now = deps.identity(leadId)
    if (now !== p.identity) {
      // the lead host was removed or re-pointed (same id, another endpoint or token): what was read belongs to another daemon
      p.identity = now
      useHostRootsStore.getState().forget(leadId)
      void refreshHostRoots(leadId)
    }
    const view = viewOf(useHostRootsStore.getState(), leadId)
    const want = p.count > 0 && (hasPending(view.hosts) || view.dirty)
    if (want && p.timer === null) p.timer = setInterval(() => { void refreshHostRoots(leadId) }, HOST_ROOTS_POLL_MS)
    else if (!want && p.timer !== null) { clearInterval(p.timer); p.timer = null }
  }
}

/** An editor of the lead host is shown: the first one reads the view; the view is re-read every 10 s while a change is pending. Call the result when it is collapsed. */
export function retainHostRootsPoll(leadId: string): () => void {
  let p = polls.get(leadId)
  if (p === undefined) { p = { count: 0, timer: null, identity: deps.identity(leadId) }; polls.set(leadId, p) }
  p.count++
  if (p.count === 1) void refreshHostRoots(leadId)
  unsubscribe ??= useHostRootsStore.subscribe(syncPolls)
  unsubscribeHosts ??= useHostStore.subscribe(syncPolls)
  syncPolls()
  let released = false
  return () => {
    if (released) return
    released = true
    p.count--
    if (p.count === 0) {
      if (p.timer !== null) clearInterval(p.timer)
      polls.delete(leadId)
      if (polls.size === 0) { unsubscribe?.(); unsubscribe = null; unsubscribeHosts?.(); unsubscribeHosts = null }
    }
  }
}

/** Test seam. */
export function resetHostRoots(): void {
  for (const p of polls.values()) if (p.timer !== null) clearInterval(p.timer)
  polls.clear()
  unsubscribe?.()
  unsubscribe = null
  unsubscribeHosts?.()
  unsubscribeHosts = null
  deps = defaultDeps()
  useHostRootsStore.getState().reset()
}

// ---- reading ----

const reading = new Map<string, string>() // lead host id → the identity the request in flight was sent to
const again = new Set<string>() // a write finished while a read was out: that read may predate it, read once more
const epochs = new Map<string, number>() // bumped when a write finishes: a read that began before it must not commit
const writing = new Set<string>()

/**
 * Read the lead host's view. One request at a time per lead host (a second ask joins the one in flight; `afterWrite` asks for one more
 * read when that one ends, since it may have been sent before the write); an answer for another daemon than the one asked is dropped.
 */
export async function refreshHostRoots(leadId: string, afterWrite = false): Promise<void> {
  const identity = deps.identity(leadId)
  if (identity === null) return
  if (reading.get(leadId) === identity) {
    if (afterWrite) again.add(leadId)
    return
  }
  reading.set(leadId, identity)
  const epoch = epochs.get(leadId) ?? 0
  try {
    const answer = await deps.fetch(leadId)
    if (deps.identity(leadId) !== identity || (epochs.get(leadId) ?? 0) !== epoch) return
    if (answer === 'unsupported') useHostRootsStore.getState().patch(leadId, { load: 'unsupported', hosts: [], rowError: null, dirty: false })
    else useHostRootsStore.getState().patch(leadId, { load: 'ok', hosts: answer, dirty: false })
  } catch {
    // an unreachable lead or a bad answer keeps what was shown; the next read tries again
  } finally {
    if (reading.get(leadId) === identity) reading.delete(leadId)
    if (again.delete(leadId)) void refreshHostRoots(leadId)
  }
}

// ---- writing ----

function errorOf(e: unknown, alias: string): HostRootsError {
  if (!(e instanceof ApprovalApiError)) return { kind: 'failed', code: 'error' }
  if (e.code === 'roots_in_use') return { kind: 'in_use', who: asUses(e.body).map(inUseWho) }
  if (e.code === 'unknown_host') return { kind: 'unknown_host', name: alias }
  if (e.code === 'bad_root') return { kind: 'bad_root', detail: e.detail }
  if (e.code === 'not_a_root') return { kind: 'not_a_root' }
  return { kind: 'failed', code: e.code }
}

export type WriteResult = { ok: true } | { ok: false; error: HostRootsError }

/**
 * Add or remove one folder of a paired host. Ignored (not ok) while another write of this lead host is out. A failure is kept on the
 * row (`rowError`) for a remove and returned for an add (the input shows it); the next action clears it.
 */
export async function changeHostRoot(leadId: string, host: PairedHostRoots, op: 'add' | 'remove', path: string): Promise<WriteResult> {
  const identity = deps.identity(leadId)
  if (identity === null || writing.has(leadId)) return { ok: false, error: { kind: 'failed', code: 'busy' } }
  writing.add(leadId)
  const store = useHostRootsStore.getState()
  store.patch(leadId, { writing: true, rowError: null })
  try {
    await deps.post(leadId, host.alias, op, path)
    await settle(leadId, identity)
    return { ok: true }
  } catch (e) {
    const error = errorOf(e, host.alias)
    if (deps.identity(leadId) === identity) {
      if (op === 'remove') useHostRootsStore.getState().patch(leadId, { rowError: { hostKey: hostKeyOf(host), path, error } })
      await settle(leadId, identity)
    }
    return { ok: false, error }
  } finally {
    writing.delete(leadId)
    if (deps.identity(leadId) === identity) useHostRootsStore.getState().patch(leadId, { writing: false })
  }
}

/** Clear a refused / dropped change. 404 `no_such_change` and 409 `pending` are not failures: the view is re-read either way. */
export async function dismissHostRootChange(leadId: string, id: string): Promise<void> {
  const identity = deps.identity(leadId)
  if (identity === null || writing.has(leadId)) return
  writing.add(leadId)
  useHostRootsStore.getState().patch(leadId, { writing: true, rowError: null })
  try {
    await deps.dismiss(leadId, id)
  } catch {
    // nothing to tell: the re-read below shows what is true
  } finally {
    writing.delete(leadId)
    if (deps.identity(leadId) === identity) {
      useHostRootsStore.getState().patch(leadId, { writing: false })
      await settle(leadId, identity)
    }
  }
}

async function settle(leadId: string, identity: string): Promise<void> {
  if (deps.identity(leadId) !== identity) return
  epochs.set(leadId, (epochs.get(leadId) ?? 0) + 1) // a read already out predates this write: it will not commit
  useHostRootsStore.getState().patch(leadId, { dirty: true })
  await refreshHostRoots(leadId, true)
}
