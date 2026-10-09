// spa/src/components/team/TeamPanel.tsx — the team view inside the panel area (team spec §4.4; ported from the prototype
// at f61748aa).
//
// Full: a sidebar-like list, lead on top and fixed, members draggable; each person takes two lines.
// One-line: one cell per person (bot + light, context ring around the model shape) plus the team name; the cells wrap to a
// second row when the team is big. Both take the width of the area they sit in; the area (TeamPanelArea) owns the frame.
// Row look follows the sidebar: the seat being looked at has the highlight + bright text, no side line.
// Live readings (model, effort, context) are selected per seat (team-readings.ts), not passed down from the structure.
import { useCallback, useEffect, useLayoutEffect, useRef, useState } from 'react'
import { ArrowsInSimple, ArrowsOutSimple, CaretDown, CaretUp } from '@phosphor-icons/react'
import type { TeamPanelTeam, TeamSeatView } from './team-display'
import { TeamSeatHostBadge, TeamSeatIcon } from './TeamSeatIcon'
import { ContextRing, ModelIcon } from './ModelIcon'
import { MODEL_LABEL } from './model-family'
import { notInApp, transitionOf } from './seat-flags'
import { useSeatReading } from './team-readings'
import { useMemberDrag } from './useMemberDrag'
import { TeamEditPopover } from './TeamEditPopover'
import { useHeaderGestures, type HeaderHandlers } from './useHeaderGestures'
import { useI18nStore } from '../../stores/useI18nStore'
import { useTeamRosterStore } from '../../stores/useTeamRosterStore'
import { useUISettingsStore } from '../../stores/useUISettingsStore'
import { useUnattendedStore } from '../../stores/useUnattendedStore'
import { CELL_GAP, CELL_H, CELL_ICON, CELL_ICON_PULL, CELL_INNER_GAP, CELL_PX, CELL_RING, CAPSULE_MAX_W, HEADER_GAP, HEADER_H, HEADER_PX, capacityFromWidths, firstRowCapacity } from './panel-layout'

interface Props {
  team: TeamPanelTeam
  activeTabId: string | null
  expanded: boolean
  /** The area's width while it floats (draft included); undefined when enlarged (everything fits in the header row). */
  width?: number
  onSetMode: (mode: 'full' | 'line') => void
  onToggleExpanded: () => void
  onOpen: (sessionId: string) => void
  onReorder: (sessionIds: string[]) => void
}

export function TeamPanel(props: Props) {
  const { team } = props
  const [hostId, teamId = ''] = team.teamKey.split('\u0000')
  // The edit needs the lead's host to list `team.edit.v1` and the roster to hold the team (its values are the form's start).
  const editable = useUnattendedStore((s) => s.byHost[hostId]?.editSupport === 'yes')
  const roster = useTeamRosterStore((s) => s.byHost[hostId]?.find((r) => r.id === teamId))
  const canEdit = editable && roster !== undefined
  const { rootRef, hdr, editOpen, close, anchor } = useHeaderGestures({ teamKey: team.teamKey, mode: team.mode, onSetMode: props.onSetMode, canEdit })
  return (
    <div ref={rootRef} data-testid="team-panel" data-mode={team.mode} className="text-xs text-text-primary">
      {team.mode === 'full' ? <FullPanel {...props} hdr={hdr} /> : <LinePanel {...props} hdr={hdr} />}
      {editOpen && canEdit && (
        <TeamEditPopover
          target={{ hostId, teamId, name: roster.team_name, label: roster.team_label, color: roster.team_color ?? null }}
          anchor={anchor}
          onClose={close}
        />
      )}
    </div>
  )
}

/** The header row both modes share: one fixed height, capsule | middle | buttons in the same places. */
const HEADER_CLASS = 'flex items-center cursor-pointer select-none'
const headerStyle = { height: HEADER_H, paddingInline: HEADER_PX, columnGap: HEADER_GAP } as const

/** Buttons keep the terminal's focus: a mousedown on them does not move it. */
const keepFocus = (e: React.MouseEvent) => e.preventDefault()

