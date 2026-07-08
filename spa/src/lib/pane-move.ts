import { useTabStore } from '../stores/useTabStore'
import { useWorkspaceStore } from '../features/workspace/store'
import { useEditorStore } from '../stores/useEditorStore'
import { countLeaves, getPrimaryPane } from './pane-tree'
import { bufferKey } from './editor-buffer-key'

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

  // 1. For editor content, pre-bind the target pane to the buffer BEFORE we
  //    mutate the tab tree. A dirty editor buffer is ref-counted by the
  //    paneStates referencing its key; the source EditorPane's unmount cleanup
  //    (closePane) destroys the buffer once no paneState references it. That
  //    cleanup runs synchronously in the same React commit as this move, BEFORE
  //    the target EditorPane mounts and registers its own reference — so the
  //    ref-count would momentarily hit zero and the unsaved edits would be lost.
  //    Attaching the target here keeps the count > 0 across the unmount; the
  //    target EditorPane's own attachPane on mount then no-ops (same key).
  if (sourceContent.kind === 'editor') {
    const key = bufferKey(sourceContent.source, sourceContent.filePath)
    useEditorStore.getState().attachPane(targetPaneId, key)
  }
  // 2. Inject source content into the target pane.
  tabStore.setPaneContent(targetTabId, targetPaneId, sourceContent)
  // 3. Retire the source tab as a move (workspace-aware, no dirty-confirm).
  useWorkspaceStore.getState().closeTabInWorkspace(sourceTabId, { skipHistory: true })

  return true
}
