// spa/src/components/team/TeamPanelArea.tsx — the one panel area, mounted once by the shell (team spec §4.4 amended, WA-2a).
//
// A floating layer over the top-right of the pane area (the shell's content box is `relative`): the panes keep their
// size, the tab bar is never covered. It shows the team view for the active tab's team (`panelView`), or that team's
// drilled-in seat workbook in the same frame; a tab of no team shows its own conversation's workbook (WA-2b-1b), in the
// one value all such tabs share. Everything the person arranged lives in `useTeamUiStore` (width, and per team one of three
// states: titlebar | line | full), so a tab switch that unmounts the pane and a reload both come back to the same
// area. In the `titlebar` state the pane draws nothing: the strip in the title bar (TeamTitleStrip) is the area.
//
// Resize: the LEFT edge, draft-then-commit (the ActivityBarWide pattern): a drag only moves a local draft width and the
// store is written once on mouseup. The area never calls focus(): the terminal keeps the keyboard.
import { useEffect, useRef, useState } from 'react'
import { RegionResize } from '../RegionResize'
import { useTabStore } from '../../stores/useTabStore'
import { PANEL_MAX_WIDTH, currentPanelMin, useTeamUiStore } from '../../stores/useTeamUiStore'
import { useJournalTarget } from '../journal/useJournalTarget'
import { useIsJournalHost } from '../../lib/journal/use-journal-host'
import { useI18nStore } from '../../stores/useI18nStore'
import { useTeamDisplay } from './team-display'
import { panelView } from './panel-view'
import { useOwnWorkbook } from './own-workbook'
import { TeamPanel } from './TeamPanel'
import { OwnWorkbookPanel } from './OwnWorkbookPanel'

export function TeamPanelArea() {
  const t = useI18nStore((s) => s.t)
  const display = useTeamDisplay()
  const activeTabId = useTabStore((s) => s.activeTabId)
  const teamDrill = useTeamUiStore((s) => s.teamDrill)
  const { width } = useTeamUiStore((s) => s.panel)
  const own = useOwnWorkbook()
  const sharedMode = useTeamUiStore((s) => s.sharedPanelMode)
  const panelTeam = display ? display.panelTeam(activeTabId) : null
  // On a journal.v1 host (J-2b) the workbook branches stay off: the team view only. Either host decides: the tab's own host (its cc
  // session) or the team's lead host. `useJournalTarget().journalHost` is the host the unit is READ from, which a follow route moves to
  // the lead's, so it cannot stand for the tab's own.
  const originHost = useJournalTarget().origin?.hostId ?? ''
  const tabJournal = useIsJournalHost(originHost)
  const leadJournal = useIsJournalHost(panelTeam?.lead.hostId ?? '')
  const noDrill = tabJournal || leadJournal
  const view0 = panelView({ panelTeam, teamDrill, own, journalHost: noDrill })
  // The team the area draws a frame for: the team view, or the team's drilled-in seat workbook (same frame, same mode).
  const shownTeam = view0 !== null && 'team' in view0 ? view0.team : null
  // A tab of no team shows its own workbook in the one value all such tabs share.
  const mode = shownTeam ? shownTeam.mode : view0 !== null ? sharedMode : null
  const [draft, setDraft] = useState<number | null>(null)
  const draftRef = useRef<number | null>(null)
  // In the title bar the area is the strip's (TeamTitleStrip), not the pane's.
  const hidden = view0 === null || mode === null || mode === 'titlebar'
  // The handle unmounts when the area goes away: a half-done drag is abandoned, so drop its draft.
  const [wasHidden, setWasHidden] = useState(hidden)
  if (hidden !== wasHidden) { // adjust state during render (no effect round trip)
    setWasHidden(hidden)
    if (hidden) setDraft(null)
  }
  useEffect(() => {
    if (hidden) draftRef.current = null
  }, [hidden])
  if (hidden) return null
  const paneMode = mode

  return (
    <div
      data-testid="team-panel-area"
      className="absolute z-20 flex font-sans top-0 right-3 max-h-[calc(100%-12px)]"
      style={{ width: draft ?? width }}
    >
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
      <div className="flex-1 min-w-0 min-h-0 flex flex-col overflow-y-auto rounded-b-lg border border-t-0 border-border-default bg-surface-elevated shadow-xl">
        {shownTeam && display ? (
          <TeamPanel
            team={shownTeam}
            activeTabId={activeTabId}
            width={draft ?? width}
            noDrill={noDrill}
            onSetMode={(m) => useTeamUiStore.getState().setPanelMode(shownTeam.teamKey, m)}
            onOpen={(sessionId) => display.onOpenSeat(shownTeam.teamKey, sessionId)}
            onReorder={(ids) => display.onReorderMembers(shownTeam.teamKey, ids)}
          />
        ) : view0.kind === 'workbook' && view0.from === 'own' ? (
          <OwnWorkbookPanel
            hostId={view0.hostId}
            sessionId={view0.sessionId}
            mode={paneMode}
            onSetMode={(m) => useTeamUiStore.getState().setSharedPanelMode(m)}
          />
        ) : null}
      </div>
    </div>
  )
}
