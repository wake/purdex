// spa/src/components/UnattendedQuotaSection.tsx — 「接力額度」 in the unattended panel (relay quota spec §3, plan RQ-A Task 6):
// one row per session that can hold a quota, a stepper for its auto-relay quota and, on a lead, a second one for the pool
// its members' relays draw from. The numbers belong to the chain (everything is keyed by (host, root)), so two rows of one
// root show the same numbers and a click on either writes the same thing (relay-quota.ts / relay-quota-writer.ts).
//
// The member cap and the team's folders are the team panel's now (TeamSettingsBlock, name pool v2 spec §8, U13); this section keeps the quotas (L12).
//
// Which sessions: every `quotas` row that is NOT a member in any loaded roster (all hosts: a member of a lead on another
// host is still a member). While a row's own host has no roster yet, that host shows one muted line instead of rows
// (unknown is not "not a member").
import { Fragment } from 'react'
import { useI18nStore } from '../stores/useI18nStore'
import { useTeamRosterStore } from '../stores/useTeamRosterStore'
import { hostLabel, hostLookOf } from '../lib/host-look'
import type { SessionQuota } from '../lib/team/types'
import type { TeamRoster } from '../lib/team/roster'
import { leadNameBySession, type LeadName } from '../lib/team/lead-name'
import { addressName, seatLabel, usePersonaRevision } from '../lib/names/seat'
import { QuotaStepper } from './team/Steppers'

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

/** The name a solo row shows: a persona by its name with the title after it (spec §9.3), else its title, else the name part of its address (`<alias>/<name>`). */
function plainName(r: SessionQuota): string {
  const persona = seatLabel(r.address, r.title)
  if (persona !== null) return persona
  if (r.title !== undefined && r.title !== '') return r.title
  return addressName(r.address)
}

/** What a row is called: a lead by its team's name (U15, the lead's title after it), anyone else by `plainName`. */
function lookOf(rosters: Record<string, TeamRoster[]>, r: SessionQuota): Look {
  const lead = r.is_lead ? leadNameBySession(rosters, r.session_id, r.title) : undefined
  return lead !== undefined ? { ...lead, team: true } : { name: plainName(r), team: false }
}

type Look = LeadName & { team: boolean }

/** Leads first, then by the shown name, then address; `looks` is each row's `lookOf`, taken once. */
function order(looks: Map<string, Look>) {
  return (a: SessionQuota, b: SessionQuota): number => {
    if (a.is_lead !== b.is_lead) return a.is_lead ? -1 : 1
    return (looks.get(a.session_id) as Look).name.localeCompare((looks.get(b.session_id) as Look).name) || a.address.localeCompare(b.address)
  }
}

export function UnattendedQuotaSection({ hosts, headings }: UnattendedQuotaSectionProps) {
  const t = useI18nStore((s) => s.t)
  usePersonaRevision() // the label below reads the persona store without subscribing: redraw when it loads or changes
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
          body = <div data-testid="quota-unreadable" className="text-status-warning-text">{t('unattended.quota.unreadable', { host: label(h.hostId) })}</div>
        } else if (rosters[h.hostId] === undefined) {
          body = <div data-testid="quota-loading-team" className="text-text-muted">{t('unattended.quota.loading_team')}</div>
        } else {
          const own = h.rows.filter((r) => !members.has(r.session_id))
          const looks = new Map(own.map((r) => [r.session_id, lookOf(rosters, r)] as const)) // one roster scan per row
          const rows = own.sort(order(looks))
          body = rows.length === 0
            ? <div data-testid="quota-none" className="text-text-muted">{t('unattended.quota.none')}</div>
            : (
              <ul className="grid grid-cols-[minmax(0,1fr)_auto_auto] items-center gap-x-3 gap-y-1">
                {rows.map((r) => { const look = looks.get(r.session_id) as Look; return (
                  <li key={`${r.session_id}`} data-testid="quota-row" data-session={r.session_id} className="contents">
                    <span className="block min-w-0 truncate text-text-primary" title={look.team ? `${look.name}${look.title !== undefined ? `\n${look.title}` : ''}` : `${look.name}\n${r.address}`}>
                      {look.name}{look.title !== undefined && <span data-testid="quota-lead-title" className="text-text-muted"> {look.title}</span>}
                    </span>
                    <QuotaStepper name={look.name} hostId={h.hostId} row={r} field="self_left" label={t('unattended.quota.self')} />
                    {r.is_lead
                      ? <QuotaStepper name={look.name} hostId={h.hostId} row={r} field="member_pool_left" label={t('unattended.quota.pool')} />
                      : <span data-testid="quota-pool-cell-empty" />}
                  </li>
                )})}
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
