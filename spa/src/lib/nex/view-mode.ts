import type { ExecutionContent, ExecutionViewMode } from '../../types/tab'
import { findPane } from '../pane-tree'
import { useTabStore } from '../../stores/useTabStore'
import { resolveExecutionHostId } from './resolve-host'

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

/** The worker a mode switch is meant for: the execution store's key (host + execution id). */
export interface ExecutionPaneTarget {
  executionId: string
  /** The resolved host id the caller's view is on (`resolveExecutionHostId(content.host)`, as the pane keys its view). */
  host: string
}

/**
 * Switches the pane's view to `mode` (shell cleanup T6.5: the pane's view
 * menu and the status bar's mode buttons both write through here). It reads
 * the content the store holds *now*, not a render's, so a `from` rewrite that
 * landed since is kept; a pane that is gone or no longer shows `expected` is
 * left alone. "Shows" compares the host too (P6 review A2): the execution
 * store keys by host + execution id, so the same id on another host is
 * another worker. The pane's host resolves as the pane resolves it (an
 * absent hint → the first host).
 */
export function setExecutionPaneMode(tabId: string, paneId: string, expected: ExecutionPaneTarget, mode: ExecutionViewMode): void {
  const store = useTabStore.getState()
  const tab = store.tabs[tabId]
  const current = tab ? findPane(tab.layout, paneId)?.content : undefined
  if (current?.kind !== 'execution' || current.executionId !== expected.executionId) return
  if (resolveExecutionHostId(current.host) !== expected.host) return
  store.setPaneContent(tabId, paneId, withViewMode(current, mode))
}
