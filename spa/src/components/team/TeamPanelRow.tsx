// spa/src/components/team/TeamPanelRow.tsx — one person's row in the full list (moved out of TeamPanel.tsx, #2418).
import { useState } from 'react'
import { Notebook } from '@phosphor-icons/react'
import type { TeamSeatView } from './team-display'
import { TeamSeatHostBadge, TeamSeatIcon } from './TeamSeatIcon'
import { ContextRing } from './ModelIcon'
import { MODEL_LABEL } from './model-family'
import { notInApp, transitionOf } from './seat-flags'
import { ctxLeftText, ctxTip, useRosterSession, useSeatReading } from './team-readings'
import { firstSentence, useSeatWorkbook } from './seat-workbook'
import { useMemberDrag } from './useMemberDrag'
import { PauseReleaseButton } from '../PauseReleaseButton'
import { pausedLightText } from '../../lib/team/paused-text'
import { useIsJournalHost } from '../../lib/journal/use-journal-host'
import type { JournalMember } from '../../lib/journal/types'
import { memberLastLine, memberTaskLine } from './member-lines'
import { useI18nStore } from '../../stores/useI18nStore'
import { useTeamUiStore } from '../../stores/useTeamUiStore'
import { keepFocus, useReturnFocus } from './panel-focus'
import { HalfHandoffMark } from '../relay/HalfHandoffMark'
import { useFreshHandoffOfRosterSession } from '../../hooks/useFreshHandoff'

interface RowProps {
  /** The journal stack's team card: never offers the workbook drill, whatever host the seat is on. */
  noDrill?: boolean
  /** This seat's lines from the leader's journal (JV-2); undefined = no lines (no capability, or not a member). */
  member?: JournalMember
  teamKey: string
  seat: TeamSeatView
  color: string
  isActive: boolean
  onOpen: (sessionId: string) => void
  drag?: ReturnType<ReturnType<typeof useMemberDrag>['propsFor']>
  insert?: 'before' | 'after' | null
  dragging?: boolean
}

