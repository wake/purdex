// spa/src/components/team/TeamPanel.tsx — the team view inside the panel area (team spec §4.4; ported from the prototype
// at f61748aa).
//
// Full: a sidebar-like list, lead on top and fixed, members draggable; each person takes two lines.
// One-line: one cell per person (bot + light, context ring around the model shape) plus the team name; the cells wrap to a
// second row when the team is big. Both take the width of the area they sit in; the area (TeamPanelArea) owns the frame.
// Row look follows the sidebar: the seat being looked at has the highlight + bright text, no side line.
// Live readings (model, effort, context) are selected per seat (team-readings.ts), not passed down from the structure.
import { useCallback } from 'react'
import { ArrowsInSimple, ArrowsOutSimple, CaretDown, CaretUp } from '@phosphor-icons/react'
import type { TeamPanelTeam, TeamSeatView } from './team-display'
import { TeamSeatHostBadge, TeamSeatIcon } from './TeamSeatIcon'
import { ContextRing, ModelIcon } from './ModelIcon'
import { MODEL_LABEL } from './model-family'
import { useSeatReading } from './team-readings'
import { useMemberDrag } from './useMemberDrag'
import { useI18nStore } from '../../stores/useI18nStore'

interface Props {
  team: TeamPanelTeam
  activeTabId: string | null
  expanded: boolean
  onSetMode: (mode: 'full' | 'line') => void
  onToggleExpanded: () => void
  onOpen: (sessionId: string) => void
  onReorder: (sessionIds: string[]) => void
}

export function TeamPanel(props: Props) {
  const { team } = props
  return (
    <div data-testid="team-panel" data-mode={team.mode} className="text-xs text-text-primary">
      {team.mode === 'full' ? <FullPanel {...props} /> : <LinePanel {...props} />}
    </div>
  )
}

/** Buttons keep the terminal's focus: a mousedown on them does not move it. */
const keepFocus = (e: React.MouseEvent) => e.preventDefault()

function ExpandButton({ expanded, onToggle }: { expanded: boolean; onToggle: () => void }) {
  const t = useI18nStore((s) => s.t)
  const label = t(expanded ? 'team.panel.restore' : 'team.panel.enlarge')
  return (
    <button
      type="button"
      data-testid="team-panel-expand"
      aria-pressed={expanded}
      onMouseDown={keepFocus}
      onClick={onToggle}
      title={label}
      aria-label={label}
      className="px-1 py-0.5 rounded text-text-secondary hover:text-text-primary hover:bg-surface-hover cursor-pointer flex-shrink-0"
    >
      {expanded ? <ArrowsInSimple size={12} /> : <ArrowsOutSimple size={12} />}
    </button>
  )
}

function NameCapsule({ team, className = '' }: { team: TeamPanelTeam; className?: string }) {
  return (
    <span
      data-testid="team-panel-name"
      className={`px-1.5 rounded text-[11px] font-semibold leading-[18px] truncate ${team.unnamed ? 'italic opacity-80' : ''} ${className}`}
      style={{ background: team.color, color: '#14141f' }}
      title={team.tooltip}
    >
      {team.name}
    </span>
  )
}

function FullPanel({ team, activeTabId, expanded, onSetMode, onToggleExpanded, onOpen, onReorder }: Props) {
  const t = useI18nStore((s) => s.t)
  const { teamKey, color, lead, members } = team
  const order = members.map((m) => m.sessionId)
  const reorder = useCallback((ids: string[]) => onReorder(ids), [onReorder])
  const { propsFor, over, draggingId } = useMemberDrag(teamKey, order, reorder, 'y')
  return (
    <>
      <div className="flex items-center gap-2 px-2.5 py-2">
        <NameCapsule team={team} />
        <span data-testid="team-panel-count" className="text-text-muted whitespace-nowrap">· {t('team.panel.members', { count: members.length })}</span>
        <span className="ml-auto flex items-center gap-0.5">
          <button
            type="button"
            data-testid="team-panel-to-line"
            onMouseDown={keepFocus}
            onClick={() => onSetMode('line')}
            className="px-1 py-0.5 rounded text-text-secondary hover:text-text-primary hover:bg-surface-hover cursor-pointer"
            title={t('team.panel.to_line')}
            aria-label={t('team.panel.to_line')}
          >
            <CaretUp size={11} />
          </button>
          <ExpandButton expanded={expanded} onToggle={onToggleExpanded} />
        </span>
      </div>
      <div className="border-t border-border-subtle py-1.5 flex flex-col gap-1">
        <PanelRow teamKey={teamKey} seat={lead} color={color} isActive={lead.tabId !== null && lead.tabId === activeTabId} onOpen={onOpen} />
        {members.map((m) => (
          <PanelRow
            key={m.sessionId}
            teamKey={teamKey}
            seat={m}
            color={color}
            isActive={m.tabId !== null && m.tabId === activeTabId}
            onOpen={onOpen}
            drag={propsFor(m.sessionId)}
            insert={over?.id === m.sessionId ? (over.after ? 'after' : 'before') : null}
            dragging={draggingId === m.sessionId}
          />
        ))}
      </div>
    </>
  )
}

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

