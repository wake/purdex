// spa/src/components/team/TeamSettingsPanel.tsx — a team's settings (journal v5 spec §3.9, user decision U14): opened from the
// gear on the team panel's title (TeamPanel.tsx, on the lead's tab), a FloatingPanel 「團隊設定・{team name}」 whose sections
// are each there only when the lead's host can serve it (team-settings.ts):
//   - 「成員上限」: the cap stepper, moved here from the unattended panel with its rules and texts (Steppers.tsx);
//   - 「資料夾」: the team's roots and the other hosts' (TeamRootsEditor, HostRootsSection), moved too.
// They read what they read in the unattended panel (the roster's `max_members` / `in_use` / `roots`, the roots store).
//
// Not tab-hosted: opened by a click of the gear and gone when closed; what it shows lives in the stores, and the typed
// root is kept by the roots store.
import type { RefObject } from 'react'
import { FloatingPanel } from '../FloatingPanel'
import { useI18nStore } from '../../stores/useI18nStore'
import { useTeamSettingsOffer } from './team-settings'
import { CapStepper } from './Steppers'
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

/** The sections (the panel's body), a line between those that are there — the unattended panel's look. */
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
    </div>
  )
}

function SectionTitle({ text }: { text: string }) {
  return <div className="font-medium text-text-primary">{text}</div>
}
