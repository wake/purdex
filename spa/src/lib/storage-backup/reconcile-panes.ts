/**
 * reconcile-panes — bring open In-App panes back in line with the tree after a
 * restore (Phase 2c, R3-Pb / R4-P2). Restore refuses while any inapp buffer is
 * dirty/locked (restore-guard), so every open inapp pane here is clean: we may
 * close / reload / remount it without losing edits.
 *
 * `planReconciliation` is pure (diff + a tab snapshot → an action list) so it is
 * unit-testable headlessly; `applyReconciliation` executes the actions through
 * injected store/backend operations (the live wiring supplies the real stores).
 *
 * Path mapping: the restore `changed` diff carries ROOT-RELATIVE manifest paths
 * (`a.txt`, `sub/b.md`); pane `filePath`s are FULL paths (`/buffer/a.txt`). We
 * compare via `relativeToRoot(filePath)`. `added` never applies to an
 * already-open pane (its path existed in the pre-restore tree), so only
 * `removed` + `modified` drive actions.
 */
import type { Tab, PaneContent } from '../../types/tab'
import type { FileSource } from '../../types/fs'
import { scanPaneTree } from '../pane-tree'
import { relativeToRoot } from '../storage-paths'
import { bufferKey } from '../editor-buffer-key'
import type { RestoreChange } from './restore'

/** One reconciliation step against a specific open pane. */
export type PaneAction =
  | { kind: 'close-editor'; tabId: string; paneId: string; source: FileSource; filePath: string }
  | { kind: 'reload-editor'; tabId: string; paneId: string; source: FileSource; filePath: string }
  | { kind: 'close-preview'; tabId: string; paneId: string }
  | { kind: 'remount-preview'; tabId: string; paneId: string }

/** True for an inapp file-bearing pane content (editor / image / pdf). */
function inappFilePath(content: PaneContent): string | null {
  if (content.kind !== 'editor' && content.kind !== 'image-preview' && content.kind !== 'pdf-preview') {
    return null
  }
  return content.source.type === 'inapp' ? content.filePath : null
}

/**
 * Diff every open inapp pane (across ALL tabs, every split leaf) against the
 * restore `changed` set and return the actions needed to reconcile them.
 */
export function planReconciliation(
  changed: RestoreChange,
  tabs: Record<string, Tab>,
  restoredFiles: Iterable<string>,
): PaneAction[] {
  const removed = new Set(changed.removed)
  const modified = new Set(changed.modified)
  const fileRels = new Set(restoredFiles)
  const actions: PaneAction[] = []

  for (const tab of Object.values(tabs)) {
    scanPaneTree(tab.layout, (pane) => {
      const content = pane.content
      const filePath = inappFilePath(content)
      if (filePath === null) return
      const rel = relativeToRoot(filePath)
      const isRemoved = removed.has(rel)
      const isModified = modified.has(rel)
      if (!isRemoved && !isModified) return

      // A `modified` path whose restored entry is no longer a FILE (file→dir
      // transition, codex 2c-2 R1) is, for an open file pane, equivalent to a
      // removal — reloading/ remounting it would read a directory. Close it.
      const gone = isRemoved || !fileRels.has(rel)

      if (content.kind === 'editor') {
        actions.push(
          gone
            ? { kind: 'close-editor', tabId: tab.id, paneId: pane.id, source: content.source, filePath }
            : { kind: 'reload-editor', tabId: tab.id, paneId: pane.id, source: content.source, filePath },
        )
      } else {
        // image-preview / pdf-preview
        actions.push(
          gone
            ? { kind: 'close-preview', tabId: tab.id, paneId: pane.id }
            : { kind: 'remount-preview', tabId: tab.id, paneId: pane.id },
        )
      }
    })
  }
  return actions
}

/** Injected operations so the apply step stays decoupled from the live stores. */
export interface ReconcileDeps {
  getTabs: () => Record<string, Tab>
  /** Read a restored file's bytes back as text + stat (full path). */
  readFile: (fullPath: string) => Promise<{ content: string; stat: { mtime: number; size: number } }>
  /** Close a pane in the tab store (mirrors storage-actions G2 ordering). */
  closeTabPane: (tabId: string, paneId: string) => void
  /** Close the matching editor buffer/pane state. */
  closeEditorPane: (paneId: string, expectedBufferKey: string) => void
  /** Re-align a clean buffer to the restored bytes (content/saved/dirty/stat). */
  reloadBuffer: (bufferKey: string, content: string, stat: { mtime: number; size: number }) => void
  /** Remount a leaf (new pane id) to force a preview re-read. */
  remountPane: (tabId: string, paneId: string) => string | null
}

/** Outcome of a best-effort reconciliation: which actions (if any) failed. */
export interface ReconcileResult {
  failed: Array<{ action: PaneAction; error: string }>
}

/**
 * Execute the reconciliation plan. Editor closes follow the close-pane-before
 * (storage-actions §531, G2): tab pane first, then the editor buffer/pane state.
 *
 * **Best-effort, never throws** (codex 2c-2 R2 H2/H3): this runs AFTER restore
 * has already committed `replaceTree`, so a failure here is NOT a restore failure
 * and must not be reported as a rollback. Each action is isolated — one failing
 * `readFile` does not abort the rest, leaving the UI in a mixed state. Failures
 * are collected and returned so the caller can log / surface them as non-fatal.
 */
export async function applyReconciliation(
  changed: RestoreChange,
  restoredFiles: Iterable<string>,
  deps: ReconcileDeps,
): Promise<ReconcileResult> {
  const actions = planReconciliation(changed, deps.getTabs(), restoredFiles)
  const failed: ReconcileResult['failed'] = []
  for (const action of actions) {
    try {
      switch (action.kind) {
        case 'close-editor':
          deps.closeTabPane(action.tabId, action.paneId)
          deps.closeEditorPane(action.paneId, bufferKey(action.source, action.filePath))
          break
        case 'reload-editor': {
          const { content, stat } = await deps.readFile(action.filePath)
          deps.reloadBuffer(bufferKey(action.source, action.filePath), content, stat)
          break
        }
        case 'close-preview':
          deps.closeTabPane(action.tabId, action.paneId)
          break
        case 'remount-preview':
          deps.remountPane(action.tabId, action.paneId)
          break
      }
    } catch (e) {
      failed.push({ action, error: e instanceof Error ? e.message : String(e) })
    }
  }
  return { failed }
}
