// spa/src/lib/team/lead-name.ts — what the unattended panel calls a row that stands for a team (journal v5 U15):
// the team's name from the roster, with the lead's own title after it (muted) when that differs. A daemon that predates
// names sends `team_name: ''`, then the lead's title stands in. Rosters of every host are searched: a lead on another host
// still leads the team that host's roster lists.
import type { TeamRoster } from './roster'

export interface LeadName {
  /** The team name, or the lead's title when the team has no name. */
  name: string
  /** The lead's title, only when a team name is shown and the title differs from it. */
  title?: string
}

function findTeam(rosters: Record<string, TeamRoster[]>, match: (t: TeamRoster) => boolean): TeamRoster | undefined {
  for (const teams of Object.values(rosters)) {
    const hit = teams.find(match)
    if (hit !== undefined) return hit
  }
  return undefined
}

function nameOfTeam(team: TeamRoster, fallbackTitle: string | undefined): LeadName | undefined {
  const teamName = team.team_name.trim()
  const title = (fallbackTitle ?? team.lead.title ?? '').trim()
  if (teamName === '') return title !== '' ? { name: title } : undefined
  return title !== '' && title !== teamName ? { name: teamName, title } : { name: teamName }
}

/** The name of the team a session leads, or undefined when it leads none (a solo session, a member) or nothing names it. */
export function leadNameBySession(rosters: Record<string, TeamRoster[]>, sessionId: string, title?: string): LeadName | undefined {
  const team = findTeam(rosters, (t) => t.lead.session_id === sessionId)
  return team === undefined ? undefined : nameOfTeam(team, title)
}

/** The same, for a team known by its id (a member-relay request names its team). */
export function leadNameByTeam(rosters: Record<string, TeamRoster[]>, teamId: string, title?: string): LeadName | undefined {
  const team = findTeam(rosters, (t) => t.id === teamId)
  return team === undefined ? undefined : nameOfTeam(team, title)
}
