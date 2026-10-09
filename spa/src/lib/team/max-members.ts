// spa/src/lib/team/max-members.ts — the team cap stepper's write path (`PUT /api/team/max-members`, 1f's wire 2026-10-09).
//
// A click sends the ABSOLUTE value (current ± 1). One request per team at a time: while it is out the stepper's buttons
// are disabled (`useMaxMembersStore.inflight`), so there is nothing to coalesce and nothing to merge. There is no
// optimistic value: what the stepper shows is the roster's `max_members` / `in_use`, which the PUT's answer and the
// `team.roster` events set. Failures toast: 409 names the members in use, 404 says the team ended, the rest carry the code.
import { create } from 'zustand'
import { ApprovalApiError } from './approval-api'
import { hostIdentityNow } from './quota-host'
import { putMaxMembers } from './unattended-api'
import { hostLabel, hostLookOf } from '../host-look'
import { useI18nStore } from '../../stores/useI18nStore'
import { useTeamRosterStore } from '../../stores/useTeamRosterStore'
import { useUndoToast } from '../../stores/useUndoToast'
import type { TeamRoster } from './roster'
import { MAX_MEMBERS_MAX, MAX_MEMBERS_MIN, type MaxMembersView } from './types'

export const teamKey = (hostId: string, teamId: string): string => `${hostId}\u0000${teamId}`

/** One request in flight: its token (so only the request that made the entry may end it) and the daemon it went to. */
export interface Flight {
  token: number
  identity: string
}

interface MaxMembersState {
  inflight: Record<string, Flight>
  begin: (key: string, flight: Flight) => void
  /** Ends the flight only if it is still this request's: a replaced entry belongs to a newer request. */
  end: (key: string, token: number) => void
  /** A removed or re-pointed host: what was in flight went to a daemon that is no longer the host's. */
  forgetHost: (hostId: string) => void
  reset: () => void
}

export const useMaxMembersStore = create<MaxMembersState>()((set) => ({
  inflight: {},
  begin: (key, flight) => set((s) => ({ inflight: { ...s.inflight, [key]: flight } })),
  end: (key, token) => set((s) => {
    if (s.inflight[key]?.token !== token) return s
    const { [key]: _gone, ...rest } = s.inflight
    return { inflight: rest }
  }),
  forgetHost: (hostId) => set((s) => {
    const prefix = `${hostId}\u0000`
    const keep = Object.entries(s.inflight).filter(([k]) => !k.startsWith(prefix))
    return keep.length === Object.keys(s.inflight).length ? s : { inflight: Object.fromEntries(keep) }
  }),
  reset: () => set({ inflight: {} }),
}))

let nextToken = 0

export interface MaxMembersTarget {
  hostId: string
  teamId: string
  /** The lead's name for the toasts. */
  label: string
}

export interface MaxMembersDeps {
  put: (hostId: string, teamId: string, value: number) => Promise<MaxMembersView>
  toast: (message: string) => void
  message: (key: string, params: Record<string, string>) => string
  hostLabel: (hostId: string) => string
  identity: (hostId: string) => string | null
}

const defaultDeps = (): MaxMembersDeps => ({
  put: (hostId, teamId, value) => putMaxMembers(hostId, teamId, value), // resolved at call time
  toast: (m) => useUndoToast.getState().show(m),
  message: (key, params) => useI18nStore.getState().t(key, params),
  hostLabel: (hostId) => hostLabel(hostId, hostLookOf(hostId)),
  identity: hostIdentityNow,
})

let deps: MaxMembersDeps = defaultDeps()

/** Test seam: replace some of the dependencies. */
export function configureMaxMembers(over: Partial<MaxMembersDeps>): void {
  deps = { ...deps, ...over }
}

export function resetMaxMembers(): void {
  deps = defaultDeps()
  useMaxMembersStore.getState().reset()
}

/** Put the answer into the host's roster, unless a roster event already did (see `setMaxMembers`). */
function applyAnswer(hostId: string, view: MaxMembersView): void {
  const store = useTeamRosterStore.getState()
  const teams = store.byHost[hostId]
  if (teams === undefined || !teams.some((t) => t.id === view.team_id)) return
  store.apply(hostId, teams.map((t) => (t.id === view.team_id ? { ...t, max_members: view.max_members, in_use: view.in_use } : t)))
}

const teamOf = (hostId: string, teamId: string): TeamRoster | undefined => useTeamRosterStore.getState().byHost[hostId]?.find((t) => t.id === teamId)

/**
 * Send the cap. Ignored while a request for the team is out, when the host is gone, or for a value the daemon would
 * refuse anyway (outside 1-8, or below the members in use). The answer is shown through the roster; if a roster event
 * changed THIS team's numbers at any moment while the request was out, that event is at least as new as this answer (the
 * daemon emits it after the write) and the answer is not applied over it. Events about other teams do not matter.
 */
export async function setMaxMembers(target: MaxMembersTarget, value: number): Promise<void> {
  const { hostId, teamId } = target
  const key = teamKey(hostId, teamId)
  const store = useMaxMembersStore.getState()
  const identity = deps.identity(hostId)
  if (identity === null) return
  const before = teamOf(hostId, teamId)
  if (!Number.isInteger(value) || value < MAX_MEMBERS_MIN || value > MAX_MEMBERS_MAX || value < (before?.in_use ?? 0)) return
  // A request out to the daemon the host means now holds the team; one out to an earlier daemon does not.
  if (store.inflight[key]?.identity === identity) return
  const token = ++nextToken
  store.begin(key, { token, identity })
  // Watch THIS team's numbers for the whole flight: a change at any moment (even one that was later changed back, an
  // A -> B -> A) is an event newer than the request, and the answer must not be put over it.
  let touched = false
  let seen = { max: before?.max_members, used: before?.in_use }
  const unwatch = useTeamRosterStore.subscribe(() => {
    const t = teamOf(hostId, teamId)
    if (t?.max_members !== seen.max || t?.in_use !== seen.used) {
      touched = true
      seen = { max: t?.max_members, used: t?.in_use }
    }
  })
  try {
    const view = await deps.put(hostId, teamId, value)
    if (deps.identity(hostId) !== identity) return // re-pointed or removed meanwhile: the answer is the old daemon's
    if (!touched) applyAnswer(hostId, view)
  } catch (e: unknown) {
    if (deps.identity(hostId) !== identity) return
    const code = e instanceof ApprovalApiError ? e.code : 'error'
    if (code === 'max_below_in_use') {
      const inUse = e instanceof ApprovalApiError ? e.body?.in_use : undefined
      deps.toast(deps.message('unattended.cap.below', { n: typeof inUse === 'number' ? String(inUse) : '?' }))
    } else if (code === 'not_found') {
      deps.toast(deps.message('unattended.cap.ended', {}))
    } else {
      deps.toast(deps.message('unattended.cap.failed', { host: deps.hostLabel(hostId), session: target.label, code }))
    }
  } finally {
    unwatch()
    useMaxMembersStore.getState().end(key, token)
  }
}
