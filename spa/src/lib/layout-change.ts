// spa/src/lib/layout-change.ts — which panes survive a title-bar layout change (shell cleanup spec §10, rule D.1a).
//
// Pure: the caller passes the agent-pane predicate (`isAgentPane`, spec §9.1) and the tab's focus record
// (`usePaneFocusStore.recent[tabId]`, most recent first; it may hold dead ids). The result says whether the change can
// apply at once, needs a confirm, or needs the user to pick; `applyLayoutPattern(layout, pattern, keepIds)` does the
// rest.
import { collectLeaves } from './pane-tree'
import type { LayoutPattern, Pane, PaneContent, PaneLayout } from '../types/tab'

export type LayoutChangePlan =
  /** Case 1: every content pane fits. Nothing with content closes, so no dialog. */
  | { kind: 'apply'; keepIds: string[] }
  /** Case 2: exactly k agent panes. They are kept; `closing` (content panes, layout order) is listed in a confirm. */
  | { kind: 'confirm'; keepIds: string[]; closing: Pane[] }
  /**
   * Case 3: the user ticks exactly `k` of `candidates` (every content pane, layout order). `preselected` holds the k
   * most recently focused candidates, most recent first, topped up in layout order.
   */
  | { kind: 'pick'; k: number; candidates: Pane[]; preselected: string[] }

export interface LayoutChangeInput {
  isAgent: (content: PaneContent) => boolean
  recent: readonly string[] | undefined
}

/** How many panes `pattern` holds. */
export function slotsOf(pattern: LayoutPattern): number {
  return pattern === 'single' ? 1 : 2
}

/** A content pane is any leaf that is not a blank `new-tab` pane: closing it loses something the user opened. */
function isContentPane(pane: Pane): boolean {
  return pane.content.kind !== 'new-tab'
}

/** Rule D.1a for changing `layout` to `pattern`. */
export function planLayoutChange(layout: PaneLayout, pattern: LayoutPattern, { isAgent, recent }: LayoutChangeInput): LayoutChangePlan {
  const k = slotsOf(pattern)
  const leaves = collectLeaves(layout)
  const content = leaves.filter(isContentPane)

  // 1. Everything with content fits. Free slots take the existing blank panes first (layout order), so a blank pane
  //    the user already placed keeps its id and position; `applyLayoutPattern` makes fresh ones for the rest.
  if (content.length <= k) {
    const blanks = leaves.filter((p) => !isContentPane(p)).slice(0, k - content.length)
    const keep = new Set([...content, ...blanks])
    return { kind: 'apply', keepIds: leaves.filter((p) => keep.has(p)).map((p) => p.id) }
  }

  // 2. Exactly as many agent panes as slots: keep those.
  const agents = content.filter((p) => isAgent(p.content))
  if (agents.length === k) {
    return { kind: 'confirm', keepIds: agents.map((p) => p.id), closing: content.filter((p) => !agents.includes(p)) }
  }

  // 3. The user picks. Preselect the k most recently focused content panes, then top up in layout order.
  const contentIds = new Set(content.map((p) => p.id))
  const preselected: string[] = []
  for (const id of recent ?? []) {
    if (preselected.length === k) break
    if (contentIds.has(id) && !preselected.includes(id)) preselected.push(id)
  }
  for (const p of content) {
    if (preselected.length === k) break
    if (!preselected.includes(p.id)) preselected.push(p.id)
  }
  return { kind: 'pick', k, candidates: content, preselected }
}
