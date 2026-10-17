// spa/src/components/team/TeamSettingsPanel.tsx — a team's settings (journal v5 spec §3.9, user decision U14): opened from the
// gear on the team panel's title (TeamPanel.tsx, on the lead's tab), a FloatingPanel 「團隊設定・{team name}」 with three
// sections, each there only when the lead's host can serve it (team-settings.ts):
//   - 「成員上限」: the cap stepper, moved here from the unattended panel with its rules and texts (Steppers.tsx);
//   - 「資料夾」: the team's roots and the other hosts' (TeamRootsEditor, HostRootsSection), moved too;
//   - 「接力額度」: the lead's own auto-relay quota and its member pool — the unattended panel's steppers over the same store
//     and writer, so one value per chain shows in both places at once. The quota rows are not in the roster: opening reads
//     the host's unattended view through the shared reader (unattended-reader.ts: it joins a read already out) into the
//     store; the section says 「讀取中…」 until a read has landed and the error line when the last one failed.
// The cap and the folders read what they read today (the roster's `max_members` / `in_use` / `roots`, the roots store).
//
// Not tab-hosted: opened by a click of the gear and gone when closed; what it shows lives in the stores, and the typed
// root is kept by the roots store.
import { useEffect, type RefObject } from 'react'
import { FloatingPanel } from '../FloatingPanel'
import { useI18nStore } from '../../stores/useI18nStore'
import { useHostStore } from '../../stores/useHostStore'
import { connectionKey } from '../../lib/host-connection-key'
import { useRelayQuotaStore } from '../../lib/team/relay-quota'
import { readFirstPage, watchHost } from '../../lib/team/unattended-reader'
import type { TeamRoster } from '../../lib/team/roster'
import { useTeamSettingsOffer } from './team-settings'
import { CapStepper, QuotaStepper } from './Steppers'
import { TeamRootsEditor } from './TeamRootsEditor'

const PANEL_WIDTH = 400

export interface TeamSettingsTarget {
  /** The lead's host. */
  hostId: string
  teamId: string
  /** The name the team panel shows (the team name, else the lead's label): the title, and the toasts' name for it. */
  name: string
}

export function TeamSettingsPanel({ target, anchorRef, onClose }: { target: TeamSettingsTarget; anchorRef: RefObject<HTMLElement | null>; onClose: () => void }) {
  const t = useI18nStore((s) => s.t)
  return (
    <FloatingPanel title={t('team.settings.title', { name: target.name })} anchorRef={anchorRef} onClose={onClose} width={PANEL_WIDTH} placement="below" testId="team-settings-panel">
      <TeamSettingsBody {...target} />
    </FloatingPanel>
  )
}

/** The three sections (the panel's body), a line between those that are there — the unattended panel's look. */
export function TeamSettingsBody({ hostId, teamId, name }: TeamSettingsTarget) {
  const t = useI18nStore((s) => s.t)
  const offer = useTeamSettingsOffer(hostId, teamId)
  const team = offer.team
  if (team === undefined) return <div data-testid="team-settings-ended" className="text-xs text-text-muted">{t('unattended.roots.ended')}</div>
  return (
    <div data-testid="team-settings-body" className="flex flex-col text-xs divide-y divide-border-subtle [&>*]:py-2 [&>*:first-child]:pt-0 [&>*:last-child]:pb-0">
      {offer.cap && (
        <section data-testid="team-settings-cap" className="flex flex-col gap-1.5">
          <SectionTitle text={t('team.settings.cap')} />
          <div className="flex items-center gap-3">
            <CapStepper hostId={hostId} team={team} label={name} />
            <span data-testid="team-settings-in-use" className="text-text-muted">{t('unattended.cap.in_use', { n: String(team.in_use) })}</span>
          </div>
        </section>
      )}
      {offer.folders && (
        <section data-testid="team-settings-folders" className="flex flex-col gap-1.5">
          <SectionTitle text={t('team.settings.folders')} />
          <TeamRootsEditor hostId={hostId} team={team} />
        </section>
      )}
      {offer.quota && <QuotaSection hostId={hostId} team={team} name={name} />}
    </div>
  )
}

function SectionTitle({ text }: { text: string }) {
  return <div className="font-medium text-text-primary">{text}</div>
}

/** 「接力額度」: the lead's row of the host's last read, its two steppers; 「讀取中…」 before a read landed, the error line after a failed one. */
function QuotaSection({ hostId, team, name }: { hostId: string; team: TeamRoster; name: string }) {
  const t = useI18nStore((s) => s.t)
  // Read on open, and again for a re-pointed host (its old daemon's rows were forgotten with it). The read is held to its
  // end: closing does not cancel what fills the store, and a quick reopen joins it instead of asking again.
  const identity = useHostStore((s) => { const h = s.hosts[hostId]; return h ? connectionKey(h) : null })
  useEffect(() => {
    if (identity === null) return
    readFirstPage(hostId).catch(() => {}) // a failure lands in the store as the host's read
    return watchHost(hostId) // the writer's re-read after `pending_lineage` reaches this section too
  }, [hostId, identity])
  const read = useRelayQuotaStore((s) => s.reads[hostId])
  const reading = useRelayQuotaStore((s) => s.gets[hostId] !== undefined)

  let body
  if (read?.ok === true && read.quotas !== undefined && !read.quotasFailed) {
    // Shown while a re-read is out too: the numbers are the store's, the rows only say which chain is the lead's.
    const row = read.quotas.find((r) => r.session_id === team.lead.session_id)
    body = row === undefined
      ? <div data-testid="team-settings-quota-missing" className="text-text-muted">{t('team.settings.quota_missing')}</div>
      : (
        <div data-testid="team-settings-quota-steppers" className="flex flex-wrap items-center gap-x-4 gap-y-1">
          <QuotaStepper hostId={hostId} row={row} name={name} field="self_left" label={t('unattended.quota.self')} />
          {row.is_lead && <QuotaStepper hostId={hostId} row={row} name={name} field="member_pool_left" label={t('unattended.quota.pool')} />}
        </div>
      )
  } else if (read === undefined || reading) {
    body = <div data-testid="team-settings-quota-loading" className="text-text-muted">{t('team.settings.loading')}</div>
  } else {
    body = (
      <div data-testid="team-settings-quota-error" role="alert" className="text-status-warning-text">
        {read.ok ? t('team.settings.quota_unreadable') : t('team.settings.quota_failed', { code: read.code })}
      </div>
    )
  }
  return (
    <section data-testid="team-settings-quota" className="flex flex-col gap-1.5">
      <SectionTitle text={t('team.settings.quota')} />
      <div className="text-text-muted">{t('unattended.quota.explain')}</div>
      {body}
    </section>
  )
}
