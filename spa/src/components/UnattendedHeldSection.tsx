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

const SLOT = '\uE000'

/** The translated line with `node` in the slot the lead's name was given (a placeholder in the template). */
function withSlot(text: string, node: ReactNode) {
  const at = text.indexOf(SLOT)
  return at < 0 ? text : <>{text.slice(0, at)}{node}{text.slice(at + 1)}</>
}

/** A lead's name, with its title (when it has one) muted right after it (U15). */
function LeadLabel({ name, title }: { name: string; title?: string }) {
  return <>{clipForDisplay(name, 40)}{title !== undefined && <> <span data-testid="held-lead-title" className="text-text-muted">{clipForDisplay(title, 40)}</span></>}</>
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
                return withSlot(t('unattended.held.row_member', { host, lead: SLOT, member: clipForDisplay(n.member, 40), time }),
                  <LeadLabel name={lead?.name ?? n.lead} title={lead?.title} />)
              }
              const label = approvalSessionLabel(a.origin)
              const lead = leadNameBySession(rosters, a.origin.session_id, label)
              return withSlot(t('unattended.held.row', { host, session: SLOT, time }),
                <LeadLabel name={lead?.name ?? label} title={lead?.title} />)
            })()}
          </li>
        ))}
      </ul>
    </section>
  )
}
