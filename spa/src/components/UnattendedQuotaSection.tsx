// spa/src/components/UnattendedQuotaSection.tsx — 「接力額度」 in the unattended panel (relay quota spec §3, plan RQ-A Task 6):
// one row per session that can hold a quota, a stepper for its auto-relay quota and, on a lead, a second one for the pool
// its members' relays draw from. The numbers belong to the chain (everything is keyed by (host, root)), so two rows of one
// root show the same numbers and a click on either writes the same thing (relay-quota.ts / relay-quota-writer.ts).
//
// A lead's row may carry a third group, 「上限 − N +」, the team's member cap (`PUT /api/team/max-members`): shown only
// when the host lists `team.max_members.v1` and the roster has the team the row leads (its `max_members` / `in_use`).
// − stops at max(1, members in use), + at 8; one request per team at a time (the buttons are disabled meanwhile).
//
// Which sessions: every `quotas` row that is NOT a member in any loaded roster (all hosts: a member of a lead on another
// host is still a member). While a row's own host has no roster yet, that host shows one muted line instead of rows
// (unknown is not "not a member").
import { Fragment } from 'react'
import { useI18nStore } from '../stores/useI18nStore'
import { useTeamRosterStore } from '../stores/useTeamRosterStore'
import { useUnattendedStore } from '../stores/useUnattendedStore'
import { hostLabel, hostLookOf } from '../lib/host-look'
import { shownValue, useRelayQuotaStore } from '../lib/team/relay-quota'
import { setQuota } from '../lib/team/relay-quota-writer'
import { setMaxMembers, teamKey, useMaxMembersStore } from '../lib/team/max-members'
import { MAX_MEMBERS_MAX, MAX_MEMBERS_MIN, type RelayQuotaField, type SessionQuota } from '../lib/team/types'
import type { TeamRoster } from '../lib/team/roster'

/** One host's share of the section. */
export interface QuotaHostData {
  hostId: string
  /** The rows of a successful read (`[]` = no session). Absent with `failed`. */
  rows?: readonly SessionQuota[]
  /** The daemon's `quotas` was null or malformed. */
  failed?: boolean
}

export interface UnattendedQuotaSectionProps {
  hosts: readonly QuotaHostData[]
  /** Name each host above its rows (more than one host has a section). */
  headings: boolean
}

const MAX = 99

/** The name a row shows: its title, else the name part of its address (`<alias>/<name>`). */
function nameOf(r: SessionQuota): string {
  if (r.title !== undefined && r.title !== '') return r.title
  const slash = r.address.indexOf('/')
  return slash >= 0 ? r.address.slice(slash + 1) : r.address
}

function order(a: SessionQuota, b: SessionQuota): number {
  if (a.is_lead !== b.is_lead) return a.is_lead ? -1 : 1
  return nameOf(a).localeCompare(nameOf(b)) || a.address.localeCompare(b.address)
}

function Stepper({ hostId, row, field, label }: { hostId: string; row: SessionQuota; field: RelayQuotaField; label: string }) {
  const t = useI18nStore((s) => s.t)
  const value = useRelayQuotaStore((s) => shownValue(s, hostId, row.root_session_id, field, row[field]))
  const write = (next: number) => setQuota({ hostId, sessionId: row.session_id, root: row.root_session_id, label: nameOf(row) }, field, next)
  const btn = 'w-5 h-5 inline-flex items-center justify-center rounded text-text-secondary hover:text-text-primary hover:bg-surface-hover cursor-pointer disabled:opacity-30 disabled:pointer-events-none'
  return (
    <span className="inline-flex items-center gap-1" data-testid="quota-stepper" data-field={field}>
      <span className="text-text-muted">{label}</span>
      <button type="button" aria-label={t('unattended.quota.minus')} disabled={value <= 0} onClick={() => write(value - 1)} className={btn}>−</button>
      <span data-testid="quota-value" className="min-w-[2ch] text-center tabular-nums text-text-primary">{value}</span>
      <button type="button" aria-label={t('unattended.quota.plus')} disabled={value >= MAX} onClick={() => write(value + 1)} className={btn}>+</button>
    </span>
  )
}

/** The team a lead row leads, when the cap can be shown: the host lists the capability and the roster carries the cap. */
function capTeam(rosters: Record<string, TeamRoster[]>, supported: boolean, hostId: string, row: SessionQuota): TeamRoster | undefined {
  if (!supported || !row.is_lead) return undefined
  const t = rosters[hostId]?.find((x) => x.lead.session_id === row.session_id)
  return t !== undefined && t.max_members !== undefined && t.in_use !== undefined ? t : undefined
}

