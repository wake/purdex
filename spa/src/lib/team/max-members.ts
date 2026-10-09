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
import type { MaxMembersView } from './types'

export const teamKey = (hostId: string, teamId: string): string => `${hostId}\u0000${teamId}`

interface MaxMembersState {
  inflight: Record<string, true>
  begin: (key: string) => void
  end: (key: string) => void
  reset: () => void
}

export const useMaxMembersStore = create<MaxMembersState>()((set) => ({
  inflight: {},
  begin: (key) => set((s) => ({ inflight: { ...s.inflight, [key]: true } })),
  end: (key) => set((s) => {
    if (!s.inflight[key]) return s
    const { [key]: _gone, ...rest } = s.inflight
    return { inflight: rest }
  }),
  reset: () => set({ inflight: {} }),
}))

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

/**
 * Send the cap. Ignored while a request for the team is out, or when the host is gone. The answer is shown through the
 * roster; if a roster event for the host arrived while the request was out, that event is at least as new as this answer
 * (the daemon emits it after the write) and the answer is not applied over it.
 */
export async function setMaxMembers(target: MaxMembersTarget, value: number): Promise<void> {
  const { hostId, teamId } = target
  const key = teamKey(hostId, teamId)
  const store = useMaxMembersStore.getState()
  if (store.inflight[key]) return
  const identity = deps.identity(hostId)
  if (identity === null) return
  store.begin(key)
  const rosterBefore = useTeamRosterStore.getState().byHost[hostId]
  try {
    const view = await deps.put(hostId, teamId, value)
    if (deps.identity(hostId) !== identity) return // re-pointed or removed meanwhile: the answer is the old daemon's
    if (useTeamRosterStore.getState().byHost[hostId] === rosterBefore) applyAnswer(hostId, view)
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
    useMaxMembersStore.getState().end(key)
  }
}
