// spa/src/components/team/TeamDisplayProvider.tsx — feeds the team structure (team-display.ts) to the tab surfaces
// (plan TI-1a). No surface reads it yet. Everything it needs is a store selector that returns the store's own object, so
// a write that changes none of them does not run this body; the index is built once per input change (useMemo) and the
// context value is rebuilt only when the structural signature changes — a roster frame that only moves a seat's model /
// effort / context leaves the value (and so every consumer) alone. Live readings are not in the context: the panel
// selects them from `useTeamRosterStore` per seat.
import { useMemo, type ReactNode } from 'react'
import { useTeamViews } from '../../hooks/useTeamViews'
import { buildTeamIndex } from '../../lib/team/team-index'
import { useSessionStore } from '../../stores/useSessionStore'
import { useTabStore } from '../../stores/useTabStore'
import { useTeamUiStore } from '../../stores/useTeamUiStore'
import { useWorkspaceStore } from '../../features/workspace/store'
import { TeamDisplayContext, type TeamDisplay } from './team-display'
import { buildTeamDisplay, structureSignature } from './team-structure'

export function TeamDisplayProvider({ children }: { children: ReactNode }) {
  const memberOrder = useTeamUiStore((s) => s.memberOrder)
  const collapsed = useTeamUiStore((s) => s.collapsed)
  const panelMode = useTeamUiStore((s) => s.panelMode)
  const ghostWorkspace = useTeamUiStore((s) => s.ghostWorkspace)
  const beadHost = useTeamUiStore((s) => s.teamBeadHost)
  const views = useTeamViews(memberOrder)
  const tabsById = useTabStore((s) => s.tabs)
  const workspaces = useWorkspaceStore((s) => s.workspaces)
  const sessionsByHost = useSessionStore((s) => s.sessions)

  const index = useMemo(() => buildTeamIndex(views, tabsById, sessionsByHost), [views, tabsById, sessionsByHost])
  const input = { views, index, workspaces, sessionsByHost, collapsed, panelMode, ghostWorkspace, beadHost }
  const signature = structureSignature(input)

  // Rebuilt when the signature changes, not when the inputs' identities do. The memo is keyed by the signature alone on
  // purpose: the value built from inputs with this signature is the value for any inputs with the same one, because the
  // readers use nothing the signature leaves out (a seat's model / effort / context are not read).
  // eslint-disable-next-line react-hooks/exhaustive-deps
  const value: TeamDisplay = useMemo(() => buildTeamDisplay(input), [signature])
  return <TeamDisplayContext.Provider value={value}>{children}</TeamDisplayContext.Provider>
}
