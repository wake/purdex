// spa/src/components/team/TeamPanel.tsx — the team view inside the panel area (team spec §4.4; ported from the prototype
// at f61748aa).
//
// Full: a sidebar-like list, lead on top and fixed, members draggable; each person takes one line (a second line only when the seat has a workbook: its task).
// One-line: one cell per person (bot + light, context ring around the model shape) plus the team name; the cells wrap to a
// second row when the team is big. Both take the width of the area they sit in; the area (TeamPanelArea) owns the frame.
// Row look follows the sidebar: the seat being looked at has the highlight + bright text, no side line.
// Live readings (model, effort, context) are selected per seat (team-readings.ts), not passed down from the structure.
import { Fragment, useCallback, useRef } from 'react'
import { ArrowsInSimple, ArrowsOutSimple, ArrowLineUp } from '@phosphor-icons/react'
import type { TeamPanelTeam, TeamSeatView } from './team-display'
import { CellSep, NameCapsule, TeamCell } from './TeamCell'
import { useMemberDrag } from './useMemberDrag'
import { useCellCapacity } from './useCellCapacity'
import { TeamEditPopover } from './TeamEditPopover'
import { useHeaderGestures, type HeaderHandlers } from './useHeaderGestures'
import { keepFocus } from './panel-focus'
import { EndedGroup } from './TeamEndedGroup'
import { PanelRow } from './TeamPanelRow'
import { useI18nStore } from '../../stores/useI18nStore'
import { useTeamUiStore, type PanelMode } from '../../stores/useTeamUiStore'
import { TeamSeatWorkbookView } from './TeamSeatWorkbookView'
import { useTeamRosterStore } from '../../stores/useTeamRosterStore'
import { useUnattendedStore } from '../../stores/useUnattendedStore'
import { useUISettingsStore } from '../../stores/useUISettingsStore'
import { CELL_GAP, CAPSULE_MAX_W, HEADER_GAP, HEADER_H, HEADER_PX, firstRowCapacity } from './panel-layout'

interface Props {
  team: TeamPanelTeam
  activeTabId: string | null
  /** The area's width while it floats (draft included); undefined when enlarged (everything fits in the header row). */
  width?: number
  /** Move the area: ⌃ -> titlebar, the header click line <-> full, the enlarge control full <-> max. */
  onSetMode: (mode: PanelMode) => void
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
  // A drilled-in seat takes the place of the list (the drill is the store's, so it outlives this component).
  const drill = useTeamUiStore((s) => s.teamDrill[team.teamKey])
  const endedTitle = useTeamUiStore((s) => drill ? s.endedSeats[team.teamKey]?.find((e) => e.hostId === drill.hostId && e.sessionId === drill.sessionId)?.title : undefined)
  const drillTitle = drill
    ? [team.lead, ...team.members].find((m) => m.sessionId === drill.sessionId && m.hostId === drill.hostId)?.title ?? endedTitle ?? drill.sessionId
    : ''
  return (
    <div ref={rootRef} data-testid="team-panel" data-mode={team.mode} className={`text-xs text-text-primary${drill ? ' flex flex-col flex-1 min-h-0' : ''}`}>
      {drill ? (
        <TeamSeatWorkbookView teamKey={team.teamKey} hostId={drill.hostId} sessionId={drill.sessionId} title={drillTitle} />
      ) : team.mode === 'line' ? <LinePanel {...props} hdr={hdr} /> : <FullPanel {...props} hdr={hdr} />}
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

/**
 * The header's one move-to-title-bar control (round 5): the same icon and the same action in line, full and max. Going
 * line <-> full is the header click's job, not this button's.
 */
export function ToTitleBarButton({ onClick }: { onClick: () => void }) {
  const t = useI18nStore((s) => s.t)
  const label = t('team.panel.to_titlebar')
  return (
    <button
      type="button"
      data-testid="team-panel-to-titlebar"
      onMouseDown={keepFocus}
      onClick={onClick}
      className="px-1 py-0.5 rounded text-text-secondary hover:text-text-primary hover:bg-surface-hover cursor-pointer"
      title={label}
      aria-label={label}
    >
      <ArrowLineUp size={11} />
    </button>
  )
}

export function ExpandButton({ expanded, onToggle }: { expanded: boolean; onToggle: () => void }) {
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

function FullPanel({ team, activeTabId, onSetMode, onOpen, onReorder, hdr }: Props & { hdr: HeaderHandlers }) {
  const t = useI18nStore((s) => s.t)
  const expanded = team.mode === 'max'
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
          <ToTitleBarButton onClick={() => onSetMode('titlebar')} />
          <ExpandButton expanded={expanded} onToggle={() => onSetMode(expanded ? 'full' : 'max')} />
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
      <EndedGroup teamKey={teamKey} />
    </>
  )
}

function LinePanel({ team, activeTabId, width, onSetMode, onOpen, hdr }: Props & { hdr: HeaderHandlers }) {
  const seats = [team.lead, ...team.members]
  // The header row holds as many cells as it really has room for; the rest wrap into a region UNDER it, so the first row
  // (capsule, cells, buttons) never changes height. The room is measured (useCellCapacity); where nothing can be measured
  // (no layout) it falls back to the constants: the stored width's capacity, or everything when enlarged.
  const box = useRef<HTMLDivElement>(null)
  const moreBox = useRef<HTMLDivElement>(null)
  const measured = useCellCapacity(box, { extra: moreBox })
  const lightStyle = useUISettingsStore((s) => s.tabIndicatorStyle)
  const ringBox = useUISettingsStore((s) => s.hostBadgeSidebarBox)
  const cap = measured ?? (width === undefined ? seats.length : firstRowCapacity(width, lightStyle, ringBox))
  const first = seats.slice(0, cap)
  const more = seats.slice(cap)
  const cell = (s: TeamSeatView) => <TeamCell key={s.sessionId} teamKey={team.teamKey} seat={s} isActive={s.tabId !== null && s.tabId === activeTabId} onOpen={onOpen} />
  return (
    <>
    <div data-testid="team-panel-header" className={HEADER_CLASS} style={headerStyle} {...hdr}>
      <NameCapsule team={team} className="flex-shrink-0" style={{ maxWidth: CAPSULE_MAX_W }} />
      <div ref={box} data-testid="team-panel-cells" className="flex items-center flex-1 min-w-0" style={{ columnGap: CELL_GAP }}>
        {first.map((s, i) => (
          <Fragment key={s.sessionId}>
            {i === 1 && <CellSep />}
            <span className="flex items-center">{cell(s)}</span>
          </Fragment>
        ))}
      </div>
      <span className="flex items-center gap-0.5 flex-shrink-0">
        <ToTitleBarButton onClick={() => onSetMode('titlebar')} />
        <ExpandButton expanded={false} onToggle={() => onSetMode('max')} />
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