export function PanelRow({ noDrill, member, teamKey, seat, color, isActive, onOpen, drag, insert, dragging }: RowProps) {
  const t = useI18nStore((s) => s.t)
  const r = useSeatReading(teamKey, seat.sessionId)
  const modelText = r.model ? MODEL_LABEL[r.model] : r.modelRaw ?? '—'
  const rosterSession = useRosterSession(teamKey, seat.sessionId)
  const paused = rosterSession?.paused
  const freshHandoff = useFreshHandoffOfRosterSession(rosterSession)
  const wb0 = useSeatWorkbook(seat.hostId, seat.sessionId)
  // On a journal.v1 host the workbook view is off (J-2b): no drill button, no 目前狀況 line.
  const journalHost = useIsJournalHost(seat.hostId)
  const wb = journalHost || noDrill ? { ...wb0, has: false, conv: undefined } : wb0
  const status = wb.conv?.status.trim() ?? ''
  const task = firstSentence(status)
  const [now] = useState(() => Date.now()) // the age of the last report, as of when the row was drawn
  const { remember, restore } = useReturnFocus()
  const transition = transitionOf(seat)
  const away = notInApp(seat)
  const noAnswer = r.unavailable === true ? t('team.panel.context_unavailable') : undefined
  // The row's tooltip: where the seat cannot be reached, then model · effort · context left (a missing value is a dash, never 0).
  const readingTip = noAnswer ?? `${modelText} · ${r.effort ?? '—'} · ${ctxTip(r.ctx, t)}`
  const rowTip = away ? `${t('team.seat_not_in_app')}\n${readingTip}` : readingTip
  const open = () => onOpen(seat.sessionId) // a seat on a host this Mac lacks toasts the reason (openTeamSeat)
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
      className={`group relative mx-1.5 px-2 py-2 rounded-md transition-colors cursor-pointer ${
        isActive ? 'bg-surface-active text-white' : 'text-text-secondary hover:bg-surface-hover hover:text-text-primary'
      } ${dragging ? 'opacity-30' : ''}`}
    >
      {insert && <span className="absolute left-2 right-2 h-0.5 rounded" style={{ background: color, [insert === 'before' ? 'top' : 'bottom']: -1 }} />}
      {/* Line 1: subagent dots (drawn by the icon, to its left) -> bot + light -> host chip -> title -> lead / unopened */}
      <div className="flex items-center gap-1.5 pl-1.5">
        <span data-testid="team-panel-light" data-dim={String(transition !== null)} className={`inline-flex items-center gap-1.5 ${transition !== null ? 'opacity-40' : ''}`}>
          <TeamSeatIcon hostId={seat.hostId} sessionCode={seat.sessionCode} isActive={isActive} subagents />
          <TeamSeatHostBadge hostId={seat.hostId} sessionCode={seat.sessionCode} />
        </span>
        {seat.remote && (
          <span data-testid="team-panel-host-chip" className="text-[9.5px] px-1 rounded bg-surface-hover text-text-secondary flex-shrink-0 max-w-[6rem] truncate">
            {seat.hostAlias !== '' ? seat.hostAlias : t('team.seat_host_unknown')}
          </span>
        )}
        <span className="truncate min-w-0 flex-1">{seat.title}</span>
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
        {/* The workbook button follows the ring (only with a workbook); it drills in, the row itself still opens the seat */}
        {wb.has && (
          <button
            type="button"
            data-testid="team-panel-workbook"
            onMouseDown={(e) => { e.stopPropagation(); e.preventDefault() }}
            onClick={(e) => { e.stopPropagation(); useTeamUiStore.getState().setTeamDrill(teamKey, { hostId: seat.hostId, sessionId: seat.sessionId }) }}
            onKeyDown={(e) => e.stopPropagation()}
            title={t('team.panel.workbook')}
            aria-label={t('team.panel.workbook')}
            className="p-0.5 rounded text-text-secondary hover:text-text-primary hover:bg-surface-hover cursor-pointer flex-shrink-0"
          >
            <Notebook size={14} />
          </button>
        )}
      </div>
      {/* 被擋下 (UP-5b): the roster row says it is paused; the chip's 解除 goes to the host the seat lives on. */}
      {paused && (
        <div data-testid="team-panel-paused" className="pl-[26px] mt-1 flex items-center gap-2 text-[11px] leading-[16px] text-status-warning-text">
          <span className="min-w-0 flex-1 truncate">{pausedLightText(t, paused.causes, 0, seat.role)}</span>
          {seat.hostId !== '' && <PauseReleaseButton hostId={seat.hostId} sessionId={seat.sessionId} causes={paused.causes} />}
        </div>
      )}
      {/* Lines 2 and 3 from the leader's journal (JV-2, U3): the member's current task and state; its last report to the leader.
          A part the daemon left out is left out; with neither the row stays as it was. */}
      {member?.task && (
        <div data-testid="team-panel-member-task" title={member.task.title} className="pl-[26px] mt-1 text-[11px] leading-[16px] text-text-secondary truncate">
          {memberTaskLine(t, member.task)}
        </div>
      )}
      {member?.last && (
        <div data-testid="team-panel-member-last" title={member.last.text} className="pl-[26px] mt-0.5 text-[11px] leading-[16px] text-text-muted truncate">
          {memberLastLine(t, member.last, now)}
        </div>
      )}
      {/* Line 2 (older hosts): the task (only with a workbook that has a status); without one the row stays one line */}
      {task !== '' && (
        <div data-testid="team-panel-task" aria-label={t('team.panel.task', { task })} title={status} className="pl-[26px] mt-1 text-[11px] leading-[16px] text-text-secondary truncate">
          {task}
        </div>
      )}
    </div>
  )
}
