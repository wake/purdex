// spa/src/lib/pool/submitter-tab.ts — the tab a job's submitter sits in (plan "WWD-7 design": the detail's submitter is a link to its tab). The match is the one the tab's hourglass makes (hooks/useTabQueued.ts): the job's `submitter.session`
// against the agent session id a pane keeps (`TabAgentPane.sessionId`), the case ignored. A job from before #3477 has no session, and a session no tab holds is none: the detail then shows text.
import { useMemo } from 'react'
import { useTabStore } from '../../stores/useTabStore'
import { tabAgentPanes } from '../agent-lights/tab-aggregate'
import { compositeKey } from '../composite-key'
import { collectLeaves, getPrimaryPane } from '../pane-tree'
import { activateTabPane } from '../open-session-tab'

export interface SubmitterTab { tabId: string; paneId: string }

/** The first tab (in the store's order) with a pane that holds `session`, and that pane. */
export function findSubmitterTab(session: string): SubmitterTab | null {
  if (session === '') return null
  const want = session.toLowerCase()
  for (const tab of Object.values(useTabStore.getState().tabs)) {
    const hit = tabAgentPanes(tab.layout).find((p) => p.sessionId?.toLowerCase() === want)
    if (!hit) continue
    const leaf = collectLeaves(tab.layout).find((l) => l.content.kind === 'tmux-session' && compositeKey(l.content.hostId, l.content.sessionCode) === hit.key)
    return { tabId: tab.id, paneId: (leaf ?? getPrimaryPane(tab.layout)).id }
  }
  return null
}

/** The submitter's tab for a view: the selector returns a string, so a store change that moves nothing for this session does not re-render it. */
export function useSubmitterTab(session: string): SubmitterTab | null {
  const sig = useTabStore(() => { const to = findSubmitterTab(session); return to ? `${to.tabId}\u0000${to.paneId}` : '' })
  return useMemo(() => { const [tabId, paneId] = sig.split('\u0000'); return sig === '' ? null : { tabId, paneId } }, [sig])
}

/** Show the tab (and its workspace) with that pane taking the keyboard. */
export const showSubmitterTab = (to: SubmitterTab): void => activateTabPane(to.tabId, to.paneId)
