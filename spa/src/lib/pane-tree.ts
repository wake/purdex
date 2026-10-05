import type { Pane, PaneContent, PaneLayout, LayoutPattern } from '../types/tab'
import { generateId } from './id'
import { execAgentCode } from './nex/worker-agent-status'
import { resolveExecutionHostId } from './nex/resolve-host'

export function getPrimaryPane(layout: PaneLayout): Pane {
  if (layout.type === 'leaf') return layout.pane
  if (!layout.children.length) {
    // Corrupted layout — return a placeholder to prevent crash
    return { id: 'corrupted', content: { kind: 'new-tab' } }
  }
  return getPrimaryPane(layout.children[0])
}

export function findPane(layout: PaneLayout, paneId: string): Pane | undefined {
  if (layout.type === 'leaf') {
    return layout.pane.id === paneId ? layout.pane : undefined
  }
  for (const child of layout.children) {
    const found = findPane(child, paneId)
    if (found) return found
  }
  return undefined
}

export function updatePaneInLayout(
  layout: PaneLayout,
  paneId: string,
  content: PaneContent,
): PaneLayout {
  if (layout.type === 'leaf') {
    if (layout.pane.id === paneId) {
      return { type: 'leaf', pane: { ...layout.pane, content } }
    }
    return layout
  }
  return {
    ...layout,
    children: layout.children.map((child) => updatePaneInLayout(child, paneId, content)),
  }
}

export function scanPaneTree(layout: PaneLayout, fn: (pane: Pane) => void): void {
  if (layout.type === 'leaf') {
    fn(layout.pane)
  } else {
    layout.children.forEach((child) => scanPaneTree(child, fn))
  }
}

export function getLayoutKey(layout: PaneLayout): string {
  return layout.type === 'leaf' ? layout.pane.id : layout.id
}

/**
 * Clone the leaf identified by `paneId` IN PLACE with a fresh `pane.id`, leaving
 * its content (and every sibling) untouched. Because the layout renderer keys
 * each leaf by `pane.id` (`getLayoutKey`), swapping the id changes the React key
 * → forces an unmount+remount of just that leaf, so a preview pane re-runs its
 * `[identity, backend]` read effect and shows freshly-restored bytes WITHOUT
 * moving the leaf or disturbing the split layout (Phase 2c restore, R4-P2).
 * Returns `null` when `paneId` is not in the tree.
 */
export function remountLeaf(
  layout: PaneLayout,
  paneId: string,
): { layout: PaneLayout; newPaneId: string } | null {
  if (layout.type === 'leaf') {
    if (layout.pane.id !== paneId) return null
    const newPaneId = generateId()
    return { layout: { type: 'leaf', pane: { ...layout.pane, id: newPaneId } }, newPaneId }
  }
  for (let i = 0; i < layout.children.length; i++) {
    const res = remountLeaf(layout.children[i], paneId)
    if (res) {
      const children = [...layout.children]
      children[i] = res.layout
      return { layout: { ...layout, children }, newPaneId: res.newPaneId }
    }
  }
  return null
}

/**
 * Find the tab ID whose primary pane is a `tmux-session` matching BOTH
 * `hostId` and `sessionCode`.
 *
 * Session codes are a deterministic encoding of tmux's `$N` session id
 * (`internal/module/session/codec.go`), so two hosts routinely produce the
 * same code for unrelated sessions. Matching on the code alone would land on
 * whichever host's tab happens to come first in `tabs`.
 *
 * A worker (execution) primary pane matches the agent key `exec-<executionId>`
 * on its resolved host (the pane's host hint, else the first host — the host
 * the worker projection writes under; worker-pane theme spec §8.2).
 */
export function findTabBySessionCode(
  tabs: Record<string, { layout: PaneLayout }>,
  hostId: string,
  sessionCode: string,
): string | undefined {
  for (const [tabId, tab] of Object.entries(tabs)) {
    const primary = getPrimaryPane(tab.layout)
    const c = primary.content
    if (c.kind === 'tmux-session' && c.hostId === hostId && c.sessionCode === sessionCode) return tabId
    if (c.kind === 'execution' && execAgentCode(c.executionId) === sessionCode && resolveExecutionHostId(c.host) === hostId) return tabId
  }
  return undefined
}

/**
 * Panes (across every tab, any depth) showing `hostId`/`sessionCode`, minus
 * `excludePaneId` — what a hand-off that does not keep the session will mark
 * terminated (exec-to-terminal spec §4.3).
 */
export function countPanesOnSession(
  tabs: Record<string, { layout: PaneLayout }>,
  hostId: string,
  sessionCode: string,
  excludePaneId?: string,
): number {
  let n = 0
  for (const tab of Object.values(tabs)) {
    for (const pane of collectLeaves(tab.layout)) {
      const c = pane.content
      if (c.kind === 'tmux-session' && c.hostId === hostId && c.sessionCode === sessionCode && pane.id !== excludePaneId) n++
    }
  }
  return n
}

