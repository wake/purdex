// spa/src/lib/conversations/pane-release.ts — the one place a session pane's in-memory state is let go (#2457). The deck, the
// chat and the input keep their state in modules OUTSIDE the components (the tab-hosted rule: a pane unmounts on a tab switch),
// which means nothing frees it when the pane is gone for good. Per-pane / per-session memos and their keys:
//
//   memo                                   key                                           freed by
//   send queue   (send-queue)              pane|host|session                             releasePane, retireStaleSessions
//   draft        (draft-memory)            pane|host|session                             releasePane, retireStaleSessions
//   attachments  (attachment-memory)       pane|host|session                             releasePane, retireStaleSessions
//   uploads      (attachment-upload)       pane|host|session                             releasePane, retireStaleSessions (aborts; drops unseen failures)
//   right panel  (panel-memory)            pane                                          releasePane (a stale binding is dropped by the panel itself)
//   fold         (fold-memory)             deck: pane\0session   chat: pane\0host\0session releasePane, retireStaleSessions
//   scroll       (transcript-scroll-memory) deck: pane\0session   chat: pane\0host\0session\0chat   releasePane, retireStaleSessions
//   deck panes   (fold-memory `shown`)     pane                                          releasePane
//   workbook view (workbook/view-memory)   not a session pane's: its own LRU, untouched
//
// "Gone for good" is: the pane is in NO world any more — not on screen and not parked in the master's or a slave's world (a
// profile switch swaps the live tabs and parks the old ones; a failed switch is rolled back; neither kills a pane) and not in a
// closed tab that can still be reopened (`useHistoryStore.closedTabs`, not yet reopened: reopening hands back the same pane ids,
// so while the record is kept the pane's state is kept and works as before) — because its tab was closed and the record was
// reopened, evicted (cap) or cleared, the pane was closed, a world was replaced or thrown away; or
// the pane now shows another conversation and the old one will not be shown again (/clear, relay, rebuild). A tab SWITCH is
// neither: the pane unmounts and comes back, and the undo window, the queued messages and the draft must be there.
import { scanPaneTree } from '../pane-tree'
import { forgetScrollMemo, forgetScrollMemosWithPrefix } from '../nex/transcript-scroll-memory'
import { useHistoryStore } from '../../stores/useHistoryStore'
import { useLocalProfilesStore } from '../../stores/useLocalProfilesStore'
import { useTabStore } from '../../stores/useTabStore'
import { forgetAttachmentsWhere } from './attachment-memory'
import { forgetUploadsWhere } from './attachment-upload'
import { forgetDraftsWhere, draftKey } from './draft-memory'
import { forgetDeckPane, forgetFoldsOfPane } from './fold-memory'
import { chatScrollKey, conversationBinding, forgetPanel } from './panel-memory'
import { releaseSendQueues, retireSendQueues } from './send-queue'

/** Everything the pane keeps in memory, for good. */
export function releasePane(paneId: string): void {
  const mine = (key: string) => key.startsWith(`${paneId}|`)
  releaseSendQueues(mine)
  forgetDraftsWhere(mine)
  forgetAttachmentsWhere(mine)
  forgetUploadsWhere(mine)
  forgetPanel(paneId)
  forgetFoldsOfPane(paneId)
  forgetScrollMemo(paneId)
  forgetScrollMemosWithPrefix(paneId)
  forgetDeckPane(paneId)
}

/**
 * The pane now shows `sessionId`: what it kept for its OTHER conversations will not be read again. Their queued messages (in the
 * undo window, waiting for idle) are dropped: nobody is left to drive them and the old text must not reach another session. A
 * request already in flight runs to its end, then its queue frees itself (`SendQueue.retire`), so nothing stays in the registry.
 */
