// spa/src/components/UnattendedQuotaSection.tsx — 「接力額度」 in the unattended panel (relay quota spec §3, plan RQ-A Task 6):
// one row per session that can hold a quota, a stepper for its auto-relay quota and, on a lead, a second one for the pool
// its members' relays draw from. The numbers belong to the chain (everything is keyed by (host, root)), so two rows of one
// root show the same numbers and a click on either writes the same thing (relay-quota.ts / relay-quota-writer.ts).
//
// Which sessions: every `quotas` row that is NOT a member in any loaded roster (all hosts: a member of a lead on another
// host is still a member). While a row's own host has no roster yet, that host shows one muted line instead of rows
// (unknown is not "not a member").
import { Fragment } from 'react'
import { useI18nStore } from '../stores/useI18nStore'
import { useTeamRosterStore } from '../stores/useTeamRosterStore'
import { hostLabel, hostLookOf } from '../lib/host-look'
import { shownValue, useRelayQuotaStore } from '../lib/team/relay-quota'
import { setQuota } from '../lib/team/relay-quota-writer'
import type { RelayQuotaField, SessionQuota } from '../lib/team/types'

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
      <span data-testid="quota-value" className="min-w-[1.5ch] text-center tabular-nums text-text-primary">{value}</span>
      <button type="button" aria-label={t('unattended.quota.plus')} disabled={value >= MAX} onClick={() => write(value + 1)} className={btn}>+</button>
    </span>
  )
}

export function UnattendedQuotaSection({ hosts, headings }: UnattendedQuotaSectionProps) {
  const t = useI18nStore((s) => s.t)
  const rosters = useTeamRosterStore((s) => s.byHost)
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
          body = rows.length === 0
            ? <div data-testid="quota-none" className="text-text-muted">{t('unattended.quota.none')}</div>
            : (
              <ul className="flex flex-col gap-1">
                {rows.map((r) => (
                  <li key={`${r.session_id}`} data-testid="quota-row" data-session={r.session_id} className="flex flex-wrap items-center justify-between gap-x-3 gap-y-1">
                    <span className="truncate text-text-primary" title={r.address}>{nameOf(r)}</span>
                    <span className="inline-flex items-center gap-3">
                      <Stepper hostId={h.hostId} row={r} field="self_left" label={t('unattended.quota.self')} />
                      {r.is_lead && <Stepper hostId={h.hostId} row={r} field="member_pool_left" label={t('unattended.quota.pool')} />}
                    </span>
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
