/**
 * restore-guard — the pre-flight conflict check that hard-blocks a restore while
 * un-flushed In-App editor state exists (spec §4.4 step 1, R2-Pg).
 *
 * Un-flushed editor content lives in Zustand (`useEditorStore`), NOT IndexedDB,
 * so a safety snapshot could not capture it — therefore restore refuses (no
 * "continue anyway", C1) when:
 *   - any `inapp` editor buffer is dirty (`isDirty`), or
 *   - any `locked` tab has a pane that is an `inapp` editor
 * — the same surface `tab-lifecycle.ts` scans on tab close.
 *
 * Pure: callers inject snapshots of the tab + editor-buffer state, so the
 * orchestrator (and 2c-2 UI wiring) can supply live store state while tests
 * supply fixtures — no store/React import here.
 */
import type { Tab } from '../../types/tab'
import { scanPaneTree } from '../pane-tree'
import { bufferKey } from '../editor-buffer-key'

/** One reason a restore is blocked, tied to the offending tab + file. */
export interface RestoreConflict {
  /** `dirty` = unsaved inapp buffer; `locked` = inapp editor inside a locked tab. */
  type: 'dirty' | 'locked'
  tabId: string
  filePath: string
}

/** Minimal slice of an editor buffer the guard needs. */
interface BufferDirtyState {
  isDirty: boolean
}

/**
 * Return every conflict that blocks a restore. An empty array ⇒ restore may
 * proceed. A locked tab with a dirty inapp pane yields BOTH a `locked` and a
 * `dirty` conflict so the UI can surface each reason.
 */
export function findRestoreConflicts(deps: {
  tabs: Record<string, Tab>
  buffers: Record<string, BufferDirtyState | undefined>
}): RestoreConflict[] {
  const conflicts: RestoreConflict[] = []
  for (const tab of Object.values(deps.tabs)) {
    scanPaneTree(tab.layout, (pane) => {
      const c = pane.content
      if (c.kind !== 'editor') return
      if (c.source.type !== 'inapp') return
      const filePath = c.filePath
      if (deps.buffers[bufferKey(c.source, filePath)]?.isDirty) {
        conflicts.push({ type: 'dirty', tabId: tab.id, filePath })
      }
      if (tab.locked) {
        conflicts.push({ type: 'locked', tabId: tab.id, filePath })
      }
    })
  }
  return conflicts
}