function PanelRow({ teamKey, seat, color, isActive, onOpen, drag, insert, dragging }: RowProps) {
  const t = useI18nStore((s) => s.t)
  const r = useSeatReading(teamKey, seat.sessionId)
  const modelText = r.model ? MODEL_LABEL[r.model] : r.modelRaw ?? '—'
  const ctxText = r.ctx !== undefined ? `${r.ctx}%` : '—'
  return (
    <div
      role="button"
      tabIndex={0}
      data-testid="team-panel-row"
      data-session-id={seat.sessionId}
      data-role={seat.role}
      data-active={String(isActive)}
      onMouseDown={keepFocus}
      onClick={() => onOpen(seat.sessionId)}
      onKeyDown={(e) => { if (e.key === 'Enter') onOpen(seat.sessionId) }}
      {...drag}
      className={`group relative mx-1.5 px-2 py-2 rounded-md cursor-pointer transition-colors ${
        isActive ? 'bg-surface-active text-white' : 'text-text-secondary hover:bg-surface-hover hover:text-text-primary'
      } ${dragging ? 'opacity-30' : ''}`}
    >
      {insert && <span className="absolute left-2 right-2 h-0.5 rounded" style={{ background: color, [insert === 'before' ? 'top' : 'bottom']: -1 }} />}
      {/* Line 1: subagent dots (drawn by the icon, to its left) -> bot + light -> host chip -> title -> lead / unopened */}
      <div className="flex items-center gap-1.5 pl-1.5">
        <TeamSeatIcon hostId={seat.hostId} sessionCode={seat.sessionCode} isActive={isActive} subagents />
        <TeamSeatHostBadge hostId={seat.hostId} sessionCode={seat.sessionCode} />
        <span className="truncate min-w-0 flex-1" title={seat.title}>{seat.title}</span>
        {seat.role === 'lead' && (
          <span className="text-[9.5px] px-1 rounded border flex-shrink-0 text-text-primary" style={{ borderColor: color }}>{t('team.panel.lead')}</span>
        )}
        {seat.tabId === null && <span className="text-[9.5px] text-text-secondary flex-shrink-0">{t('team.panel.unopened')}</span>}
      </div>
      {/* Line 2: model icon + name, effort, context ring + percent; a missing value is a dash, never 0 */}
      <div className="flex items-center gap-2 pl-[26px] mt-1.5 text-[11px] leading-[16px] text-text-secondary min-w-0">
        <span className="flex items-center gap-1 min-w-0">
          <ModelIcon model={r.model} size={10} />
          <span data-testid="team-panel-model" className="truncate" title={r.modelRaw}>{modelText}</span>
        </span>
        <span data-testid="team-panel-effort" className="truncate">{r.effort ?? '—'}</span>
        <span className="ml-auto flex items-center gap-1 flex-shrink-0 text-text-primary" title={`${t('team.panel.context')} ${ctxText}`}>
          <ContextRing pct={r.ctx} model={r.model} size={16} />
          <span data-testid="team-panel-ctx" className="tabular-nums text-text-secondary">{ctxText}</span>
        </span>
      </div>
    </div>
  )
}

function Cell({ teamKey, seat, isActive, onOpen }: { teamKey: string; seat: TeamSeatView; isActive: boolean; onOpen: (sessionId: string) => void }) {
  const t = useI18nStore((s) => s.t)
  const r = useSeatReading(teamKey, seat.sessionId)
  const model = r.model ? MODEL_LABEL[r.model] : r.modelRaw ?? '?'
  return (
    <button
      type="button"
      data-testid="team-panel-cell"
      data-session-id={seat.sessionId}
      data-active={String(isActive)}
      onMouseDown={keepFocus}
      onClick={() => onOpen(seat.sessionId)}
      title={`${seat.title} · ${model} · ${t('team.panel.context')} ${r.ctx !== undefined ? `${r.ctx}%` : '—'}${seat.tabId ? '' : ` · ${t('team.panel.unopened')}`}`}
      className={`flex items-center gap-1 h-7 px-1 rounded-md cursor-pointer ${isActive ? 'bg-surface-active text-white' : 'text-text-secondary hover:bg-surface-hover hover:text-text-primary'}`}
    >
      <TeamSeatIcon hostId={seat.hostId} sessionCode={seat.sessionCode} isActive={isActive} />
      <ContextRing pct={r.ctx} model={r.model} />
    </button>
  )
}

function LinePanel({ team, activeTabId, expanded, onSetMode, onToggleExpanded, onOpen }: Props) {
  const t = useI18nStore((s) => s.t)
  const seats = [team.lead, ...team.members]
  return (
    <div className="flex items-start gap-2 px-2.5 py-2">
      <NameCapsule team={team} className="mt-[5px] max-w-[84px] flex-shrink-0" />
      <div data-testid="team-panel-cells" className="flex flex-wrap items-center gap-0.5 flex-1 min-w-0">
        {seats.map((s, i) => (
          <span key={s.sessionId} className="flex items-center">
            {i === 1 && <span className="w-px h-4 bg-border-default mx-1" />}
            <Cell teamKey={team.teamKey} seat={s} isActive={s.tabId !== null && s.tabId === activeTabId} onOpen={onOpen} />
          </span>
        ))}
      </div>
      <span className="mt-[5px] flex items-center gap-0.5 flex-shrink-0">
        <button
          type="button"
          data-testid="team-panel-to-full"
          onMouseDown={keepFocus}
          onClick={() => onSetMode('full')}
          className="px-1 py-0.5 rounded text-text-secondary hover:text-text-primary hover:bg-surface-hover cursor-pointer"
          title={t('team.panel.to_full')}
          aria-label={t('team.panel.to_full')}
        >
          <CaretDown size={11} />
        </button>
        <ExpandButton expanded={expanded} onToggle={onToggleExpanded} />
      </span>
    </div>
  )
}
