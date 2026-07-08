import { useTabStore } from '../stores/useTabStore'
import { useWorkspaceStore } from '../features/workspace/store'
import { countLeaves, getPrimaryPane } from './pane-tree'

/**
 * Pane-content kinds that may be pulled out of a tab and dropped into another
 * tab's pane. These are "portable" content: their real state lives outside the
 * pane descriptor (editor bytes in `useEditorStore`, tmux sessions referenced
 * by host + code), so moving the descriptor never destroys anything. Chrome
 * pages (`browser`) and singleton views (`dashboard`, `hosts`, …) are excluded.
 */
export const MOVABLE_KINDS = ['editor', 'tmux-session', 'image-preview', 'pdf-preview'] as const

/**
 * Move a single-pane source tab's content into an existing pane of another tab
 * (the "pull tab into pane" primitive for cross-workspace drag).
 *
 * This is a MOVE, not a destroy: the source content descriptor is injected into
 * the target pane and the source tab is retired via
 * `useWorkspaceStore.closeTabInWorkspace(..., { skipHistory: true })`, which is
 * the single source of truth for workspace-membership removal, next-tab budget,
 * and active-tab fallback (both global and workspace scoped). We deliberately do
 * NOT route through `tab-lifecycle::closeTab` — that path has destroy semantics
 * (dirty-confirm prompt + BrowserView teardown) which would be a false alarm
 * here because the content is being relocated, not thrown away.
 *
 * @returns `true` when the move was performed, `false` (no-op) when any guard
 *   fails.
 */
export function moveTabContentIntoPane(
  sourceTabId: string,
  targetTabId: string,
  targetPaneId: string,
): boolean {
  if (sourceTabId === targetTabId) return false

  const tabStore = useTabStore.getState()
  const source = tabStore.tabs[sourceTabId]
  if (!source) return false
  if (source.locked) return false
  if (countLeaves(source.layout) !== 1) return false

  const sourceContent = getPrimaryPane(source.layout).content
  if (!(MOVABLE_KINDS as readonly string[]).includes(sourceContent.kind)) return false

  // 1. Inject source content into the target pane.
  tabStore.setPaneContent(targetTabId, targetPaneId, sourceContent)
  // 2. Retire the source tab as a move (workspace-aware, no dirty-confirm).
  useWorkspaceStore.getState().closeTabInWorkspace(sourceTabId, { skipHistory: true })

  return true
}