function CapStepper({ hostId, team, label }: { hostId: string; team: TeamRoster; label: string }) {
  const t = useI18nStore((s) => s.t)
  const busy = useMaxMembersStore((s) => s.inflight[teamKey(hostId, team.id)] === true)
  const value = team.max_members as number
  const inUse = team.in_use as number
  const write = (next: number) => { void setMaxMembers({ hostId, teamId: team.id, label }, next) }
  const btn = 'w-5 h-5 inline-flex items-center justify-center rounded text-text-secondary hover:text-text-primary hover:bg-surface-hover cursor-pointer disabled:opacity-30 disabled:pointer-events-none'
  return (
    <span className="inline-flex items-center gap-1" data-testid="cap-stepper" title={t('unattended.cap.in_use', { n: String(inUse) })}>
      <span className="text-text-muted">{t('unattended.cap.label')}</span>
      <button type="button" aria-label={t('unattended.cap.minus')} disabled={busy || value <= Math.max(MAX_MEMBERS_MIN, inUse)} onClick={() => write(value - 1)} className={btn}>−</button>
      <span data-testid="cap-value" className="min-w-[2ch] text-center tabular-nums text-text-primary">{value}</span>
      <button type="button" aria-label={t('unattended.cap.plus')} disabled={busy || value >= MAX_MEMBERS_MAX} onClick={() => write(value + 1)} className={btn}>+</button>
    </span>
  )
}

export function UnattendedQuotaSection({ hosts, headings }: UnattendedQuotaSectionProps) {
  const t = useI18nStore((s) => s.t)
  const rosters = useTeamRosterStore((s) => s.byHost)
  const support = useUnattendedStore((s) => s.byHost)
  if (hosts.length === 0) return null

  const members = new Set<string>()
  for (const teams of Object.values(rosters)) for (const tm of teams) for (const m of tm.members) members.add(m.session_id)
  const label = (hostId: string) => hostLabel(hostId, hostLookOf(hostId))

  return (
    <section data-testid="quota-section" className="flex flex-col gap-1">
      <div data-testid="quota-title" className="font-medium text-text-primary">{t('unattended.quota.title')}</div>
      <div data-testid="quota-explain" className="text-text-muted">{t('unattended.quota.explain')}</div>
      {hosts.map((h) => {
        let body
        if (h.failed || h.rows === undefined) {
          body = <div data-testid="quota-unreadable" className="text-status-warning">{t('unattended.quota.unreadable', { host: label(h.hostId) })}</div>
        } else if (rosters[h.hostId] === undefined) {
          body = <div data-testid="quota-loading-team" className="text-text-muted">{t('unattended.quota.loading_team')}</div>
        } else {
          const rows = h.rows.filter((r) => !members.has(r.session_id)).sort(order)
          const capOn = support[h.hostId]?.maxMembersSupport === 'yes'
          const caps = new Map(rows.map((r) => [r.session_id, capTeam(rosters, capOn, h.hostId, r)] as const))
          const withCap = [...caps.values()].some((x) => x !== undefined) // a fourth column only where some row has one
          body = rows.length === 0
            ? <div data-testid="quota-none" className="text-text-muted">{t('unattended.quota.none')}</div>
            : (
              <ul className={`grid ${withCap ? 'grid-cols-[minmax(0,1fr)_auto_auto_auto]' : 'grid-cols-[minmax(0,1fr)_auto_auto]'} items-center gap-x-3 gap-y-1`}>
                {rows.map((r) => (
                  <li key={`${r.session_id}`} data-testid="quota-row" data-session={r.session_id} className="contents">
                    <span className="block min-w-0 truncate text-text-primary" title={r.address}>{nameOf(r)}</span>
                    <Stepper hostId={h.hostId} row={r} field="self_left" label={t('unattended.quota.self')} />
                    {r.is_lead
                      ? <Stepper hostId={h.hostId} row={r} field="member_pool_left" label={t('unattended.quota.pool')} />
                      : <span data-testid="quota-pool-cell-empty" />}
                    {withCap && (() => {
                      const team = caps.get(r.session_id)
                      return team !== undefined
                        ? <CapStepper hostId={h.hostId} team={team} label={nameOf(r)} />
                        : <span data-testid="cap-cell-empty" />
                    })()}
                  </li>
                ))}
              </ul>
            )
        }
        return (
          <Fragment key={h.hostId}>
            {headings && <div data-testid="quota-host-heading" className="mt-1 text-text-secondary">{label(h.hostId)}</div>}
            {body}
          </Fragment>
        )
      })}
    </section>
  )
}
