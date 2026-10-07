import { useTabStore } from '../stores/useTabStore'
import { useEditorStore } from '../stores/useEditorStore'
import { useI18nStore } from '../stores/useI18nStore'
import { useWorkspaceStore } from '../features/workspace/store'
import { destroyBrowserViewIfNeeded } from './browser-cleanup'
import { scanPaneTree } from './pane-tree'
import { bufferKey } from './editor-buffer-key'

/**
 * The gates {@link closeTab} applies before it removes anything, on their own: the tab must exist and be
 * unlocked, and a tab holding unsaved editor changes needs the user's OK (this may prompt). A caller that has
 * to commit to something else before the tab goes — tear-off sends it to a new window first (#1816) — asks
 * here, then closes with `closeTab(id, { confirmed: true })` so the user is not asked twice.
 */
export function confirmCloseTab(tabId: string): boolean {
  const tab = useTabStore.getState().tabs[tabId]
  if (!tab || tab.locked) return false

  let dirty = false
  scanPaneTree(tab.layout, (pane) => {
    if (dirty) return
    const c = pane.content
    if (c.kind !== 'editor') return
    if (useEditorStore.getState().buffers[bufferKey(c.source, c.filePath)]?.isDirty) dirty = true
  })
  return !dirty || window.confirm(useI18nStore.getState().t('editor.close_dirty_confirm'))
}

/** `confirmed`: the caller already passed {@link confirmCloseTab} for this tab — the gates are not asked again. */
export function closeTab(tabId: string, opts?: { skipHistory?: boolean; confirmed?: boolean }): void {
  if (!opts?.confirmed && !confirmCloseTab(tabId)) return
  const tab = useTabStore.getState().tabs[tabId]
  if (!tab) return

  destroyBrowserViewIfNeeded(tab)
  useWorkspaceStore.getState().closeTabInWorkspace(tabId, opts)
}