export function retireStaleSessions(paneId: string, hostId: string, sessionId: string): void {
  if (!sessionId) return
  const now = draftKey(paneId, hostId, sessionId)
  retireSendQueues((key) => key.startsWith(`${paneId}|`) && key !== now)
  forgetDraftsWhere((key) => key.startsWith(`${paneId}|`) && key !== now)
  forgetAttachmentsWhere((key) => key.startsWith(`${paneId}|`) && key !== now)
  forgetUploadsWhere((key) => key.startsWith(`${paneId}|`) && key !== now)
  const binding = conversationBinding(hostId, sessionId)
  const chatFold = `${paneId}\0${binding}`
  const deck = `${paneId}\0${sessionId}`
  const current = (key: string) => key === deck || key === chatFold || key === chatScrollKey(paneId, binding)
  forgetFoldsOfPane(paneId, current)
  forgetScrollMemosWithPrefix(paneId, current)
}

let uninstall: (() => void) | null = null

type Tabs = ReturnType<typeof useTabStore.getState>['tabs']

function addPaneIds(into: Set<string>, tabs: Tabs): void {
  for (const tab of Object.values(tabs)) scanPaneTree(tab.layout, (pane) => into.add(pane.id))
}

/** Every pane that still lives somewhere: on screen, or parked in the master's / a slave's world (a profile switch parks the old world, and it can come back). */
function livePaneIds(): Set<string> {
  const ids = new Set<string>()
  addPaneIds(ids, useTabStore.getState().tabs)
  const p = useLocalProfilesStore.getState()
  if (p.parkedMaster) addPaneIds(ids, p.parkedMaster.tabs)
  for (const slave of Object.values(p.slaves)) if (slave.world) addPaneIds(ids, slave.world.tabs)
  // a closed tab that has not been reopened keeps its panes: `reopenLast` hands the same tab (same pane ids) back
  for (const r of useHistoryStore.getState().closedTabs) if (r.reopenedAt === undefined) scanPaneTree(r.tab.layout, (pane) => ids.add(pane.id))
  return ids
}

/**
 * Releases every pane that is gone from EVERY world. The pane's world can change hands without the pane dying: a profile switch
 * replaces the live tabs wholesale (\`commitTabWorld\`) and parks the old world in \`useLocalProfilesStore\`, a failed commit is
 * rolled back, a parked world may come back. So a change in either store only SCHEDULES a check, and the check runs when the
 * synchronous write has finished (a microtask later): a pane is released iff it is neither on screen nor in a parked world.
 * A parked world that is later thrown away is seen the same way, through the profile store. Nothing is judged while either
 * store has not hydrated (an empty tab world, or parked worlds not yet visible). Called once below; calling it again replaces
 * the subscription. Returns the uninstall function.
 */
export function installPaneRelease(): () => void {
  uninstall?.()
  let known = livePaneIds()
  let scheduled = false
  const settled = () => {
    if (Object.keys(useTabStore.getState().tabs).length === 0 && !useTabStore.persist.hasHydrated()) return false
    return useLocalProfilesStore.persist.hasHydrated() && useHistoryStore.persist.hasHydrated()
  }
  const check = () => {
    scheduled = false
    if (!settled()) return
    const alive = livePaneIds()
    for (const id of known) if (!alive.has(id)) releasePane(id)
    known = alive
  }
  const schedule = () => {
    // a pane that appears is known at once, so one that is created and closed inside the same tick is still released
    for (const id of livePaneIds()) known.add(id)
    if (scheduled) return
    scheduled = true
    queueMicrotask(check)
  }
  const offTabs = useTabStore.subscribe((next, prev) => { if (next.tabs !== prev.tabs) schedule() })
  const offProfiles = useLocalProfilesStore.subscribe((next, prev) => {
    if (next.parkedMaster !== prev.parkedMaster || next.slaves !== prev.slaves) schedule()
  })
  const offHistory = useHistoryStore.subscribe((next, prev) => { if (next.closedTabs !== prev.closedTabs) schedule() })
  // hydration may be what makes a pending check meaningful
  const offHydration = useLocalProfilesStore.persist.onFinishHydration(() => schedule())
  const offHistoryHydration = useHistoryStore.persist.onFinishHydration(() => schedule())
  const stop = () => {
    offTabs()
    offProfiles()
    offHistory()
    offHydration()
    offHistoryHydration()
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
