// spa/src/components/team/TeamTitleBar.tsx — the panel area's title-bar state (team spec §4.4 Round 3): the strip in the
// right of the window title bar, next to the buttons (the one-line content: team name + cells, no wrap, overflow -> 「+N」) and the Notebook
// button that moves the area between the title bar and the pane. TitleBar.tsx only places them.
//
// Only the strip's content is no-drag: the strip box itself ignores the pointer, so the empty part of the title bar still
// drags the window. The state lives in `useTeamUiStore` (per team), so a tab switch or a reload comes back the same.
import { Fragment, useLayoutEffect, useRef } from 'react'
import { Notebook } from '@phosphor-icons/react'
import { useTeamDisplay, type TeamPanelTeam } from './team-display'
import { CellSep, NameCapsule, TeamCell } from './TeamCell'
import { useCellCapacity } from './useCellCapacity'
import { useOwnStatusLine } from './own-workbook'
import type { OwnWorkbookTarget } from './panel-view'
import { CAPSULE_MAX_W, CELL_GAP, HEADER_GAP, PLUS_CHIP_W } from './panel-layout'
import { BUTTON, IDLE, PRESSED } from '../title-bar-styles'
import { useTabStore } from '../../stores/useTabStore'
import { useTeamUiStore } from '../../stores/useTeamUiStore'
import { useI18nStore } from '../../stores/useI18nStore'
import { keepFocus } from '../../lib/keep-focus'

const NO_DRAG = { WebkitAppRegion: 'no-drag' } as React.CSSProperties

/**
 * The strip: shown by TitleBar at the right of the bar, right before the button group, while the team's area is in the
 * title bar (the window title stays centred). `room` is the box's width from `titleBarLayout`; null = not measured, take what is left.
 */
export function TeamTitleStrip({ team, room = null, onContentWidth }: { team: TeamPanelTeam; room?: number | null; onContentWidth?: (w: number) => void }) {
  const display = useTeamDisplay()
  const activeTabId = useTabStore((s) => s.activeTabId)
  const box = useRef<HTMLDivElement>(null)
  const inner = useRef<HTMLDivElement>(null)
  // The title overlay's padding follows the strip's real content width (not the box's, which is the room it may use).
  useLayoutEffect(() => {
    const el = inner.current
    if (!el || !onContentWidth) return
    const report = () => onContentWidth(el.offsetWidth)
    report()
    if (typeof ResizeObserver === 'undefined') return () => onContentWidth(0)
    const ro = new ResizeObserver(report)
    ro.observe(el)
    return () => { ro.disconnect(); onContentWidth(0) }
  }, [onContentWidth])
  const seats = [team.lead, ...team.members]
  // The room is measured from every seat's own cell width: the strip's width, less the team name's worst case, and 「+N」
  // when someone does not fit. The seats that do not fit are still rendered, in a hidden row, so their widths count; where
  // nothing can be measured (no layout) everyone is drawn.
  const cap = useCellCapacity(box, {
    base: CAPSULE_MAX_W + HEADER_GAP,
    reserve: PLUS_CHIP_W + HEADER_GAP,
    min: 0,
  }) ?? seats.length
  const shown = seats.slice(0, cap)
  const rest = seats.slice(shown.length)
  const hidden = rest.length
  const cell = (s: TeamPanelTeam['lead']) => (
    <TeamCell
      key={s.sessionId}
      teamKey={team.teamKey}
      seat={s}
      isActive={s.tabId !== null && s.tabId === activeTabId}
      onOpen={(sessionId) => display?.onOpenSeat(team.teamKey, sessionId)}
    />
  )
  const back = () => useTeamUiStore.getState().toggleTitleBar(team.teamKey)
  return (
    <div
      ref={box}
      data-testid="team-title-strip"
      className={`flex items-center justify-end relative min-w-0 overflow-hidden pointer-events-none ${room === null ? 'flex-1' : 'shrink-0'}`}
      style={{ ...(room === null ? null : { width: room }), marginRight: HEADER_GAP }}
    >
      <div ref={inner} data-testid="team-strip-inner" className="flex items-center min-w-0 max-w-full" style={{ columnGap: HEADER_GAP }}>
        <TeamStripButton testId="team-strip-name" onClick={back} label={team.tooltip} className="min-w-0 shrink overflow-hidden">
          <NameCapsule team={team} className="block" style={{ maxWidth: CAPSULE_MAX_W }} />
        </TeamStripButton>
        <div data-testid="team-strip-cells" className="pointer-events-auto flex items-center min-w-0" style={{ ...NO_DRAG, columnGap: CELL_GAP }}>
          {shown.map((s, i) => (
            <Fragment key={s.sessionId}>
              {i === 1 && <CellSep />}
              <span className="flex items-center">{cell(s)}</span>
            </Fragment>
          ))}
        </div>
        {hidden > 0 && <MoreChip count={hidden} onClick={back} />}
      </div>
      {hidden > 0 && (
        // Out of sight and out of reach (not focusable, not clickable): only here so their widths can be measured.
        <div data-testid="team-strip-measure" aria-hidden="true" className="absolute left-0 top-0 flex items-center whitespace-nowrap" style={{ visibility: 'hidden', pointerEvents: 'none', height: 0, overflow: 'hidden', width: 'max-content', columnGap: CELL_GAP }}>
          {rest.map((s) => <span key={s.sessionId} className="flex items-center">{cell(s)}</span>)}
        </div>
      )}
    </div>
  )
}

