// spa/src/hooks/useStatusTargetPane.ts — the one pane of the active tab the status bar shows (shell cleanup spec §9.1,
// rule D.4). The rule itself is `statusTargetOf` in `lib/pane-focus.ts`; this hook feeds it the tab's focus record and
// the agent-pane predicate, and re-renders only when either actually changes for this tab.
import { useMemo } from 'react'
import { useAgentStore } from '../stores/useAgentStore'
import { usePaneFocusStore } from '../stores/usePaneFocusStore'
import { compositeKey } from '../lib/composite-key'
import { collectLeaves } from '../lib/pane-tree'
import { statusTargetOf } from '../lib/pane-focus'
import type { Pane, PaneContent, Tab } from '../types/tab'

/**
 * An agent pane (spec §9.1): any `execution` (worker) pane, or a `tmux-session` pane that is not terminated and whose
 * session has a detected agent type. A terminated pane's code may already belong to another session after a tmux
 * restart, so a leftover agent type there says nothing about this pane.
 */
export function isAgentPane(content: PaneContent, agentTypes: Readonly<Record<string, string>>): boolean {
  if (content.kind === 'execution') return true
  if (content.kind !== 'tmux-session' || content.terminated) return false
  return !!agentTypes[compositeKey(content.hostId, content.sessionCode)]
}

/** The ids of `tab`'s agent panes in layout order, as one string, so a selector over it compares by value. */
function agentPaneKey(tab: Tab, agentTypes: Readonly<Record<string, string>>): string {
  return collectLeaves(tab.layout)
    .filter((p) => isAgentPane(p.content, agentTypes))
    .map((p) => p.id)
    .join('\n')
}

/**
 * Rule D.4 for `tab`, live: the most recently focused agent pane, else the first agent pane, else the most recently
 * focused pane, else the primary pane. Null only when there is no tab.
 *
 * Subscriptions are narrow: the agent store is read through one selector that reduces it to this tab's agent pane ids
 * (a string), so the agent events that stream in for every session (status, model, titles) re-render nothing here
 * unless a pane of this tab gains or loses its agent.
 */
export function useStatusTargetPane(tab: Tab | null): Pane | null {
  const recent = usePaneFocusStore((s) => (tab ? s.recent[tab.id] : undefined))
  const agentKey = useAgentStore((s) => (tab ? agentPaneKey(tab, s.agentTypes) : ''))
  return useMemo(() => {
    if (!tab) return null
    const agentIds = new Set(agentKey ? agentKey.split('\n') : [])
    // `statusTargetOf` asks about content, the key holds pane ids: map one to the other through this tab's leaves.
    const agentContents = new Set(collectLeaves(tab.layout).filter((p) => agentIds.has(p.id)).map((p) => p.content))
    return statusTargetOf(tab, recent, (content) => agentContents.has(content))
  }, [tab, recent, agentKey])
}
