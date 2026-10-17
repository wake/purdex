// spa/src/components/UnattendedHeldSection.tsx — 「額度用完，等你核准」 in the unattended panel (relay quota spec §3.5, plan RQ-A
// Task 6): the open self_relay requests the daemon holds because the session's quota ran out. Display only here: an open
// request already raises the approval dialog by itself, and opening one specific request from the panel needs a "show
// this request" action the approval store does not have (a follow-up).
import type { ReactNode } from 'react'
import { useI18nStore } from '../stores/useI18nStore'
import { hostLabel, hostLookOf } from '../lib/host-look'
import { approvalSessionLabel, memberRelayNames } from '../lib/team/approval-format'
import { sinceText } from '../lib/team/time-text'
import { clipForDisplay, type Approval } from '../lib/team/types'
import { useTeamRosterStore } from '../stores/useTeamRosterStore'
import { leadNameBySession, leadNameByTeam } from '../lib/team/lead-name'

export interface HeldRow {
  hostId: string
  a: Approval
}

/** The line, with the lead's title (when it has one) after it. */
function withNode(text: string, title: ReactNode) {
  return title === null ? text : <>{text} {title}</>
}

export function UnattendedHeldSection({ rows }: { rows: readonly HeldRow[] }) {
  const t = useI18nStore((s) => s.t)
  const rosters = useTeamRosterStore((s) => s.byHost)
  if (rows.length === 0) return null
  const sorted = [...rows].sort((x, y) => y.a.created_at - x.a.created_at)
  return (
    <section data-testid="held-section" className="flex flex-col gap-1">
      <div data-testid="held-title" className="font-medium text-text-primary">{t('unattended.held.title')}</div>
      <ul className="flex flex-col gap-1">
        {sorted.map(({ hostId, a }) => (
          <li key={`${hostId}:${a.id}`} data-testid="held-row" className="text-text-primary">
            {(() => {
              // the lead (a team) is named by its team name, its title after it muted (U15)
              const host = hostLabel(hostId, hostLookOf(hostId))
              const time = sinceText(a.created_at)
              if (a.kind === 'member_relay') {
                const n = memberRelayNames(a)
                const lead = leadNameByTeam(rosters, n.payload.team_id, n.lead)
                return withNode(t('unattended.held.row_member', { host, lead: clipForDisplay(lead?.name ?? n.lead, 40), member: clipForDisplay(n.member, 40), time }),
                  lead?.title !== undefined ? <span data-testid="held-lead-title" className="text-text-muted">{clipForDisplay(lead.title, 40)}</span> : null)
              }
              const lead = leadNameBySession(rosters, a.origin.session_id, approvalSessionLabel(a.origin))
              return withNode(t('unattended.held.row', { host, session: lead?.name !== undefined ? clipForDisplay(lead.name, 40) : approvalSessionLabel(a.origin), time }),
                lead?.title !== undefined ? <span data-testid="held-lead-title" className="text-text-muted">{clipForDisplay(lead.title, 40)}</span> : null)
            })()}
          </li>
        ))}
      </ul>
    </section>
  )
}
