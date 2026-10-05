import type { ExecutionContent, ExecutionViewMode } from '../../types/tab'
import { findPane } from '../pane-tree'
import { useTabStore } from '../../stores/useTabStore'

/**
 * The view an execution pane shows its worker in. Only `'chat'` is chat:
 * an absent mode, or one this build does not know (written by an older or
 * newer client), reads as room.
 */
export function viewModeOf(content: ExecutionContent): ExecutionViewMode {
  return content.mode === 'chat' ? 'chat' : 'room'
}

/** The same content showing `mode`; every other field (`from`, `host`, …) is kept. */
export function withViewMode(content: ExecutionContent, mode: ExecutionViewMode): ExecutionContent {
  return { ...content, mode }
}

/**
 * Switches the pane's view to `mode` (shell cleanup T6.5: the pane's view
 * menu and the status bar's mode buttons both write through here). It reads
 * the content the store holds *now*, not a render's, so a `from` or host
 * rewrite that landed since is kept; a pane that is gone or no longer shows
 * `executionId` is left alone.
 */
export function setExecutionPaneMode(tabId: string, paneId: string, executionId: string, mode: ExecutionViewMode): void {
  const store = useTabStore.getState()
  const tab = store.tabs[tabId]
  const current = tab ? findPane(tab.layout, paneId)?.content : undefined
  if (current?.kind !== 'execution' || current.executionId !== executionId) return
  store.setPaneContent(tabId, paneId, withViewMode(current, mode))
}