export function splitAtPane(layout: PaneLayout, paneId: string, direction: 'h' | 'v', newContent: PaneContent): PaneLayout {
  if (layout.type === 'leaf') {
    if (layout.pane.id === paneId) {
      return { type: 'split', id: generateId(), direction, children: [layout, { type: 'leaf', pane: { id: generateId(), content: newContent } }], sizes: [50, 50] }
    }
    return layout
  }
  const newChildren = layout.children.map((child) => splitAtPane(child, paneId, direction, newContent))
  return newChildren.some((c, i) => c !== layout.children[i]) ? { ...layout, children: newChildren } : layout
}

export function removePane(layout: PaneLayout, paneId: string): PaneLayout | null {
  if (layout.type === 'leaf') return layout.pane.id === paneId ? null : layout

  const mapped = layout.children.map((child) => removePane(child, paneId))
  const newChildren = mapped.filter((c): c is PaneLayout => c !== null)

  if (newChildren.length === layout.children.length) {
    // No child was removed (null), but a child might have been modified internally
    const anyChanged = newChildren.some((c, i) => c !== layout.children[i])
    if (!anyChanged) return layout
    // Children were modified but not removed — return updated layout with same sizes
    return { ...layout, children: newChildren }
  }
  if (newChildren.length === 0) return null
  if (newChildren.length === 1) return newChildren[0]

  const keptSizes = layout.sizes.filter((_, i) => mapped[i] !== null)
  const total = keptSizes.reduce((a, b) => a + b, 0)
  const normalizedSizes = keptSizes.map((s) => (s / total) * 100)
  return { ...layout, children: newChildren, sizes: normalizedSizes }
}

export function countLeaves(layout: PaneLayout): number {
  if (layout.type === 'leaf') return 1
  return layout.children.reduce((sum, child) => sum + countLeaves(child), 0)
}

export function collectLeaves(layout: PaneLayout): Pane[] {
  if (layout.type === 'leaf') return [layout.pane]
  return layout.children.flatMap((child) => collectLeaves(child))
}

function newTabPane(): Pane {
  return { id: generateId(), content: { kind: 'new-tab' } }
}

export function swapPaneContent(layout: PaneLayout, paneIdA: string, paneIdB: string): PaneLayout {
  const paneA = findPane(layout, paneIdA)
  const paneB = findPane(layout, paneIdB)
  if (!paneA || !paneB) return layout
  const contentA = paneA.content
  const contentB = paneB.content
  let result = updatePaneInLayout(layout, paneIdA, contentB)
  result = updatePaneInLayout(result, paneIdB, contentA)
  return result
}

/**
 * The pattern `layout` already has (shell cleanup spec §10): a leaf → `single`; a split of exactly two **leaf**
 * children → `split-h` / `split-v` by its direction; any other shape → null (no title-bar button is pressed).
 */
export function currentLayoutPattern(layout: PaneLayout): LayoutPattern | null {
  if (layout.type === 'leaf') return 'single'
  if (layout.children.length !== 2 || layout.children.some((c) => c.type !== 'leaf')) return null
  return layout.direction === 'h' ? 'split-h' : 'split-v'
}

/**
 * Rebuild `layout` as `pattern` (1 slot for `single`, 2 for a split).
 *
 * Without `keepIds` the first leaves in layout order fill the slots. With `keepIds` those panes are the exact
 * survivor set (rule D.1a, worked out by `planLayoutChange`): every other leaf is dropped, the survivors keep their
 * layout order whatever order `keepIds` lists them in, and ids that are not in the layout are ignored. In both forms
 * a slot left over is filled with a fresh `new-tab` pane.
 */
export function applyLayoutPattern(layout: PaneLayout, pattern: LayoutPattern, keepIds?: readonly string[]): PaneLayout {
  const all = collectLeaves(layout)
  const keep = keepIds ? new Set(keepIds) : null
  const leaves = keep ? all.filter((p) => keep.has(p.id)) : all
  const p = (i: number): Pane => leaves[i] ?? newTabPane()
  switch (pattern) {
    case 'single': return { type: 'leaf', pane: p(0) }
    case 'split-h': return { type: 'split', id: generateId(), direction: 'h', children: [{ type: 'leaf', pane: p(0) }, { type: 'leaf', pane: p(1) }], sizes: [50, 50] }
    case 'split-v': return { type: 'split', id: generateId(), direction: 'v', children: [{ type: 'leaf', pane: p(0) }, { type: 'leaf', pane: p(1) }], sizes: [50, 50] }
  }
}
