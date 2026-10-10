// spa/src/components/team/TeamCell.tsx — the pieces the pane's one-line header and the title-bar strip share: the team name
// capsule and one person's cell. One copy, so the two places can not drift apart.
//
// The cell is laid out like the sidebar tab row's "bot -> host icon" run (InlineTab): the same icon (TeamSeatIcon, light and
// subagent dots included — the dots float into the left padding, so they add no width), 6px, and then a square the size of the
// sidebar host box. That square is the usage ring here, not a host icon: the host is shown by the ring's model symbol, painted
// in the host's main colour, and by the tooltip. The box is a bead's (h-6, pl-1.5, pr-[3px]), so the dots at the left and the
// light at the top right fall in the cell's own padding and never reach a neighbour.
import { TeamSeatIcon } from './TeamSeatIcon'
import { useSeatHostMain } from './useSeatHostMain'
import { ContextRing } from './ModelIcon'
import { MODEL_LABEL } from './model-family'
import { notInApp, transitionOf } from './seat-flags'
import { ctxTip, useSeatReading } from './team-readings'
import type { TeamPanelTeam, TeamSeatView } from './team-display'
import { useI18nStore } from '../../stores/useI18nStore'
import { useUISettingsStore } from '../../stores/useUISettingsStore'
import { keepFocus } from '../../lib/keep-focus'

export function NameCapsule({ team, className = '', style }: { team: TeamPanelTeam; className?: string; style?: React.CSSProperties }) {
  return (
    <span
      data-testid="team-panel-name"
      className={`px-1.5 rounded text-[11px] font-semibold leading-[18px] truncate ${team.unnamed ? 'italic opacity-80' : ''} ${className}`}
      style={{ background: team.color, color: '#14141f', ...style }}
      title={team.tooltip}
    >
      {team.name}
    </span>
  )
}

export function TeamCell({ teamKey, seat, isActive, onOpen }: { teamKey: string; seat: TeamSeatView; isActive: boolean; onOpen: (sessionId: string) => void }) {
  const t = useI18nStore((s) => s.t)
  const r = useSeatReading(teamKey, seat.sessionId)
  const ringBox = useUISettingsStore((s) => s.hostBadgeSidebarBox)
  const hostMain = useSeatHostMain(seat.hostId, seat.sessionCode)
  const model = r.unavailable ? '—' : r.model ? MODEL_LABEL[r.model] : r.modelRaw ?? '?'
  const transition = transitionOf(seat)
  const away = notInApp(seat)
  // A remote seat's tooltip names its host, then what is going on with it (a transition, an unreachable host, a host this Mac lacks).
  const notes = [
    seat.remote ? (seat.hostAlias !== '' ? seat.hostAlias : t('team.seat_host_unknown')) : null,
    transition ? t(`team.seat_state.${transition}`) : null,
    r.unavailable ? t('team.panel.context_unavailable') : null,
    away ? t('team.seat_not_in_app') : null,
  ].filter((x): x is string => x !== null)
  return (
    <button
      type="button"
      data-testid="team-panel-cell"
      data-session-id={seat.sessionId}
      data-active={String(isActive)}
      data-seat-state={seat.state}
      onMouseDown={keepFocus}
      onClick={() => onOpen(seat.sessionId)}
      title={`${seat.title} · ${model} · ${ctxTip(r.ctx, t)}${seat.tabId ? '' : ` · ${t('team.panel.unopened')}`}${notes.map((n) => ` · ${n}`).join('')}`}
      className={`group relative flex items-center gap-1.5 h-6 pl-1.5 pr-[3px] rounded-md cursor-pointer ${isActive ? 'bg-surface-active text-white' : 'text-text-secondary hover:bg-surface-hover hover:text-text-primary'}`}
    >
      <span data-testid="team-panel-light" data-dim={String(transition !== null)} className={`inline-flex ${transition !== null ? 'opacity-40' : ''}`}>
        <TeamSeatIcon hostId={seat.hostId} sessionCode={seat.sessionCode} isActive={isActive} subagents />
      </span>
      <ContextRing pct={r.ctx} model={r.model} size={ringBox} symbolColor={hostMain} />
    </button>
  )
}

/** The 1px divider after the lead. Place it as a direct child of the cells row (gap = CELL_GAP): the gap is its spacing. */
export function CellSep() {
  return <span data-testid="cell-sep" className="w-px h-4 flex-shrink-0 bg-border-default" />
}
