// spa/src/components/team/TeamPanelArea.tsx — the one panel area, mounted once by the shell (team spec §4.4 amended, WA-2a).
//
// A floating layer over the top-right of the pane area (the shell's content box is `relative`): the panes keep their
// size, the tab bar is never covered. It shows the team view for the active tab's team (`panelView`), or that team's
// drilled-in seat workbook in the same frame; a tab's own workbook renders nothing yet (WA-2b-1b). Everything the person arranged lives in `useTeamUiStore` (width, and per team one of four
// states: titlebar | line | full | max), so a tab switch that unmounts the pane and a reload both come back to the same
// area. In the `titlebar` state the pane draws nothing: the strip in the title bar (TeamTitleStrip) is the area.
//
// Resize: the LEFT edge, draft-then-commit (the ActivityBarWide pattern): a drag only moves a local draft width and the
// store is written once on mouseup. The area never calls focus(): the terminal keeps the keyboard.
import { useEffect, useRef, useState } from 'react'
import { RegionResize } from '../RegionResize'
import { useTabStore } from '../../stores/useTabStore'
import { PANEL_MAX_WIDTH, currentPanelMin, useTeamUiStore } from '../../stores/useTeamUiStore'
import { useI18nStore } from '../../stores/useI18nStore'
import { useTeamDisplay } from './team-display'
import { panelView } from './panel-view'
import { TeamPanel } from './TeamPanel'

export function TeamPanelArea() {
  const t = useI18nStore((s) => s.t)
  const display = useTeamDisplay()
  const activeTabId = useTabStore((s) => s.activeTabId)
  const workbookTabs = useTeamUiStore((s) => s.workbookTabs)
  const teamDrill = useTeamUiStore((s) => s.teamDrill)
  const { width } = useTeamUiStore((s) => s.panel)
  const view0 = display ? panelView(activeTabId, { workbookTabs, panelTeam: display.panelTeam(activeTabId), teamDrill }) : null
  // The team the area draws a frame for: the team view, or the team's drilled-in seat workbook (same frame, same mode).
  const shownTeam = view0 !== null && 'team' in view0 ? view0.team : null
  const expanded = shownTeam?.mode === 'max'
  // A store saved before the four states held `expanded: true`: the team showing when the area first draws becomes `max`
  // (an old value with no team on screen is dropped). Once per load.
  const teamOnShow = shownTeam?.teamKey ?? null
  useEffect(() => { useTeamUiStore.getState().takeLegacyMax(teamOnShow) }, [teamOnShow])
  const [draft, setDraft] = useState<number | null>(null)
  const draftRef = useRef<number | null>(null)
  // The handle unmounts when the panel is enlarged (or goes away): a half-done drag is abandoned, so drop its draft.
  const [wasExpanded, setWasExpanded] = useState(expanded)
  if (expanded !== wasExpanded) { // adjust state during render (no effect round trip)
    setWasExpanded(expanded)
    if (expanded) setDraft(null)
  }
  useEffect(() => {
    if (expanded) draftRef.current = null
  }, [expanded])

  // In the title bar the area is the strip's (TeamTitleStrip), not the pane's.
  if (!display || !shownTeam || shownTeam.mode === 'titlebar') return null
  const team = shownTeam

  return (
    <div
      data-testid="team-panel-area"
      data-expanded={String(expanded)}
      className={`absolute z-20 flex font-sans ${expanded ? 'inset-3' : 'top-0 right-3 max-h-[calc(100%-12px)]'}`}
      style={expanded ? undefined : { width: draft ?? width }}
    >
      {!expanded && (
        <div className="absolute inset-y-0 left-0 z-10 flex" title={t('team.panel.resize')}>
          <RegionResize
            resizeEdge="left"
            onResize={(delta) => {
              // The latest committed width plus the drag so far; the store stays untouched until mouseup.
              const base = draftRef.current ?? useTeamUiStore.getState().panel.width
              const next = Math.max(currentPanelMin(), Math.min(PANEL_MAX_WIDTH, base + delta))
              draftRef.current = next
              setDraft(next)
            }}
            onResizeEnd={() => {
              if (draftRef.current !== null) {
                useTeamUiStore.getState().setPanelWidth(draftRef.current)
                draftRef.current = null
                setDraft(null)
              }
            }}
          />
        </div>
      )}
      <div className="flex-1 min-w-0 overflow-y-auto rounded-b-lg border border-t-0 border-border-default bg-surface-elevated shadow-xl">
        <TeamPanel
          team={team}
          activeTabId={activeTabId}
          width={expanded ? undefined : draft ?? width}
          onSetMode={(mode) => useTeamUiStore.getState().setPanelMode(team.teamKey, mode)}
          onOpen={(sessionId) => display.onOpenSeat(team.teamKey, sessionId)}
          onReorder={(ids) => display.onReorderMembers(team.teamKey, ids)}
        />
      </div>
    </div>
  )
}