function MoreChip({ count, onClick }: { count: number; onClick: () => void }) {
  const t = useI18nStore((s) => s.t)
  return (
    <TeamStripButton testId="team-strip-more" onClick={onClick} label={t('team.titlebar.more', { count })} className="flex-shrink-0">
      <span className="block text-[11px] leading-[18px] px-1.5 rounded bg-surface-hover text-text-secondary tabular-nums" style={{ minWidth: PLUS_CHIP_W }}>+{count}</span>
    </TeamStripButton>
  )
}

/** A no-drag, clickable piece of the strip. A mouse press leaves focus on the pane. */
function TeamStripButton({ testId, onClick, label, className, children }: { testId: string; onClick: () => void; label: string; className: string; children: React.ReactNode }) {
  return (
    <button
      type="button"
      data-testid={testId}
      title={label}
      aria-label={label}
      className={`pointer-events-auto cursor-pointer ${className}`}
      style={NO_DRAG}
      onMouseDown={keepFocus}
      onClick={onClick}
    >
      {children}
    </button>
  )
}

/** The Notebook button, left of 無人值守模式: lit while the area is in the title bar, dim in the pane; a press moves it. */
export function TeamNotebookButton({ team }: { team: TeamPanelTeam }) {
  return <NotebookButton inBar={team.mode === 'titlebar'} onToggle={() => useTeamUiStore.getState().toggleTitleBar(team.teamKey)} />
}

/** The same button for a tab of no team whose own conversation has a workbook: it moves the shared value. */
export function OwnNotebookButton() {
  const inBar = useTeamUiStore((s) => s.sharedPanelMode === 'titlebar')
  return <NotebookButton inBar={inBar} onToggle={() => useTeamUiStore.getState().toggleSharedTitleBar()} />
}

/**
 * The strip of a tab of no team, while the shared value is `titlebar`: one line, the first sentence of the conversation's
 * latest status; a click brings the area into the pane at full. Same box and sizing contract as `TeamTitleStrip`.
 */
export function OwnTitleStrip({ target, room = null, onContentWidth }: { target: OwnWorkbookTarget; room?: number | null; onContentWidth?: (w: number) => void }) {
  const inner = useRef<HTMLDivElement>(null)
  const line = useOwnStatusLine(target.hostId, target.sessionId)
  const open = useI18nStore((s) => s.t)('team.workbook.open')
  useLayoutEffect(() => {
    const el = inner.current
    if (!el || !onContentWidth) return
    const report = () => onContentWidth(el.offsetWidth)
    report()
    if (typeof ResizeObserver === 'undefined') return () => onContentWidth(0)
    const ro = new ResizeObserver(report)
    ro.observe(el)
    return () => { ro.disconnect(); onContentWidth(0) }
  }, [onContentWidth])
  return (
    <div
      data-testid="own-title-strip"
      className={`flex items-center justify-end relative min-w-0 overflow-hidden pointer-events-none ${room === null ? 'flex-1' : 'shrink-0'}`}
      style={{ ...(room === null ? null : { width: room }), marginRight: HEADER_GAP }}
    >
      <div ref={inner} className="flex items-center min-w-0 max-w-full">
        <TeamStripButton testId="own-strip-line" onClick={() => useTeamUiStore.getState().setSharedPanelMode('full')} label={line.full !== '' ? line.full : open} className="min-w-0 shrink overflow-hidden">
          <span className="block truncate text-[11px] leading-[18px] text-text-secondary">{line.text}</span>
        </TeamStripButton>
      </div>
    </div>
  )
}

function NotebookButton({ inBar, onToggle }: { inBar: boolean; onToggle: () => void }) {
  const t = useI18nStore((s) => s.t)
  const label = t(inBar ? 'team.titlebar.to_pane' : 'team.titlebar.to_titlebar')
  return (
    <div data-testid="team-notebook-wrap" className="shrink-0 flex items-center translate-y-[2.5px] mr-0.5" style={NO_DRAG}>
      <button
        type="button"
        data-testid="team-notebook-button"
        aria-pressed={inBar}
        aria-label={label}
        title={label}
        className={`${BUTTON} ${inBar ? PRESSED : IDLE}`}
        onMouseDown={keepFocus}
        onClick={onToggle}
      >
        <Notebook size={14} weight={inBar ? 'fill' : 'regular'} />
      </button>
    </div>
  )
}
