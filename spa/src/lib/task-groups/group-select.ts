// spa/src/lib/task-groups/group-select.ts — selecting a group from the sidebar, and switching between the two sidebar views (task groups spec §6.2, §6.3). Plain functions over the stores, for the header click, the view toggle and
// anything else that is not a render.
//   - Selecting a group activates its last active tab if that tab still belongs to it, else the first tab of `groupTabs`; an EMPTY group sets the active tab to none (and shows the empty state, as an empty workspace does).
//   - Switching views restores the view's last selection and its last tab; if that is gone, the view selects the workspace / group of the current tab.
import { useTabStore } from '../../stores/useTabStore'
import { useGroupViewStore } from '../../stores/useGroupViewStore'
import { useTeamUiStore, type SidebarView } from '../../stores/useTeamUiStore'
import { useWorkspaceStore } from '../../features/workspace/store'
import { currentGroupOrderCtx } from './current'
import { groupTabs, type GroupOrderCtx } from './group-order'
import { parseGroupKey } from './group-key'
import { resyncSelection } from './selection'

/** Whether a group key names a group that exists now (「未分組」 always does). */
export function groupExists(key: string, ctx: Pick<GroupOrderCtx, 'index' | 'manual'>): boolean {
  const parsed = parseGroupKey(key)
  if (parsed === null) return false
  if (parsed.kind === 'ungrouped') return true
  return parsed.kind === 'team' ? ctx.index.byKey.has(parsed.teamKey) : ctx.manual.groups[parsed.id] !== undefined
}

/** The tab selecting `key` activates: its last active tab while that still belongs to it, else its first tab; null when the group is empty. */
export function targetTabOfGroup(key: string, ctx: GroupOrderCtx): string | null {
  const ids = groupTabs(key, ctx)
  const last = useGroupViewStore.getState().lastActiveTab[key]
  return last !== undefined && ids.includes(last) ? last : ids[0] ?? null
}

/** Makes `tabId` the active tab and its workspace the active workspace (what a click on a row does in the workspace view). */
export function activateTab(tabId: string): void {
  useTabStore.getState().setActiveTab(tabId)
  const ws = useWorkspaceStore.getState().findWorkspaceByTab(tabId)
  if (ws) {
    useWorkspaceStore.getState().setActiveWorkspace(ws.id)
    useWorkspaceStore.getState().setWorkspaceActiveTab(ws.id, tabId)
  }
}

/** The sidebar selected `key` (a header click). */
export function selectGroupFromSidebar(key: string): void {
  const ctx = currentGroupOrderCtx()
  if (!groupExists(key, ctx)) return
  const target = targetTabOfGroup(key, ctx)
  useGroupViewStore.getState().selectGroup(key) // an empty group is selected too
  if (target !== null) activateTab(target)
  else useTabStore.getState().setActiveTab(null)
}

/** Switches the sidebar view (the toggle in the Home row) and restores that view's last place. */
export function switchSidebarView(to: SidebarView): void {
  useTeamUiStore.getState().setSidebarView(to)
  const { lastByView } = useGroupViewStore.getState()
  if (to === 'groups') {
    const key = lastByView.group
    if (key !== null && groupExists(key, currentGroupOrderCtx())) selectGroupFromSidebar(key)
    else resyncSelection() // gone: the group of the current tab
    return
  }
  const ws = useWorkspaceStore.getState().workspaces.find((w) => w.id === lastByView.workspace)
  if (ws === undefined) {
    // gone: the workspace of the current tab becomes the active one (the sidebar highlights it) and is recorded
    const current = useTabStore.getState().activeTabId
    const home = current === null ? null : useWorkspaceStore.getState().findWorkspaceByTab(current)
    if (home) useWorkspaceStore.getState().setActiveWorkspace(home.id)
    resyncSelection()
    return
  }
  useWorkspaceStore.getState().setActiveWorkspace(ws.id)
  const tabs = useTabStore.getState().tabs
  const tabId = ws.activeTabId !== null && tabs[ws.activeTabId] !== undefined ? ws.activeTabId : ws.tabs.find((id) => tabs[id] !== undefined) ?? null
  useTabStore.getState().setActiveTab(tabId)
}