/** A draggable row cannot preventDefault on mousedown (the browser would never start the HTML5 drag), so it lets the focus
 *  move, remembers where it was, and hands it back when the press ends: mouseup / click / dragend on the row, or a mouseup
 *  anywhere (the pointer left the row without reaching the drag threshold), the window losing focus, or the row unmounting.
 *  It only hands back while the focus is still on the row (or nowhere): a focusable the person moved to is left alone. */
function useReturnFocus() {
  const [api] = useState(() => {
    let held: { prev: HTMLElement; row: HTMLElement } | null = null
    let listening = false
    function restore() {
      if (listening) {
        listening = false
        document.removeEventListener('mouseup', restore)
        window.removeEventListener('blur', restore)
      }
      const h = held
      held = null
      if (!h || !h.prev.isConnected) return
      const a = document.activeElement
      if (a === h.row || a === document.body || a === null) h.prev.focus()
    }
    function remember(e: React.MouseEvent) {
      const a = document.activeElement
      const row = e.currentTarget as HTMLElement
      held = a instanceof HTMLElement && a !== row ? { prev: a, row } : null
      if (held && !listening) {
        listening = true
        document.addEventListener('mouseup', restore)
        window.addEventListener('blur', restore)
      }
    }
    return { remember, restore }
  })
  useEffect(() => api.restore, [api])
  return api
}

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

