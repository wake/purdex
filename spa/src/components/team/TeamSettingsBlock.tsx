// spa/src/components/team/TeamSettingsBlock.tsx — a team's settings on the team panel (name pool v2 spec §9.5, NA-0 design, U13): a block at the panel's bottom,
// always shown, on the lead's own team only (the panel decides). A top border, then lines at 11.5 px, each there only when the lead's host can serve it
// (team-settings.ts):
//   - 「成員上限」 stepper and 「用了 N」 (Steppers.tsx: the cap, 1–8, or 1–9 against a host with team.max_members.v2);
//   - 「自動接力」 stepper | 「成員接力池」 stepper: the lead's own auto-relay quota and its member pool — the unattended panel's steppers over the same store and
//     writer, so one value per chain shows in both places at once. The quota rows are not in the roster: showing the block reads the host's unattended view through
//     the shared reader (unattended-reader.ts: it joins a read already out) into the store; the line says 「讀取中…」 until a read has landed and the error line when
//     the last one failed;
//   - 「資料夾」 chips (the team's roots, truncated) and a dashed +: the + opens the roots editor (TeamRootsEditor) under them.
// The cap reads what it read in the unattended panel (the roster's `max_members` / `in_use`). Part of a tab-hosted panel: it keeps no state of its own that must survive
// a tab switch — what it shows lives in the stores.
import { useEffect } from 'react'
import { useI18nStore } from '../../stores/useI18nStore'
import { useHostStore } from '../../stores/useHostStore'
import { connectionKey } from '../../lib/host-connection-key'
import { useRelayQuotaStore } from '../../lib/team/relay-quota'
import { readFirstPage, watchHost } from '../../lib/team/unattended-reader'
import type { TeamRoster } from '../../lib/team/roster'
import { useTeamSettingsOffer } from './team-settings'
import { CapStepper, QuotaStepper } from './Steppers'
import { TeamFoldersLine } from './TeamFoldersLine'

export interface TeamSettingsTarget {
  /** The lead's host. */
  hostId: string
  teamId: string
  /** The name the team panel shows (the team name, else the lead's label): the toasts' name for it. */
  name: string
}

/** The block, or nothing when the host offers nothing to set (the panel then has no bottom block at all). */
export function TeamSettingsBlock({ hostId, teamId, name }: TeamSettingsTarget) {
  const t = useI18nStore((s) => s.t)
  const offer = useTeamSettingsOffer(hostId, teamId)
  const team = offer.team
  if (team === undefined || !(offer.cap || offer.quota || offer.folders)) return null
  return (
    <div data-testid="team-settings-block" className="border-t border-border-subtle px-3 py-2 flex flex-col gap-1.5 text-[11.5px]">
      {offer.cap && (
        <div data-testid="team-settings-cap" className="flex items-center gap-3">
          <CapStepper hostId={hostId} team={team} label={name} labelText={t('team.settings.cap')} />
          <span data-testid="team-settings-in-use" className="text-text-muted">{t('team.settings.in_use', { n: String(team.in_use) })}</span>
        </div>
      )}
      {offer.quota && <QuotaLine hostId={hostId} team={team} name={name} />}
      {offer.folders && <TeamFoldersLine key={`${hostId}\0${team.id}`} hostId={hostId} team={team} />} {/* its open flag and the editor's refusals are local: a switch to another team starts clean */}
    </div>
  )
}

/** 「自動接力」 | 「成員接力池」: the lead's row of the host's last read, its two steppers; 「讀取中…」 before a read landed, the error line after a failed one. */
function QuotaLine({ hostId, team, name }: { hostId: string; team: TeamRoster; name: string }) {
  const t = useI18nStore((s) => s.t)
  // Read when the block shows, and again for a re-pointed host (its old daemon's rows were forgotten with it). The read is held to its end: the block going away
  // does not cancel what fills the store, and a quick return joins it instead of asking again.
  const identity = useHostStore((s) => { const h = s.hosts[hostId]; return h ? connectionKey(h) : null })
  useEffect(() => {
    if (identity === null) return
    readFirstPage(hostId).catch(() => {}) // a failure lands in the store as the host's read
    return watchHost(hostId) // the writer's re-read after `pending_lineage` reaches this line too
  }, [hostId, identity])
  const read = useRelayQuotaStore((s) => s.reads[hostId])
  const reading = useRelayQuotaStore((s) => s.gets[hostId] !== undefined)

  let body
  if (read?.ok === true && read.quotas !== undefined && !read.quotasFailed) {
    // Shown while a re-read is out too: the numbers are the store's, the rows only say which chain is the lead's.
    const row = read.quotas.find((r) => r.session_id === team.lead.session_id)
    body = row === undefined
      ? <span data-testid="team-settings-quota-missing" className="text-text-muted">{t('team.settings.quota_missing')}</span>
      : (
        <div data-testid="team-settings-quota-steppers" className="flex flex-wrap items-center gap-x-4 gap-y-1">
          <QuotaStepper hostId={hostId} row={row} name={name} field="self_left" label={t('unattended.quota.self')} />
          {row.is_lead && <QuotaStepper hostId={hostId} row={row} name={name} field="member_pool_left" label={t('unattended.quota.pool')} />}
        </div>
      )
  } else if (read === undefined || reading) {
    body = <span data-testid="team-settings-quota-loading" className="text-text-muted">{t('team.settings.loading')}</span>
  } else {
    body = (
      <span data-testid="team-settings-quota-error" role="alert" className="text-status-warning-text">
        {read.ok ? t('team.settings.quota_unreadable') : t('team.settings.quota_failed', { code: read.code })}
      </span>
    )
  }
  return <div data-testid="team-settings-quota" className="flex items-center">{body}</div>
}
