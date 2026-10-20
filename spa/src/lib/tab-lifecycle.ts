import { useTabStore } from '../stores/useTabStore'
import { useEditorStore } from '../stores/useEditorStore'
import { useI18nStore } from '../stores/useI18nStore'
import { useWorkspaceStore } from '../features/workspace/store'
import { WATCH_WORKSPACE_ID } from './watch/ids'
import { selectedGroupRange } from './task-groups/range'
import { destroyBrowserViewIfNeeded } from './browser-cleanup'
import { scanPaneTree } from './pane-tree'
import { bufferKey } from './editor-buffer-key'

/**
 * In the task-group view the tab shown after a close comes from the selected group's range (task groups spec §6.2), whichever control closed it (every close goes through here); the watcher area keeps the workspace rule.
 * `opts.scope` overrides the range (a caller that already knows it).
 */
export function closeTab(tabId: string, opts?: { skipHistory?: boolean; scope?: readonly string[] }): void {
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
  const scope = opts?.scope ?? (useWorkspaceStore.getState().activeWorkspaceId === WATCH_WORKSPACE_ID ? null : selectedGroupRange()) ?? undefined
  useWorkspaceStore.getState().closeTabInWorkspace(tabId, scope === undefined ? opts : { ...opts, scope })
}
