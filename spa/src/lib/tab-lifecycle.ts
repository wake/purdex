import { useTabStore } from '../stores/useTabStore'
import { useEditorStore } from '../stores/useEditorStore'
import { useI18nStore } from '../stores/useI18nStore'
import { useWorkspaceStore } from '../features/workspace/store'
import { destroyBrowserViewIfNeeded } from './browser-cleanup'
import { scanPaneTree } from './pane-tree'
import { bufferKey } from './editor-buffer-key'

export function closeTab(tabId: string, opts?: { skipHistory?: boolean }): void {
  const tab = useTabStore.getState().tabs[tabId]
  if (!tab || tab.locked) return

  let dirty = false
  scanPaneTree(tab.layout, (pane) => {
    if (dirty) return
    const c = pane.content
    if (c.kind !== 'editor') return
    if (useEditorStore.getState().buffers[bufferKey(c.source, c.filePath)]?.isDirty) dirty = true
  })
  if (dirty && !window.confirm(useI18nStore.getState().t('editor.close_dirty_confirm'))) return

  destroyBrowserViewIfNeeded(tab)
  useWorkspaceStore.getState().closeTabInWorkspace(tabId, opts)
}
