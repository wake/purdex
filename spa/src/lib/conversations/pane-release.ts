// spa/src/lib/conversations/pane-release.ts — the one place a session pane's in-memory state is let go (#2457). The deck, the
// chat and the input keep their state in modules OUTSIDE the components (the tab-hosted rule: a pane unmounts on a tab switch),
// which means nothing frees it when the pane is gone for good. Per-pane / per-session memos and their keys:
//
//   memo                                   key                                           freed by
//   send queue   (send-queue)              pane|host|session                             releasePane, retireStaleSessions
//   draft        (draft-memory)            pane|host|session                             releasePane, retireStaleSessions
//   right panel  (panel-memory)            pane                                          releasePane (a stale binding is dropped by the panel itself)
//   fold         (fold-memory)             deck: pane\0session   chat: pane\0host\0session releasePane, retireStaleSessions
//   scroll       (transcript-scroll-memory) deck: pane\0session   chat: pane\0host\0session\0chat   releasePane, retireStaleSessions
//   deck panes   (fold-memory `shown`)     pane                                          releasePane
//   workbook view (workbook/view-memory)   not a session pane's: its own LRU, untouched
//
// "Gone for good" is: the pane left the tab world (its tab closed, the pane closed, a wholesale replacement of the tabs), or
// the pane now shows another conversation and the old one will not be shown again (/clear, relay, rebuild). A tab SWITCH is
// neither: the pane unmounts and comes back, and the undo window, the queued messages and the draft must be there.
import { scanPaneTree } from '../pane-tree'
import { forgetScrollMemo, forgetScrollMemosWithPrefix } from '../nex/transcript-scroll-memory'
import { useTabStore } from '../../stores/useTabStore'
import { forgetDraftsWhere, draftKey } from './draft-memory'
import { forgetDeckPane, forgetFoldsOfPane } from './fold-memory'
import { chatScrollKey, conversationBinding, forgetPanel } from './panel-memory'
import { releaseSendQueues } from './send-queue'

/** Everything the pane keeps in memory, for good. */
export function releasePane(paneId: string): void {
  const mine = (key: string) => key.startsWith(`${paneId}|`)
  releaseSendQueues(mine)
  forgetDraftsWhere(mine)
  forgetPanel(paneId)
  forgetFoldsOfPane(paneId)
  forgetScrollMemo(paneId)
  forgetScrollMemosWithPrefix(paneId)
  forgetDeckPane(paneId)
}

/**
 * The pane now shows `sessionId`: what it kept for its OTHER conversations will not be read again. A queue that still has a
 * message waiting (in its undo window, in flight, or waiting for idle) is left to finish; it goes with the pane.
 */
export function retireStaleSessions(paneId: string, hostId: string, sessionId: string): void {
  if (!sessionId) return
  const now = draftKey(paneId, hostId, sessionId)
  releaseSendQueues((key) => key.startsWith(`${paneId}|`) && key !== now, { keepPending: true })
  forgetDraftsWhere((key) => key.startsWith(`${paneId}|`) && key !== now)
  const binding = conversationBinding(hostId, sessionId)
  const chatFold = `${paneId}\0${binding}`
  const deck = `${paneId}\0${sessionId}`
  const current = (key: string) => key === deck || key === chatFold || key === chatScrollKey(paneId, binding)
  forgetFoldsOfPane(paneId, current)
  forgetScrollMemosWithPrefix(paneId, current)
}

let uninstall: (() => void) | null = null

function paneIds(tabs: ReturnType<typeof useTabStore.getState>['tabs']): Set<string> {
  const ids = new Set<string>()
  for (const tab of Object.values(tabs)) scanPaneTree(tab.layout, (pane) => ids.add(pane.id))
  return ids
}

/**
 * Releases every pane that disappears from the tab world. A subscription on the tab store rather than a hook in its close path,
 * because the world can also be replaced wholesale (cross-window rehydrate, Profile Sync apply). An empty world is acted on too
 * (the last tab closed), except before the store has hydrated, when it only looks empty. Called once below; calling it again
 * replaces the subscription. Returns the uninstall function.
 */
export function installPaneRelease(): () => void {
  uninstall?.()
  const off = useTabStore.subscribe((next, prev) => {
    if (next.tabs === prev.tabs) return
    if (Object.keys(next.tabs).length === 0 && !useTabStore.persist.hasHydrated()) return
    const alive = paneIds(next.tabs)
    for (const id of paneIds(prev.tabs)) if (!alive.has(id)) releasePane(id)
  })
  const stop = () => {
    off()
    if (uninstall === stop) uninstall = null
  }
  uninstall = stop
  return stop
}

installPaneRelease()

// HMR: a hot reload of this module must not leave the old subscription running.
if (import.meta.hot) {
  import.meta.hot.dispose(() => uninstall?.())
}
