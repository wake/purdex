// spa/src/components/team/TeamPanelRow.tsx — one person's row in the full list (moved out of TeamPanel.tsx, #2418).
import type { TeamSeatView } from './team-display'
import { TeamSeatHostBadge, TeamSeatIcon } from './TeamSeatIcon'
import { TeamSeatLight } from './TeamSeatLight'
import { SEAT_LIGHT_POSITION } from '../overlay-dot'
import { Avatar } from '../Avatar'
import { useSeat } from '../../lib/names/seat'
import { ContextRing } from './ModelIcon'
import { MODEL_LABEL } from './model-family'
import { notInApp, transitionOf } from './seat-flags'
import { ctxLeftText, ctxTip, useRosterSession, useSeatReading } from './team-readings'
import { useMemberDrag } from './useMemberDrag'
import { PauseReleaseButton } from '../PauseReleaseButton'
import { pausedLightText } from '../../lib/team/paused-text'
import { useI18nStore } from '../../stores/useI18nStore'
import { keepFocus, useReturnFocus } from './panel-focus'
import { HalfHandoffMark } from '../relay/HalfHandoffMark'
import { useFreshHandoffOfRosterSession } from '../../hooks/useFreshHandoff'
import { RecapRowButton } from '../recap/RecapButton'
import { RecapLine } from '../recap/RecapLine'
import { openRecapView, type RecapTarget } from '../../stores/useRecapViewStore'

interface RowProps {
  teamKey: string
  seat: TeamSeatView
  color: string
  isActive: boolean
  onOpen: (sessionId: string) => void
  drag?: ReturnType<ReturnType<typeof useMemberDrag>['propsFor']>
  insert?: 'before' | 'after' | null
  dragging?: boolean
}

