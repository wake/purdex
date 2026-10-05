// spa/src/components/HandoffDialogHost.tsx — the one "Hand to nex" confirm dialog (shell cleanup spec §9.4), mounted
// once with the app-level overlays. It renders from `useHandoffDialogStore`, which the pane context menu and the status
// bar's mode buttons open.
//
// It closes by itself when the target can no longer be handed off from where it was opened:
// - the target's host is hidden in this workbench (host ownership H2d-3/H2d-4): the pane is gated, so nothing may be
//   handed off from it;
// - the pane no longer holds that session — the tab or the pane is gone, or it shows another session or another tmux
//   process of it (the same identity `handToNex`'s checked swap compares).
// A request already confirmed is not abortable: closing only unmounts the dialog. `handToNex` re-checks both before it
// writes the pane, and the dialog's completion still shows its toast (with "Open execution" when the swap missed).
import { useEffect } from 'react'
import { HandoffConfirmDialog } from './HandoffConfirmDialog'
import { useHandoffDialogStore, type HandoffDialogTarget } from '../stores/useHandoffDialogStore'
import { useTabStore } from '../stores/useTabStore'
import { findPane } from '../lib/pane-tree'
import { usePaneHostShown } from '../lib/shown-hosts'
import type { PaneContent, TmuxSessionContent } from '../types/tab'

function holdsSession(content: PaneContent | undefined, session: TmuxSessionContent): boolean {
  return content?.kind === 'tmux-session'
    && content.hostId === session.hostId
    && content.sessionCode === session.sessionCode
    && content.tmuxInstance === session.tmuxInstance
}

export function HandoffDialogHost() {
  const target = useHandoffDialogStore((s) => s.target)
  if (!target) return null
  // Keyed by the pane: moving the dialog to another pane is a fresh dialog (busy off, "keep the session" checked).
  return <OpenHandoffDialog key={`${target.tabId}\u0000${target.paneId}`} target={target} />
}

function OpenHandoffDialog({ target }: { target: HandoffDialogTarget }) {
  const { tabId, paneId, content } = target
  const hostShown = usePaneHostShown(content)
  const holds = useTabStore((s) => {
    const tab = s.tabs[tabId]
    return tab !== undefined && holdsSession(findPane(tab.layout, paneId)?.content, content)
  })
  const live = hostShown && holds
  useEffect(() => {
    if (!live) useHandoffDialogStore.getState().closeFor(target)
  }, [live, target])
  if (!live) return null
  return (
    <HandoffConfirmDialog
      hostId={content.hostId}
      sessionCode={content.sessionCode}
      tmuxInstance={content.tmuxInstance}
      cachedName={content.cachedName}
      tabId={tabId}
      paneId={paneId}
      onClose={() => useHandoffDialogStore.getState().closeFor(target)}
    />
  )
}
