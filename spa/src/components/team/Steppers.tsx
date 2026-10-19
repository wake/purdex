// spa/src/components/team/Steppers.tsx — the − N + steppers of the unattended panel and the team settings (journal v5 §3.9).
//
// QuotaStepper: one relay quota field of a chain (`self_left`, or a lead's `member_pool_left`). It shows the quota store's
// value for (host, root, field) and writes through the one writer (relay-quota.ts / relay-quota-writer.ts), so every
// stepper of one root — in either panel — shows the same number and a click on any of them writes the same thing.
//
// CapStepper: the team's member cap (`PUT /api/team/max-members`, moved from the unattended panel to the team settings).
// − stops at max(1, members in use), + at 8 (9 against a host with team.max_members.v2); one request per team at a time (the buttons are disabled meanwhile); no
// optimistic number: it shows the roster's `max_members`.
import { useI18nStore } from '../../stores/useI18nStore'
import { useHostStore } from '../../stores/useHostStore'
import { useHostLookResolver } from '../../lib/host-look'
import { shownValue, useRelayQuotaStore } from '../../lib/team/relay-quota'
import { setQuota } from '../../lib/team/relay-quota-writer'
import { setMaxMembers, teamKey, useMaxMembersStore } from '../../lib/team/max-members'
import { maxMembersMax, MAX_MEMBERS_MIN, type RelayQuotaField, type SessionQuota } from '../../lib/team/types'
import { useUnattendedStore } from '../../stores/useUnattendedStore'
import type { TeamRoster } from '../../lib/team/roster'
import { NumberField } from './NumberField'

const QUOTA_MAX = 99

const STEP_BTN = 'w-5 h-5 inline-flex items-center justify-center rounded text-text-secondary hover:text-text-primary hover:bg-surface-hover cursor-pointer disabled:opacity-30 disabled:pointer-events-none'

/** A − / + button. When it is off for a rule (not merely busy), the reason shows on hover: the title sits on a wrapper, since a disabled button gets no hover. */
function StepButton({ sign, label, disabled, reason, onClick }: { sign: string; label: string; disabled: boolean; reason?: string; onClick: () => void }) {
  return (
    <span title={disabled ? reason : undefined} data-testid="step-reason" data-reason={disabled && reason ? reason : undefined} className="inline-flex">
      <button type="button" aria-label={label} title={disabled ? reason : undefined} disabled={disabled} onClick={onClick} className={STEP_BTN}>{sign}</button>
    </span>
  )
}

/**
 * `name` is the row's name (the writer's toasts, and the buttons' and the box's accessible names, with the host's after it when there is more than one host); `label` the text before the number. The two buttons read "Increase <label> quota (<name>)": two steppers of
 * one screen (the auto relay and the member pool, and every row or team beside them) must not both be called just 「增加」 to a screen reader or to voice control.
 */
export function QuotaStepper({ hostId, row, name, field, label }: { hostId: string; row: SessionQuota; name: string; field: RelayQuotaField; label: string }) {
  const t = useI18nStore((s) => s.t)
  // With more than one host the same row name can be on two of them, and the host heading above the rows is no part of a button's name: the subject says the host too.
  const lookOf = useHostLookResolver()
  const manyHosts = useHostStore((s) => s.hostOrder.length > 1)
  const subject = manyHosts ? `${name} · ${lookOf(hostId).name || hostId}` : name
  const value = useRelayQuotaStore((s) => shownValue(s, hostId, row.root_session_id, field, row[field]))
  const write = (next: number) => setQuota({ hostId, sessionId: row.session_id, root: row.root_session_id, label: name }, field, next)
  return (
    <span className="inline-flex items-center gap-1" data-testid="quota-stepper" data-field={field}>
      <span className="text-text-muted">{label}</span>
      <StepButton sign="−" label={t('unattended.quota.step_minus', { field: label, name: subject })} disabled={value <= 0} reason={t('unattended.quota.at_zero')} onClick={() => write(value - 1)} />
      <NumberField
        testId="quota-value" ariaLabel={t('unattended.quota.value_name', { field: label, name: subject })} value={value}
        validate={(n) => (n > QUOTA_MAX ? t('unattended.field.range', { min: '0', max: String(QUOTA_MAX) }) : null)}
        onCommit={write}
      />
      <StepButton sign="+" label={t('unattended.quota.step_plus', { field: label, name: subject })} disabled={value >= QUOTA_MAX} reason={t('unattended.quota.at_max', { n: String(QUOTA_MAX) })} onClick={() => write(value + 1)} />
    </span>
  )
}

/** `team` must carry its cap and its usage (`hasCap`, team-settings.ts); `label` names the team in the toasts; `labelText` is the text before the number (default 「上限」). */
export function CapStepper({ hostId, team, label, labelText }: { hostId: string; team: TeamRoster; label: string; labelText?: string }) {
  const t = useI18nStore((s) => s.t)
  const busy = useMaxMembersStore((s) => s.inflight[teamKey(hostId, team.id)] !== undefined)
  const max = maxMembersMax(useUnattendedStore((s) => s.byHost[hostId]?.maxMembersV2Support === 'yes')) // 9 against team.max_members.v2, else 8
  const text = labelText ?? t('unattended.cap.label')
  const value = team.max_members as number
  const inUse = team.in_use as number
  const write = (next: number) => { void setMaxMembers({ hostId, teamId: team.id, label }, next) }
  // why − is off: the daemon will not take the cap below the members in use (409 max_below_in_use), nor below 1
  const floor = Math.max(MAX_MEMBERS_MIN, inUse)
  const minusReason = value <= inUse ? t('unattended.cap.minus_in_use', { n: String(inUse) }) : t('unattended.cap.minus_min', { n: String(MAX_MEMBERS_MIN) })
  return (
    <span className="inline-flex items-center gap-1" data-testid="cap-stepper" title={t('unattended.cap.in_use', { n: String(inUse) })}>
      <span className="text-text-muted">{text}</span>
      <StepButton sign="−" label={t('unattended.cap.minus')} disabled={busy || value <= floor} reason={busy ? undefined : minusReason} onClick={() => write(value - 1)} />
      <NumberField
        testId="cap-value" ariaLabel={text} value={value} disabled={busy}
        validate={(n) => (n < MAX_MEMBERS_MIN || n > max
          ? t('unattended.field.range', { min: String(MAX_MEMBERS_MIN), max: String(max) })
          : n < inUse ? t('unattended.cap.below', { n: String(inUse) }) : null)}
        onCommit={write}
      />
      <StepButton sign="+" label={t('unattended.cap.plus')} disabled={busy || value >= max} reason={busy ? undefined : t('unattended.cap.plus_max', { n: String(max) })} onClick={() => write(value + 1)} />
    </span>
  )
}
