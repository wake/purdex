// spa/src/components/team/TeamTitleBar.tsx — the panel area's title-bar state (team spec §4.4 Round 3): the strip in the
// middle of the window title bar (the one-line content: team name + cells, no wrap, overflow -> 「+N」) and the Notebook
// button that moves the area between the title bar and the pane. TitleBar.tsx only places them.
//
// Only the strip's content is no-drag: the strip box itself ignores the pointer, so the empty part of the title bar still
// drags the window. The state lives in `useTeamUiStore` (per team), so a tab switch or a reload comes back the same.
import { useRef } from 'react'
import { Notebook } from '@phosphor-icons/react'
import { useTeamDisplay, type TeamPanelTeam } from './team-display'
import { CellSep, NameCapsule, TeamCell } from './TeamCell'
import { useCellCapacity } from './useCellCapacity'
import { CAPSULE_MAX_W, CELL_GAP, HEADER_GAP, PLUS_CHIP_W } from './panel-layout'
import { BUTTON, IDLE, PRESSED } from '../title-bar-styles'
import { useTabStore } from '../../stores/useTabStore'
import { useTeamUiStore } from '../../stores/useTeamUiStore'
import { useI18nStore } from '../../stores/useI18nStore'
import { keepFocus } from '../../lib/keep-focus'

const NO_DRAG = { WebkitAppRegion: 'no-drag' } as React.CSSProperties

/** The strip: shown by TitleBar in place of the window title while the team's area is in the title bar. */
export function TeamTitleStrip({ team }: { team: TeamPanelTeam }) {
  const display = useTeamDisplay()
  const activeTabId = useTabStore((s) => s.activeTabId)
  const box = useRef<HTMLDivElement>(null)
  const seats = [team.lead, ...team.members]
  // The room is measured: the strip's width, less the team name's worst case, and 「+N」 when someone does not fit. Where
  // nothing can be measured (no layout) everyone is drawn.
  const cap = useCellCapacity(box, {
    key: `${team.teamKey}|${seats.map((s) => s.sessionId).join(',')}`,
    total: seats.length,
    base: CAPSULE_MAX_W + HEADER_GAP,
    reserve: PLUS_CHIP_W + HEADER_GAP,
    min: 0,
  }) ?? seats.length
  const shown = seats.slice(0, cap)
  const hidden = seats.length - shown.length
  const back = () => useTeamUiStore.getState().toggleTitleBar(team.teamKey)
  return (
    <div
      ref={box}
      data-testid="team-title-strip"
      className="flex items-center justify-center min-w-0 w-full max-w-[calc(100%-27rem)] overflow-hidden pointer-events-none"
      style={{ columnGap: HEADER_GAP }}
    >
      <TeamStripButton testId="team-strip-name" onClick={back} label={team.tooltip} className="min-w-0 shrink overflow-hidden">
        <NameCapsule team={team} className="block" style={{ maxWidth: CAPSULE_MAX_W }} />
      </TeamStripButton>
      <div data-testid="team-strip-cells" className="pointer-events-auto flex items-center min-w-0" style={{ ...NO_DRAG, columnGap: CELL_GAP }}>
        {shown.map((s, i) => (
          <span key={s.sessionId} className="flex items-center">
            {i === 1 && <CellSep />}
            <TeamCell
              teamKey={team.teamKey}
              seat={s}
              isActive={s.tabId !== null && s.tabId === activeTabId}
              onOpen={(sessionId) => display?.onOpenSeat(team.teamKey, sessionId)}
            />
          </span>
        ))}
      </div>
      {hidden > 0 && <MoreChip count={hidden} onClick={back} />}
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
  const t = useI18nStore((s) => s.t)
  const inBar = team.mode === 'titlebar'
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
        onClick={() => useTeamUiStore.getState().toggleTitleBar(team.teamKey)}
      >
        <Notebook size={14} weight={inBar ? 'fill' : 'regular'} />
      </button>
    </div>
  )
}
