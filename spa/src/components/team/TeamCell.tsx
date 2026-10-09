// spa/src/components/team/TeamCell.tsx — the pieces the pane's one-line header and the title-bar strip share: the team name
// capsule and one person's cell (subagent slot + bot + light, context ring around the model shape). One copy, so the two
// places can not drift apart.
import { TeamSeatHostBadge, TeamSeatIcon } from './TeamSeatIcon'
import { ContextRing } from './ModelIcon'
import { MODEL_LABEL } from './model-family'
import { notInApp, transitionOf } from './seat-flags'
import { useSeatReading } from './team-readings'
import type { TeamPanelTeam, TeamSeatView } from './team-display'
import { useI18nStore } from '../../stores/useI18nStore'
import { keepFocus } from '../../lib/keep-focus'
import { SEP_MARGIN, CELL_H, CELL_ICON, CELL_ICON_PULL, CELL_INNER_GAP, CELL_PX, CELL_RING } from './panel-layout'

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
      title={`${seat.title} · ${model} · ${t('team.panel.context')} ${r.ctx !== undefined ? `${r.ctx}%` : '—'}${seat.tabId ? '' : ` · ${t('team.panel.unopened')}`}${notes.map((n) => ` · ${n}`).join('')}`}
      style={{ height: CELL_H, paddingInline: CELL_PX, columnGap: CELL_INNER_GAP }}
      className={`flex items-center rounded-md cursor-pointer ${isActive ? 'bg-surface-active text-white' : 'text-text-secondary hover:bg-surface-hover hover:text-text-primary'}`}
    >
      <span data-testid="team-panel-light" data-dim={String(transition !== null)} className={`inline-flex ${transition !== null ? 'opacity-40' : ''}`} style={{ marginLeft: CELL_ICON_PULL }}>
        <TeamSeatIcon hostId={seat.hostId} sessionCode={seat.sessionCode} isActive={isActive} size={CELL_ICON} compact subagents subagentSlot />
      </span>
      {seat.remote && <TeamSeatHostBadge hostId={seat.hostId} sessionCode={seat.sessionCode} />}
      <ContextRing pct={r.ctx} model={r.model} size={CELL_RING} />
    </button>
  )
}

/** The 1px divider after the lead. */
export function CellSep() {
  return <span className="w-px h-4 bg-border-default" style={{ marginInline: SEP_MARGIN }} />
}