function NameCapsule({ team, className = '', style }: { team: TeamPanelTeam; className?: string; style?: React.CSSProperties }) {
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

function FullPanel({ team, activeTabId, expanded, onSetMode, onToggleExpanded, onOpen, onReorder, hdr }: Props & { hdr: HeaderHandlers }) {
  const t = useI18nStore((s) => s.t)
  const { teamKey, color, lead, members } = team
  const order = members.map((m) => m.sessionId)
  const reorder = useCallback((ids: string[]) => onReorder(ids), [onReorder])
  const { propsFor, over, draggingId } = useMemberDrag(teamKey, order, reorder, 'y')
  return (
    <>
      <div data-testid="team-panel-header" className={HEADER_CLASS} style={headerStyle} {...hdr}>
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
  const { remember, restore } = useReturnFocus()
  const transition = transitionOf(seat)
  const away = notInApp(seat)
  const noAnswer = r.unavailable === true ? t('team.panel.context_unavailable') : undefined
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
      title={away ? t('team.seat_not_in_app') : undefined}
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
        <span className="truncate min-w-0 flex-1" title={away ? `${seat.title} — ${t('team.seat_not_in_app')}` : seat.title}>{seat.title}</span>
        {transition && <span data-testid="team-panel-state" className="text-[9.5px] text-text-muted flex-shrink-0">{t(`team.seat_state.${transition}`)}</span>}
        {seat.role === 'lead' && (
          <span className="text-[9.5px] px-1 rounded border flex-shrink-0 text-text-primary" style={{ borderColor: color }}>{t('team.panel.lead')}</span>
        )}
        {seat.tabId === null && <span className="text-[9.5px] text-text-secondary flex-shrink-0">{t('team.panel.unopened')}</span>}
      </div>
      {/* Line 2: model icon + name, effort, context ring + percent; a missing value is a dash, never 0 */}
      <div className="flex items-center gap-2 pl-[26px] mt-1.5 text-[11px] leading-[16px] text-text-secondary min-w-0">
        <span className="flex items-center gap-1 min-w-0">
          <ModelIcon model={r.model} size={10} />
          <span data-testid="team-panel-model" className="truncate" title={noAnswer ?? r.modelRaw}>{noAnswer ? '—' : modelText}</span>
        </span>
        <span data-testid="team-panel-effort" className="truncate">{r.effort ?? '—'}</span>
        <span className="ml-auto flex items-center gap-1 flex-shrink-0 text-text-primary" title={noAnswer ?? `${t('team.panel.context')} ${ctxText}`}>
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
        <TeamSeatIcon hostId={seat.hostId} sessionCode={seat.sessionCode} isActive={isActive} size={CELL_ICON} compact />
      </span>
      {seat.remote && <TeamSeatHostBadge hostId={seat.hostId} sessionCode={seat.sessionCode} />}
      <ContextRing pct={r.ctx} model={r.model} size={CELL_RING} />
    </button>
  )
}

function LinePanel({ team, activeTabId, expanded, width, onSetMode, onToggleExpanded, onOpen, hdr }: Props & { hdr: HeaderHandlers }) {
  const t = useI18nStore((s) => s.t)
  const seats = [team.lead, ...team.members]
  // The header row holds as many cells as it really has room for; the rest wrap into a region UNDER it, so the first row
  // (capsule, cells, buttons) never changes height. The room is MEASURED (the cells container's width over a rendered cell's
  // width: a status-light style draws a wider icon, an enlarged panel is wider than any stored width). Where nothing can be
  // measured (no layout) it falls back to the constants: the stored width's capacity, or everything when enlarged.
  const box = useRef<HTMLDivElement>(null)
  const [measured, setMeasured] = useState<number | null>(null)
  // Every seat's cell is always rendered (the first row, then the wrapped region under it), so each one's REAL width can be
  // read in seat order and the capacity is the longest prefix that fits (capacityFromWidths). It depends only on those
  // widths and the box width, never on how many cells are currently in the first row, so it cannot oscillate (N -> N-1 -> N).
  useUISettingsStore((s) => s.tabIndicatorStyle) // a light style change widens the cells: re-render so the effect below re-measures
  const moreBox = useRef<HTMLDivElement>(null)
  useLayoutEffect(() => {
    const el = box.current
    if (!el) return
    const cells = () => [...el.querySelectorAll<HTMLElement>('[data-testid="team-panel-cell"]'), ...(moreBox.current?.querySelectorAll<HTMLElement>('[data-testid="team-panel-cell"]') ?? [])]
    const measure = () => {
      const avail = el.clientWidth
      const widths = cells().map((c) => c.offsetWidth)
      setMeasured(avail > 0 && widths.length > 0 && widths.every((w) => w > 0) ? capacityFromWidths(widths, avail) : null)
    }
    measure()
    if (typeof ResizeObserver === 'undefined') return
    // A cell can change width by itself (e.g. its host badge's size setting), with neither a LinePanel re-render nor a
    // container resize: observe every cell too. The effect re-runs each render, so a changed cell set is re-bound. measure()
    // only changes state when the capacity really differs (setState bails out on an equal value), so this cannot oscillate.
    const ro = new ResizeObserver(measure)
    ro.observe(el)
    cells().forEach((c) => ro.observe(c))
    return () => ro.disconnect()
  })
  const cap = measured ?? (width === undefined ? seats.length : firstRowCapacity(width))
  const first = seats.slice(0, cap)
  const more = seats.slice(cap)
  const cell = (s: TeamSeatView) => <Cell key={s.sessionId} teamKey={team.teamKey} seat={s} isActive={s.tabId !== null && s.tabId === activeTabId} onOpen={onOpen} />
  return (
    <>
    <div data-testid="team-panel-header" className={HEADER_CLASS} style={headerStyle} {...hdr}>
      <NameCapsule team={team} className="flex-shrink-0" style={{ maxWidth: CAPSULE_MAX_W }} />
      <div ref={box} data-testid="team-panel-cells" className="flex items-center flex-1 min-w-0" style={{ columnGap: CELL_GAP }}>
        {first.map((s, i) => (
          <span key={s.sessionId} className="flex items-center">
            {i === 1 && <span className="w-px h-4 bg-border-default" style={{ marginInline: 2 }} />}
            {cell(s)}
          </span>
        ))}
      </div>
      <span className="flex items-center gap-0.5 flex-shrink-0">
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
    {more.length > 0 && (
      <div ref={moreBox} data-testid="team-panel-more" className="flex flex-wrap items-center border-t border-border-subtle py-1" style={{ paddingInline: HEADER_PX, gap: CELL_GAP }}>
        {more.map(cell)}
      </div>
    )}
    </>
  )
}
