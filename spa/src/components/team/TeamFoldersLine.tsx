// spa/src/components/team/TeamFoldersLine.tsx — 「資料夾」 in the team settings block (name pool v2 spec §9.5, NA-0 design; 761 acceptance): read-only chips — the team's
// own roots (the path cut in the middle, the whole of it in the tooltip) and, after them, the folders the other paired hosts granted this lead host (applied ones only,
// each behind its host's badge; the grant is the lead HOST's, so every team shows the same) — and a pencil that opens the roots editor under them. Adding and removing
// happen in the editor only (TeamRootsEditor: the unattended panel's, over the same store and writer). Whether it is open is this line's own — a convenience, gone with a
// tab switch; what is typed lives in the store.
import { useMemo, useState } from 'react'
import { PencilSimple } from '@phosphor-icons/react'
import { useI18nStore } from '../../stores/useI18nStore'
import { midEllipsis } from '../../lib/team/roots'
import type { TeamRoster } from '../../lib/team/roster'
import { useHostRootsStore, viewOf } from '../../lib/team/host-roots'
import { hostLabel, useHostLook } from '../../lib/host-look'
import { hasHostBadge, resolveHostBadge } from '../../lib/host-color'
import { useSidebarHostBadgeGeometry } from '../../hooks/useSidebarHostBadgeGeometry'
import { useHostRootsSync } from '../../hooks/useHostRootsSync'
import { HostBadge } from '../HostBadge'
import { TeamRootsEditor } from './TeamRootsEditor'

const CHIP = 'min-w-0 max-w-full rounded bg-surface-hover px-1.5 font-mono text-[10.5px] text-text-secondary'

/** The badge of a paired host in front of its chip; none for a host this Mac has not configured (no id) or one without a badge. */
function RemoteHostBadge({ hostId }: { hostId: string }) {
  const look = useHostLook(hostId === '' ? null : hostId)
  const { box, inset, radius, lineColor } = useSidebarHostBadgeGeometry()
  const badge = useMemo(() => resolveHostBadge(look, 'terminal'), [look])
  if (hostId === '' || !hasHostBadge(badge)) return null
  return <HostBadge colors={badge.colors} icon={badge.icon} iconWeight={badge.iconWeight} box={box} inset={inset} radius={radius} lineColor={lineColor} />
}

export function TeamFoldersLine({ hostId, team }: { hostId: string; team: TeamRoster }) {
  const t = useI18nStore((s) => s.t)
  const [open, setOpen] = useState(false)
  useHostRootsSync(hostId) // the other hosts' grants show as chips even with the editor folded
  const view = useHostRootsStore((s) => viewOf(s, hostId))
  const leadName = hostLabel(hostId, useHostLook(hostId))
  const roots = team.roots ?? []
  // applied grants only: a pending change or a refused / dropped one is not a folder a member may start in (yet)
  const remote = view.load === 'ok' ? view.hosts.flatMap((h) => h.roots.map((path) => ({ key: `${h.hostId || h.alias}\0${path}`, hostId: h.hostId, alias: h.alias, path }))) : []
  return (
    <div data-testid="team-settings-folders" className="flex flex-col gap-1">
      <div className="flex items-center gap-1.5 min-w-0">
        <span className="flex-shrink-0 text-text-secondary">{t('team.settings.folders')}</span>
        <div className="flex min-w-0 flex-1 flex-wrap items-center gap-1">
          {roots.map((root) => (
            <span key={root} data-testid="team-folder-chip" data-remote="false" title={root} className={`${CHIP} truncate`}>{midEllipsis(root, 32)}</span>
          ))}
          {remote.map((r) => (
            <span
              key={r.key} data-testid="team-folder-chip" data-remote="true" title={t('team.settings.folders_remote_tip', { alias: r.alias, path: r.path, lead: leadName })}
              className={`${CHIP} inline-flex items-center gap-1`}
            >
              <RemoteHostBadge hostId={r.hostId} />
              <span className="min-w-0 truncate">{midEllipsis(r.path, 32)}</span>
            </span>
          ))}
          <button
            type="button" data-testid="team-folder-edit" aria-expanded={open} aria-label={t('team.settings.folders_edit')} title={t('team.settings.folders_edit')}
            onClick={() => setOpen((v) => !v)}
            className="inline-flex h-4 w-5 flex-shrink-0 cursor-pointer items-center justify-center rounded text-text-secondary hover:bg-surface-hover hover:text-text-primary"
          ><PencilSimple size={11} /></button>
        </div>
      </div>
      {open && <TeamRootsEditor hostId={hostId} team={team} />}
    </div>
  )
}
