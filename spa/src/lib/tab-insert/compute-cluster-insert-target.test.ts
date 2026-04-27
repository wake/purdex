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

  it('returns undefined when there is no active tab and no workspace context', () => {
    expect(computeClusterInsertTarget(null, isFileKind)).toBeUndefined()
  })

  it('uses ws.activeTabId (workspace-scoped anchor) for clustering', () => {
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

  it('falls back to anchor when no same-kind tab to the right of anchor', () => {
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
    // findInsertTarget falls back to anchor when no match — so the
    // helper returns anchor (insert after current), which is the
    // desired "append after current" behaviour, not undefined.
    expect(computeClusterInsertTarget('w', isFileKind)).toBe(s1.id)
  })

  it('falls back to global tabOrder + global activeTabId when workspace not found', () => {
    const s1 = createTab(sessionContent('s1'))
    const editor1 = createTab(editorContent('/x.ts'))
    useTabStore.setState({
      tabs: { [s1.id]: s1, [editor1.id]: editor1 },
      tabOrder: [s1.id, editor1.id],
      activeTabId: s1.id,
    })
    expect(computeClusterInsertTarget('w-missing', isFileKind)).toBe(editor1.id)
  })

  // Regression: codex round-2 attacker / defender / file-quality all flagged
  // the cross-workspace race when global activeTabId leaks into cluster
  // insertion within a different workspace. Anchor MUST be ws.activeTabId
  // so async terminal-link opens that race a workspace switch don't
  // produce a diverged tabOrder vs workspace.tabs.
  it('ignores global activeTabId when targeting a workspace where global active does not live', () => {
    const wsSourceTab = createTab(sessionContent('s-source'))
    const wsSourceEditor = createTab(editorContent('/source.ts'))
    const wsOtherTab = createTab(sessionContent('s-other'))
    useTabStore.setState({
      tabs: {
        [wsSourceTab.id]: wsSourceTab,
        [wsSourceEditor.id]: wsSourceEditor,
        [wsOtherTab.id]: wsOtherTab,
      },
      tabOrder: [wsSourceTab.id, wsSourceEditor.id, wsOtherTab.id],
      activeTabId: wsOtherTab.id, // user switched mid-await — global active jumped
    })
    useWorkspaceStore.setState({
      workspaces: [
        {
          id: 'ws-source', name: 'Source',
          tabs: [wsSourceTab.id, wsSourceEditor.id], activeTabId: wsSourceTab.id, moduleConfig: {},
        },
        {
          id: 'ws-other', name: 'Other',
          tabs: [wsOtherTab.id], activeTabId: wsOtherTab.id, moduleConfig: {},
        },
      ],
      activeWorkspaceId: 'ws-other',
    })

    // Even though global activeTabId is in ws-other, we are inserting
    // into ws-source. Anchor must be ws-source.activeTabId (= wsSourceTab),
    // and the answer is wsSourceEditor (the next file-kind tab to the
    // right within ws-source).
    expect(computeClusterInsertTarget('ws-source', isFileKind)).toBe(wsSourceEditor.id)
  })

  it('returns undefined when ws.activeTabId is not in ws.tabs (defensive bail)', () => {
    const s1 = createTab(sessionContent('s1'))
    useTabStore.setState({
      tabs: { [s1.id]: s1 },
      tabOrder: [s1.id],
      activeTabId: s1.id,
    })
    useWorkspaceStore.setState({
      workspaces: [{
        id: 'w', name: 'W', tabs: [s1.id], activeTabId: 'stale-tab-id', moduleConfig: {},
      }],
      activeWorkspaceId: 'w',
    })
    expect(computeClusterInsertTarget('w', isFileKind)).toBeUndefined()
  })

  it('returns undefined when ws.activeTabId is null', () => {
    useWorkspaceStore.setState({
      workspaces: [{
        id: 'w', name: 'W', tabs: [], activeTabId: null, moduleConfig: {},
      }],
      activeWorkspaceId: 'w',
    })
    expect(computeClusterInsertTarget('w', isFileKind)).toBeUndefined()
  })
})
