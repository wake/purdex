// spa/src/lib/team/roots.ts — the live team's roots editor's write path (`PUT /api/team/roots`, #2561 wire, 1f 2026-10-11).
//
// An edit sends the WHOLE new list with the revision it was read at (`if_rev`). One request per team at a time (the editor's
// controls are disabled while it is out). There is no optimistic list: what the editor shows is the roster's `roots`, which the
// answer and the `team.roster` events set. A refusal is kept on the team (`error`) for the editor to show next to the thing that caused
// it, and cleared by the next edit. The path being typed lives here too (not in a component): the panel unmounts with its dialog/tab.
import { create } from 'zustand'
import { ApprovalApiError } from './approval-api'
import { hostIdentityNow } from './quota-host'
import { putRoots, type RootsView } from './unattended-api'
import { useTeamRosterStore } from '../../stores/useTeamRosterStore'
import type { TeamRoster } from './roster'
import { teamKey, type Flight } from './max-members'

export const ROOTS_MAX = 16

export interface InUseItem {
  root: string
  /** 'member' | 'spawn' | 'adoption' (anything else the daemon adds is shown as it is). */
  kind: string
  /** The member's address ref (`_xxxxxx`) when it has one. */
  ref?: string
  cwd: string
}

export type RootsError =
  /** 400: `index` is the row the daemon named (`roots[3]`), null for the list as a whole or the row being added. */
  | { kind: 'bad_request'; detail: string; index: number | null }
  /** 409: someone edited first; the list was refreshed from the answer and nothing of this edit applied. */
  | { kind: 'rev_mismatch' }
  /** 409: members / spawns / adoptions stand under a root this edit removes; nothing applied. */
  | { kind: 'in_use'; items: InUseItem[]; removed: string | null }
  /** 404: the team has ended. */
  | { kind: 'ended' }
  | { kind: 'failed'; code: string }
  /** 200, but the stored list is not the sent one's size: a path inside (or the same real path as) an existing root was merged into it. Not a refusal. */
  | { kind: 'merged' }

interface TeamEdit {
  error: RootsError | null
  /** The path being typed into the add box. */
  draft: string
}

const NONE: TeamEdit = { error: null, draft: '' }

interface RootsState {
  inflight: Record<string, Flight>
  byTeam: Record<string, TeamEdit>
  begin: (key: string, flight: Flight) => void
  end: (key: string, token: number) => void
  setError: (key: string, error: RootsError | null) => void
  setDraft: (key: string, draft: string) => void
  forgetHost: (hostId: string) => void
  reset: () => void
}

export const useRootsStore = create<RootsState>()((set) => ({
  inflight: {},
  byTeam: {},
  begin: (key, flight) => set((s) => ({ inflight: { ...s.inflight, [key]: flight } })),
  end: (key, token) => set((s) => {
    if (s.inflight[key]?.token !== token) return s
    const { [key]: _gone, ...rest } = s.inflight
    return { inflight: rest }
  }),
  setError: (key, error) => set((s) => ({ byTeam: { ...s.byTeam, [key]: { ...(s.byTeam[key] ?? NONE), error } } })),
  setDraft: (key, draft) => set((s) => ({ byTeam: { ...s.byTeam, [key]: { ...(s.byTeam[key] ?? NONE), draft } } })),
  forgetHost: (hostId) => set((s) => {
    const prefix = `${hostId}\u0000`
    const drop = (o: Record<string, unknown>) => Object.fromEntries(Object.entries(o).filter(([k]) => !k.startsWith(prefix)))
    return { inflight: drop(s.inflight) as RootsState['inflight'], byTeam: drop(s.byTeam) as RootsState['byTeam'] }
  }),
  reset: () => set({ inflight: {}, byTeam: {} }),
}))

export const editOf = (s: Pick<RootsState, 'byTeam'>, key: string): TeamEdit => s.byTeam[key] ?? NONE

export interface RootsDeps {
  put: (hostId: string, teamId: string, roots: readonly string[], ifRev: number) => Promise<RootsView>
  identity: (hostId: string) => string | null
}

const defaultDeps = (): RootsDeps => ({ put: (h, t, r, v) => putRoots(h, t, r, v), identity: hostIdentityNow })
let deps: RootsDeps = defaultDeps()

/** Test seam. */
export function configureRoots(over: Partial<RootsDeps>): void { deps = { ...deps, ...over } }
export function resetRoots(): void { deps = defaultDeps(); useRootsStore.getState().reset() }

const teamOf = (hostId: string, teamId: string): TeamRoster | undefined => useTeamRosterStore.getState().byHost[hostId]?.find((t) => t.id === teamId)

function patchRoster(hostId: string, teamId: string, roots: string[], rev: number): void {
  const store = useTeamRosterStore.getState()
  const teams = store.byHost[hostId]
  if (teams === undefined || !teams.some((t) => t.id === teamId)) return
  store.apply(hostId, teams.map((t) => (t.id === teamId ? { ...t, roots, roots_rev: rev } : t)))
}

