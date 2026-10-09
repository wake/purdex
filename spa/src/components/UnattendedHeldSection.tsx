// spa/src/components/UnattendedHeldSection.tsx — 「額度用完，等你核准」 in the unattended panel (relay quota spec §3.5, plan RQ-A
// Task 6): the open self_relay requests the daemon holds because the session's quota ran out. Display only here: an open
// request already raises the approval dialog by itself, and opening one specific request from the panel needs a "show
// this request" action the approval store does not have (a follow-up).
import { useI18nStore } from '../stores/useI18nStore'
import { hostLabel, hostLookOf } from '../lib/host-look'
import { approvalSessionLabel } from '../lib/team/approval-format'
import { sinceText } from '../lib/team/time-text'
import type { Approval } from '../lib/team/types'

export interface HeldRow {
  hostId: string
  a: Approval
}

export function UnattendedHeldSection({ rows }: { rows: readonly HeldRow[] }) {
  const t = useI18nStore((s) => s.t)
  if (rows.length === 0) return null
  const sorted = [...rows].sort((x, y) => y.a.created_at - x.a.created_at)
  return (
    <section data-testid="held-section" className="flex flex-col gap-1">
      <div data-testid="held-title" className="font-medium text-text-primary">{t('unattended.held.title')}</div>
      <ul className="flex flex-col gap-1">
        {sorted.map(({ hostId, a }) => (
          <li key={`${hostId}:${a.id}`} data-testid="held-row" className="text-text-primary">
            {t('unattended.held.row', { host: hostLabel(hostId, hostLookOf(hostId)), session: approvalSessionLabel(a.origin), time: sinceText(a.created_at) })}
          </li>
        ))}
      </ul>
    </section>
  )
}
