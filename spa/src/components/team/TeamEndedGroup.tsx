// spa/src/components/team/TeamEndedGroup.tsx — the full list's 「已結束」 group (moved out of TeamPanel.tsx, #2418).
import { useState } from 'react'
import { CaretRight } from '@phosphor-icons/react'
import { useI18nStore } from '../../stores/useI18nStore'
import { useWorkbookStore } from '../../stores/useWorkbookStore'
import { useTeamUiStore } from '../../stores/useTeamUiStore'
import { keepFocus } from './panel-focus'

/** Seats that left the team: a collapsed 「已結束 (N)」 group at the bottom of the full list (absent with none); a click on one
 *  drills into its workbook, which the daemon keeps after the session is gone. */
export function EndedGroup({ teamKey }: { teamKey: string }) {
  const t = useI18nStore((s) => s.t)
  const all = useTeamUiStore((s) => s.endedSeats[teamKey])
  const support = useWorkbookStore((s) => s.support)
  const [open, setOpen] = useState(false)
  // Only a seat on a host that lists `workbook.v1` has a workbook to open (openWorkbook does nothing elsewhere).
  const ended = (all ?? []).filter((e) => support[e.hostId]?.v1 === true)
  if (ended.length === 0) return null
  const label = t('team.panel.ended', { count: ended.length })
  return (
    <div data-testid="team-panel-ended" className="border-t border-border-subtle py-1">
      <button
        type="button"
        aria-expanded={open}
        onMouseDown={keepFocus}
        onClick={() => setOpen((v) => !v)}
        className="w-full flex items-center gap-1 px-3.5 py-1 text-text-muted hover:text-text-secondary cursor-pointer"
      >
        <CaretRight size={10} className={open ? 'rotate-90' : ''} />
        <span>{label}</span>
      </button>
      {open && ended.map((e) => (
        <div
          key={`${e.hostId}\u0000${e.sessionId}`}
          role="button"
          tabIndex={0}
          data-testid="team-panel-ended-row"
          title={t('team.panel.ended_hint')}
          onMouseDown={keepFocus}
          onClick={() => useTeamUiStore.getState().setTeamDrill(teamKey, { hostId: e.hostId, sessionId: e.sessionId })}
          onKeyDown={(ev) => { if (ev.key === 'Enter' || ev.key === ' ') useTeamUiStore.getState().setTeamDrill(teamKey, { hostId: e.hostId, sessionId: e.sessionId }) }}
          className="mx-1.5 pl-6 pr-2 py-1.5 rounded-md truncate text-text-muted hover:bg-surface-hover hover:text-text-secondary cursor-pointer"
        >
          {e.title}
        </div>
      ))}
    </div>
  )
}
