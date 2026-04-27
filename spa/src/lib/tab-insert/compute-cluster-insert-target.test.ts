import { describe, it, expect, beforeEach } from 'vitest'
import { computeClusterInsertTarget } from './compute-cluster-insert-target'
import { useTabStore } from '../../stores/useTabStore'
import { useWorkspaceStore } from '../../stores/useWorkspaceStore'
import { createTab } from '../../types/tab'
import type { PaneContent } from '../../types/tab'

const editorContent = (path: string): PaneContent =>
  ({ kind: 'editor', source: { type: 'inapp' }, filePath: path } as never)

const sessionContent = (code: string): PaneContent =>
  ({ kind: 'tmux-session', hostId: 'h', sessionCode: code, mode: 'terminal', cachedName: '', tmuxInstance: '' } as never)

const isFileKind = (c: PaneContent) =>
  c.kind === 'editor' || c.kind === 'image-preview' || c.kind === 'pdf-preview'

describe('computeClusterInsertTarget', () => {
  beforeEach(() => {
    useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null })
    useWorkspaceStore.setState({ workspaces: [], activeWorkspaceId: null })
  })

  it('returns undefined when there is no active tab', () => {
    expect(computeClusterInsertTarget(null, isFileKind)).toBeUndefined()
  })

  it('uses workspace.tabs (not global tabOrder) so clustering matches the TabBar render', () => {
    // Global tabOrder has the editor first, but the workspace shows
    // [s1, editor1, s2]. The cluster target must come from the
    // workspace-visible order, not tabOrder.
    const s1 = createTab(sessionContent('s1'))
    const editor1 = createTab(editorContent('/x.ts'))
    const s2 = createTab(sessionContent('s2'))
    useTabStore.setState({
      tabs: { [s1.id]: s1, [editor1.id]: editor1, [s2.id]: s2 },
      tabOrder: [editor1.id, s1.id, s2.id], // shuffled global
      activeTabId: s1.id,
    })
    useWorkspaceStore.setState({
      workspaces: [{
        id: 'w', name: 'W', tabs: [s1.id, editor1.id, s2.id], activeTabId: s1.id, moduleConfig: {},
      }],
      activeWorkspaceId: 'w',
    })

    expect(computeClusterInsertTarget('w', isFileKind)).toBe(editor1.id)
  })

  it('returns undefined when no same-kind tab to the right of active', () => {
    const s1 = createTab(sessionContent('s1'))
    const s2 = createTab(sessionContent('s2'))
    useTabStore.setState({
      tabs: { [s1.id]: s1, [s2.id]: s2 },
      tabOrder: [s1.id, s2.id],
      activeTabId: s1.id,
    })
    useWorkspaceStore.setState({
      workspaces: [{
        id: 'w', name: 'W', tabs: [s1.id, s2.id], activeTabId: s1.id, moduleConfig: {},
      }],
      activeWorkspaceId: 'w',
    })

    // findInsertTarget falls back to activeTabId when no match — so
    // the helper returns activeTabId (insert after active), which is
    // the desired "append after current" behaviour, not undefined.
    expect(computeClusterInsertTarget('w', isFileKind)).toBe(s1.id)
  })

  it('falls back to global tabOrder when workspace not found', () => {
    const s1 = createTab(sessionContent('s1'))
    const editor1 = createTab(editorContent('/x.ts'))
    useTabStore.setState({
      tabs: { [s1.id]: s1, [editor1.id]: editor1 },
      tabOrder: [s1.id, editor1.id],
      activeTabId: s1.id,
    })
    // No workspaces seeded
    expect(computeClusterInsertTarget('w-missing', isFileKind)).toBe(editor1.id)
  })
})
