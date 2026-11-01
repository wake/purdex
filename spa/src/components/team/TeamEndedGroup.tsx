// spa/src/components/team/TeamEndedGroup.tsx — the full list's 「已結束」 group (moved out of TeamPanel.tsx, #2418; restored for the recap history, plan T14b).
import { useState } from 'react'
import { CaretRight } from '@phosphor-icons/react'
import { useI18nStore } from '../../stores/useI18nStore'
import { useDialogueStore } from '../../stores/useDialogueStore'
import { useTeamUiStore } from '../../stores/useTeamUiStore'
import { openRecapView, type RecapTarget } from '../../stores/useRecapViewStore'
import { keepFocus } from './panel-focus'
import { seatLabel, usePersonaRevision } from '../../lib/names/seat'

/** Seats that left the team: a collapsed 「已結束 (N)」 group at the bottom of the full list (absent with none); a click on one
 *  opens its recap history, which the daemon keeps after the session is gone (nothing new can be made for it). */
export function EndedGroup({ teamKey }: { teamKey: string }) {
  const t = useI18nStore((s) => s.t)
  usePersonaRevision() // the names read the persona store without subscribing: redraw when it loads or changes
  const all = useTeamUiStore((s) => s.endedSeats[teamKey])
  const support = useDialogueStore((s) => s.support)
  const [open, setOpen] = useState(false)
  // Only a seat on a host that lists `dialogue.v1` has a recap history to open.
  const ended = (all ?? []).filter((e) => support[e.hostId] === true)
  if (ended.length === 0) return null
  const label = t('team.panel.ended', { count: ended.length })
  const target = (e: (typeof ended)[number]): RecapTarget => ({ scope: teamKey, teamKey, hostId: e.hostId, sessionId: e.sessionId, title: e.title, address: e.address ?? '' })
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
          onClick={() => openRecapView(target(e))}
          onKeyDown={(ev) => { if (ev.key === 'Enter' || ev.key === ' ') openRecapView(target(e)) }}
          className="mx-1.5 pl-6 pr-2 py-1.5 rounded-md truncate text-text-muted hover:bg-surface-hover hover:text-text-secondary cursor-pointer"
        >
          {(e.address ? seatLabel(e.address, e.title) : null) ?? e.title}
        </div>
      ))}
    </div>
  )
}
