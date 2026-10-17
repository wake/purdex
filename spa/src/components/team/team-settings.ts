// spa/src/components/team/team-settings.ts — what a team's settings (TeamSettingsPanel.tsx, journal v5 §3.9) can offer on the
// lead's host: each section is there only when the host and the roster can serve it, and the gear only when at least one is.
import { useTeamRosterStore } from '../../stores/useTeamRosterStore'
import { useUnattendedStore } from '../../stores/useUnattendedStore'
import type { TeamRoster } from '../../lib/team/roster'

/** The team carries its cap (`max_members`) and its usage (`in_use`): without the usage − could not know where to stop. */
export function hasCap(team: TeamRoster): boolean {
  return team.max_members !== undefined && team.in_use !== undefined
}

/** The daemon reports the team's roots (and their revision, which a write must send back). */
export function hasRoots(team: TeamRoster): boolean {
  return team.roots !== undefined && team.roots_rev !== undefined
}

export interface TeamSettingsOffer {
  /** The roster's team, when this host's roster holds it. */
  team: TeamRoster | undefined
  /** 「成員上限」: the host lists `team.max_members.v1` and the roster carries the cap. */
  cap: boolean
  /** 「資料夾」: the roster carries the roots. */
  folders: boolean
  /** 「接力額度」: the host lists `team.relay_quota.v1`. */
  quota: boolean
}

export function useTeamSettingsOffer(hostId: string, teamId: string): TeamSettingsOffer {
  const team = useTeamRosterStore((s) => s.byHost[hostId]?.find((r) => r.id === teamId))
  const capOn = useUnattendedStore((s) => s.byHost[hostId]?.maxMembersSupport === 'yes')
  const quota = useUnattendedStore((s) => s.byHost[hostId]?.quotaSupport === 'yes')
  return { team, cap: team !== undefined && capOn && hasCap(team), folders: team !== undefined && hasRoots(team), quota: team !== undefined && quota }
}

/** Something to set: the gear shows only then. */
export const offersAny = (o: TeamSettingsOffer): boolean => o.cap || o.folders || o.quota
