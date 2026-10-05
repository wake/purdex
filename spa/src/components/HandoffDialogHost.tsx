// spa/src/components/HandoffDialogHost.tsx — the one "Hand to nex" confirm dialog (shell cleanup spec §9.4), mounted
// once with the app-level overlays. It renders from `useHandoffDialogStore`, which the pane context menu and the status
// bar's mode buttons open.
//
// It closes by itself when the target can no longer be handed off from where it was opened:
// - the pane no longer holds that session — the tab or the pane is gone, or it shows another session or another tmux
//   process of it (the same identity `handToNex`'s checked swap compares);
// - the shared handoff gate (`useHandoffGate`, the one the menu and the mode buttons open it through) closes on the
//   pane's LIVE content (P6 review A1): its host is hidden in this workbench (host ownership H2d-3/H2d-4: the pane is
//   gated, so nothing may be handed off from it), the session terminated, Claude Code no longer runs in it, or Nex
//   stopped being ready on the host.
// The close is an effect after a render, so Confirm re-reads the same gate from the stores at the click
// (`handoffBlockReasonNow`). A request already confirmed is not abortable: closing only unmounts the dialog.
// `handToNex` re-checks readiness before it sends and the host and pane before it writes the pane, and the dialog's
// completion still shows its toast (with "Open execution" when the swap missed).
import { useEffect } from 'react'
import { HandoffConfirmDialog } from './HandoffConfirmDialog'
import { useHandoffDialogStore, type HandoffDialogTarget } from '../stores/useHandoffDialogStore'
import { useTabStore } from '../stores/useTabStore'
import { findPane } from '../lib/pane-tree'
import { useHandoffGate } from '../hooks/useHandoffCandidate'
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
  const { tabId, paneId, content, mode } = target
  // The pane's content as the store holds it now: the same object until something rewrites the pane.
  const liveContent = useTabStore((s) => {
    const tab = s.tabs[tabId]
    return tab ? findPane(tab.layout, paneId)?.content : undefined
  })
  // A gone pane hands the gate `undefined` → closed (`not_session`); a hidden host closes it as `host_hidden`.
  const gate = useHandoffGate(liveContent)
  const live = holdsSession(liveContent, content) && gate.ok
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
      {...(mode ? { mode } : {})}
      onClose={() => useHandoffDialogStore.getState().closeFor(target)}
    />
  )
}