export function PanelRow({ teamKey, seat, color, isActive, onOpen, drag, insert, dragging }: RowProps) {
  const t = useI18nStore((s) => s.t)
  const r = useSeatReading(teamKey, seat.sessionId)
  const modelText = r.model ? MODEL_LABEL[r.model] : r.modelRaw ?? '—'
  const rosterSession = useRosterSession(teamKey, seat.sessionId)
  const paused = rosterSession?.paused
  const freshHandoff = useFreshHandoffOfRosterSession(rosterSession)
  const { remember, restore } = useReturnFocus()
  const transition = transitionOf(seat)
  const away = notInApp(seat)
  const noAnswer = r.unavailable === true ? t('team.panel.context_unavailable') : undefined
  // The row's tooltip: where the seat cannot be reached, then model · effort · context left (a missing value is a dash, never 0).
  const readingTip = noAnswer ?? `${modelText} · ${r.effort ?? '—'} · ${ctxTip(r.ctx, t)}`
  const rowTip = away ? `${t('team.seat_not_in_app')}\n${readingTip}` : readingTip
  const open = () => onOpen(seat.sessionId) // a seat on a host this Mac lacks toasts the reason (openTeamSeat)
  // Who the row is (name pool v2): the persona by the code its address ends in. With none the avatar is initials on the host colour, same size, so the
  // layout does not move, and the title takes the name's place.
  const persona = useSeat(seat.address)
  const name = persona?.name ?? seat.title
  const showTitle = persona !== null && seat.title !== '' && seat.title !== persona.name
  // The recap's way in: the clock ending line 1 and the second line both open this seat's history in the panel area.
  const recapTarget: RecapTarget = { scope: teamKey, teamKey, hostId: seat.hostId, sessionId: seat.sessionId, title: seat.title, address: seat.address }
  return (
    <div
      role="button"
      tabIndex={0}
      data-testid="team-panel-row"
      data-session-id={seat.sessionId}
      data-role={seat.role}
      data-active={String(isActive)}
      data-seat-state={seat.state}
      title={rowTip}
      onMouseDown={drag ? remember : keepFocus}
      onMouseUp={drag ? restore : undefined}
      onClick={() => { restore(); open() }}
      onKeyDown={(e) => { if (e.key === 'Enter') open() }}
      {...drag}
      onDragEnd={drag ? () => { drag.onDragEnd(); restore() } : undefined}
      className={`group relative mx-1.5 px-2 py-2 rounded-md transition-colors cursor-pointer grid grid-cols-[44px_minmax(0,1fr)] gap-x-2.5 ${
        // --seat-ring is the row's own background, so the light's ring follows it (normal, hover, active)
        isActive ? 'bg-surface-active text-white [--seat-ring:var(--surface-active)]' : 'text-text-secondary hover:bg-surface-hover hover:text-text-primary [--seat-ring:var(--surface-secondary)] hover:[--seat-ring:var(--surface-hover)]'
      } ${dragging ? 'opacity-30' : ''}`}
    >
      {insert && <span className="absolute left-2 right-2 h-0.5 rounded" style={{ background: color, [insert === 'before' ? 'top' : 'bottom']: -1 }} />}
      {/* The avatar spans the row's lines; it carries no light (761: the light sits on the line-1 agent icon) */}
      <div data-testid="team-panel-avatar" aria-hidden="true" className="relative w-11 h-11 flex-shrink-0">
        <Avatar size={44} code={persona?.code ?? ''} name={name} src={persona?.avatar} />
      </div>
      <div className="min-w-0">
      {/* Line 1: agent icon with its light on the top-right corner (a seat in transition dims it) -> host badge -> host chip -> name -> title -> lead / unopened -> ring */}
      <div className="flex items-center gap-1.5">
        <span className="inline-flex items-center gap-1.5 text-text-muted">
          <span data-testid="team-panel-icon-light" className="relative inline-flex">
            <span className={transition !== null ? 'opacity-40' : ''}>
              <TeamSeatIcon hostId={seat.hostId} sessionCode={seat.sessionCode} isActive={isActive} subagents light={false} />
            </span>
            <span data-testid="team-panel-light" data-dim={String(transition !== null)} className={`absolute ${transition !== null ? 'opacity-40' : ''}`} style={SEAT_LIGHT_POSITION}>
              <TeamSeatLight hostId={seat.hostId} sessionCode={seat.sessionCode} />
            </span>
          </span>
          <span className={transition !== null ? 'opacity-40' : ''}>
            <TeamSeatHostBadge hostId={seat.hostId} sessionCode={seat.sessionCode} />
          </span>
        </span>
        {seat.remote && (
          <span data-testid="team-panel-host-chip" className="text-[9.5px] px-1 rounded bg-surface-hover text-text-secondary flex-shrink-0 max-w-[6rem] truncate">
            {seat.hostAlias !== '' ? seat.hostAlias : t('team.seat_host_unknown')}
          </span>
        )}
        {/* the name never gives way (design: the title is the first to truncate) */}
        <span data-testid="team-panel-seat-name" className={`${showTitle ? 'flex-shrink-0 whitespace-nowrap' : 'truncate min-w-0'} text-[13px] font-semibold text-text-primary`}>{name}</span>
        {showTitle && <span data-testid="team-panel-seat-title" className="truncate min-w-0 flex-1 text-[11.5px] text-text-muted">{seat.title}</span>}
        {!showTitle && <span className="flex-1" />}
        <HalfHandoffMark handoff={freshHandoff} variant="chip" />
        {transition && <span data-testid="team-panel-state" className="text-[9.5px] text-text-muted flex-shrink-0">{t(`team.seat_state.${transition}`)}</span>}
        {seat.role === 'lead' && (
          <span className="text-[9.5px] px-1 rounded border flex-shrink-0 text-text-primary" style={{ borderColor: color }}>{t('team.panel.lead')}</span>
        )}
        {seat.tabId === null && <span className="text-[9.5px] text-text-secondary flex-shrink-0">{t('team.panel.unopened')}</span>}
        {/* The right end of line 1: the context LEFT beside the ring (user 2026-10-10: the number is the remainder; a missing value is a dash),
            the model shape inside the ring; model / effort ride in the row's tooltip */}
        <span data-testid="team-panel-ring" className="flex items-center gap-1 flex-shrink-0 text-text-primary">
          <span data-testid="team-panel-ctx" className="text-[10.5px] tabular-nums text-text-muted">{ctxLeftText(noAnswer ? undefined : r.ctx)}</span>
          <ContextRing pct={noAnswer ? undefined : r.ctx} model={noAnswer ? undefined : r.model} size={16} />
        </span>
        <RecapRowButton target={recapTarget} />
      </div>
      {/* 被擋下 (UP-5b): the roster row says it is paused; the chip's 解除 goes to the host the seat lives on. */}
      {paused && (
        <div data-testid="team-panel-paused" className="mt-1 flex items-center gap-2 text-[11px] leading-[16px] text-status-warning-text">
          <span className="min-w-0 flex-1 truncate">{pausedLightText(t, paused.causes, 0, seat.role)}</span>
          {seat.hostId !== '' && <PauseReleaseButton hostId={seat.hostId} sessionId={seat.sessionId} causes={paused.causes} />}
        </div>
      )}
      {/* The row's second line: the newest recap's headline (under the paused chip). */}
      <RecapLine hostId={seat.hostId} sessionId={seat.sessionId} onOpen={() => openRecapView(recapTarget)} />
      </div>
    </div>
  )
}