/** What a typed path may not be, before the daemon is asked (it checks the same and more: the directory must exist). */
export type RootInputRefusal = 'empty' | 'relative' | 'duplicate' | 'full'
export function checkRootInput(text: string, current: readonly string[]): { ok: true; path: string } | { ok: false; reason: RootInputRefusal } {
  const path = text.trim()
  if (path === '') return { ok: false, reason: 'empty' }
  if (!path.startsWith('/')) return { ok: false, reason: 'relative' }
  if (current.length >= ROOTS_MAX) return { ok: false, reason: 'full' }
  if (current.includes(path)) return { ok: false, reason: 'duplicate' }
  return { ok: true, path }
}

const asItems = (v: unknown): InUseItem[] => {
  if (!Array.isArray(v)) return []
  const out: InUseItem[] = []
  for (const x of v) {
    if (typeof x !== 'object' || x === null) continue
    const o = x as Record<string, unknown>
    if (typeof o.root !== 'string' || typeof o.kind !== 'string') continue
    out.push({ root: o.root, kind: o.kind, ...(typeof o.ref === 'string' ? { ref: o.ref } : {}), cwd: typeof o.cwd === 'string' ? o.cwd : '' })
  }
  return out
}

const rowIndex = (field: unknown): number | null => {
  const m = typeof field === 'string' ? /^roots\[(\d+)\]$/.exec(field) : null
  return m ? Number(m[1]) : null
}

/**
 * Write `next` (the whole list) for the team. Ignored while a request for the team is out, when the host is gone, or when the roster
 * has no revision for the team (a daemon that predates the route). Returns true when the daemon took it.
 */
export async function saveRoots(hostId: string, teamId: string, next: readonly string[], removedAt: number | null = null): Promise<boolean> {
  const key = teamKey(hostId, teamId)
  const store = useRootsStore.getState()
  const identity = deps.identity(hostId)
  const team = teamOf(hostId, teamId)
  if (identity === null || team?.roots === undefined || team.roots_rev === undefined) return false
  if (store.inflight[key]?.identity === identity) return false
  const ifRev = team.roots_rev
  const removed = removedAt === null ? null : (team.roots[removedAt] ?? null)
  const token = Date.now() + Math.random()
  store.begin(key, { token, identity })
  store.setError(key, null)
  try {
    const view = await deps.put(hostId, teamId, next, ifRev)
    if (deps.identity(hostId) !== identity) return false
    // a roster event that already moved the revision is at least as new as this answer (the daemon emits it after the write)
    if ((teamOf(hostId, teamId)?.roots_rev ?? -1) <= ifRev) patchRoster(hostId, teamId, view.roots, view.roots_rev)
    if (view.roots.length !== next.length) useRootsStore.getState().setError(key, { kind: 'merged' })
    return true
  } catch (e: unknown) {
    if (deps.identity(hostId) !== identity) return false
    if (!(e instanceof ApprovalApiError)) { useRootsStore.getState().setError(key, { kind: 'failed', code: 'error' }); return false }
    const body = e.body ?? {}
    let err: RootsError
    if (e.code === 'bad_request') {
      // the daemon counts rows of the list it was SENT; the editor shows the list it has: after a removal, the rows behind it moved up by one
      const sent = rowIndex(body.field)
      err = { kind: 'bad_request', detail: e.detail, index: sent !== null && removedAt !== null && sent >= removedAt ? sent + 1 : sent }
    }
    else if (e.code === 'not_found') err = { kind: 'ended' }
    else if (e.code === 'rev_mismatch') {
      // the answer carries the list that won: show it (only if it is newer than what the roster has)
      const roots = Array.isArray(body.roots) && body.roots.every((x) => typeof x === 'string') ? (body.roots as string[]) : null
      const rev = typeof body.roots_rev === 'number' && Number.isInteger(body.roots_rev) ? body.roots_rev : null
      if (roots !== null && rev !== null && rev > (teamOf(hostId, teamId)?.roots_rev ?? -1)) patchRoster(hostId, teamId, roots, rev)
      err = { kind: 'rev_mismatch' }
    } else if (e.code === 'roots_in_use') err = { kind: 'in_use', items: asItems(body.in_use), removed }
    else err = { kind: 'failed', code: e.code }
    useRootsStore.getState().setError(key, err)
    return false
  } finally {
    useRootsStore.getState().end(key, token)
  }
}

/** A long path keeps its head and its tail: the directory it starts in and the one it is (the tooltip has all of it). */
export function midEllipsis(path: string, max = 56): string {
  if (path.length <= max) return path
  const keep = max - 1
  const head = Math.ceil(keep * 0.4)
  return `${path.slice(0, head)}…${path.slice(path.length - (keep - head))}`
}
