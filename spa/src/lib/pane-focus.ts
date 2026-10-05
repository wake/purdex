// spa/src/lib/pane-focus.ts — which pane of a tab is "the" pane, read from the focus record (shell cleanup spec §8.1).
//
// Pure helpers over a tab's layout and its entry in `usePaneFocusStore.recent` (most recent first). The record may
// hold ids of panes that have since been closed or swapped away: every reader filters against the live leaves, so
// dead ids are skipped rather than cleaned up.
import { collectLeaves, getPrimaryPane } from './pane-tree'
import type { Pane, PaneContent, PaneLayout } from '../types/tab'

/** The ids of every leaf pane in `layout`, in layout order (split nodes are not panes). */
export function liveLeafIds(layout: PaneLayout): Set<string> {
  return new Set(collectLeaves(layout).map((p) => p.id))
}

/**
 * Rule F: the pane that takes focus when the tab is shown. The first id in `recentIds` that is still a live leaf of
 * the tab, else the primary pane.
 */
export function focusTargetOf(tab: { layout: PaneLayout }, recentIds: readonly string[] | undefined): string {
  if (recentIds?.length) {
    const live = liveLeafIds(tab.layout)
    for (const id of recentIds) if (live.has(id)) return id
  }
  return getPrimaryPane(tab.layout).id
}

/**
 * Rule D.4: the one pane the status bar shows. In order:
 * 1. the most recently focused live agent pane;
 * 2. else the first agent pane in layout order;
 * 3. else the most recently focused live pane;
 * 4. else the primary pane.
 *
 * `isAgent` decides what an agent pane is (spec §9.1: a worker, or a running tmux session with a detected agent); it
 * is passed in so this stays pure.
 */
export function statusTargetOf(
  tab: { layout: PaneLayout },
  recentIds: readonly string[] | undefined,
  isAgent: (content: PaneContent) => boolean,
): Pane {
  const leaves = collectLeaves(tab.layout)
  const byId = new Map(leaves.map((p) => [p.id, p]))
  const recentLive: Pane[] = []
  for (const id of recentIds ?? []) {
    const pane = byId.get(id)
    if (pane) recentLive.push(pane)
  }
  return (
    recentLive.find((p) => isAgent(p.content)) ??
    leaves.find((p) => isAgent(p.content)) ??
    recentLive[0] ??
    getPrimaryPane(tab.layout)
  )
}
