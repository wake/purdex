// spa/src/hooks/useTeamViews.ts — the team views (lib/team/team-views.ts) for React. Every selector returns the store's
// own object (the roster map, the tab map, the workspace list, the session map, the host order), never a fresh one, so
// a store write that changes none of them does not re-render, and the views are rebuilt (useMemo) only when one of
// those inputs really changed. Selecting a derived array/object here instead would be a new reference on every store
// notification and loop.
import { useMemo } from 'react'
import { useHostStore } from '../stores/useHostStore'
import { useSessionStore } from '../stores/useSessionStore'
import { useTabStore } from '../stores/useTabStore'
import { useTeamRosterStore } from '../stores/useTeamRosterStore'
import { useWorkspaceStore } from '../features/workspace/store'
import { selectTeamViews, type TeamView } from '../lib/team/team-views'

/** `memberOrder` (per team key) is the interface PRs' to persist; pass a stable reference or `undefined`. */
export function useTeamViews(memberOrder?: Record<string, readonly string[]>): TeamView[] {
  const rosterByHost = useTeamRosterStore((s) => s.byHost)
  const tabsById = useTabStore((s) => s.tabs)
  const workspaces = useWorkspaceStore((s) => s.workspaces)
  const activeWorkspaceId = useWorkspaceStore((s) => s.activeWorkspaceId)
  const sessionsByHost = useSessionStore((s) => s.sessions)
  const hostOrder = useHostStore((s) => s.hostOrder)
  return useMemo(
    () => selectTeamViews({ rosterByHost, tabsById, workspaces, activeWorkspaceId, sessionsByHost, memberOrder, hostOrder }),
    [rosterByHost, tabsById, workspaces, activeWorkspaceId, sessionsByHost, memberOrder, hostOrder],
  )
}
