// spa/src/components/team/TeamFoldersLine.tsx — 「資料夾」 in the team settings block (name pool v2 spec §9.5, NA-0 design): the team's roots as chips (the path cut in the
// middle, the whole of it in the tooltip) and a dashed + that opens the roots editor under them. The editor is the unattended panel's (TeamRootsEditor: add, remove, the
// other hosts' grants), over the same store and writer. Whether it is open is this line's own — a convenience, gone with a tab switch; what is typed lives in the store.
import { useState } from 'react'
import { Plus } from '@phosphor-icons/react'
import { useI18nStore } from '../../stores/useI18nStore'
import { midEllipsis } from '../../lib/team/roots'
import type { TeamRoster } from '../../lib/team/roster'
import { TeamRootsEditor } from './TeamRootsEditor'

export function TeamFoldersLine({ hostId, team }: { hostId: string; team: TeamRoster }) {
  const t = useI18nStore((s) => s.t)
  const [open, setOpen] = useState(false)
  const roots = team.roots ?? []
  return (
    <div data-testid="team-settings-folders" className="flex flex-col gap-1">
      <div className="flex items-center gap-1.5 min-w-0">
        <span className="flex-shrink-0 text-text-muted">{t('team.settings.folders')}</span>
        <div className="flex min-w-0 flex-1 flex-wrap items-center gap-1">
          {roots.map((root) => (
            <span key={root} data-testid="team-folder-chip" title={root} className="min-w-0 max-w-full truncate rounded bg-surface-hover px-1.5 font-mono text-[10.5px] text-text-secondary">{midEllipsis(root, 32)}</span>
          ))}
          <button
            type="button" data-testid="team-folder-add" aria-expanded={open} aria-label={t('team.settings.folders_add')} title={t('team.settings.folders_add')}
            onClick={() => setOpen((v) => !v)}
            className="inline-flex h-4 w-6 flex-shrink-0 cursor-pointer items-center justify-center rounded border border-dashed border-border-default text-text-muted hover:text-text-primary"
          ><Plus size={10} /></button>
        </div>
      </div>
      {open && <TeamRootsEditor hostId={hostId} team={team} />}
    </div>
  )
}
