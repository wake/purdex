// spa/src/components/recap/TitleBarRecap.tsx — the title bar's recap button for the tab in view: a team seat's (the team the tab belongs to) or a personal session's.
// TitleBar.tsx only places them, beside the panel's own button (TeamNotebookButton / PersonalButton), which keeps its job of moving the panel.
import { useSeat } from '../../lib/names/seat'
import { useTabStore } from '../../stores/useTabStore'
import { useTeamUiStore } from '../../stores/useTeamUiStore'
import type { TeamPanelTeam } from '../team/team-display'
import { usePersonalNames } from '../team/usePersonalPanel'
import type { PersonalTarget } from '../../lib/task-groups/personal'
import { TitleBarRecapButton } from './RecapButton'

export function TeamTitleBarRecap({ team }: { team: TeamPanelTeam }) {
  const activeTabId = useTabStore((s) => s.activeTabId)
  const seat = [team.lead, ...team.members].find((s) => s.tabId !== null && s.tabId === activeTabId)
  const persona = useSeat(seat?.address ?? '')
  if (!seat) return null
  return (
    <TitleBarRecapButton
      target={{ scope: team.teamKey, teamKey: team.teamKey, hostId: seat.hostId, sessionId: seat.sessionId, title: seat.title, address: seat.address }}
      name={persona?.name ?? seat.title}
      panelFull={team.mode === 'full'}
    />
  )
}

export function PersonalTitleBarRecap({ target }: { target: PersonalTarget }) {
  const { name } = usePersonalNames(target)
  const panelFull = useTeamUiStore((s) => s.sharedPanelMode === 'full')
  return (
    <TitleBarRecapButton
      target={{ scope: target.tabId, teamKey: null, hostId: target.hostId, sessionId: target.agentSessionId, title: '', address: '' }}
      name={name}
      panelFull={panelFull}
    />
  )
}
