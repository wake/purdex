import { describe, it, expect, beforeEach } from 'vitest'
import { planReconciliation, applyReconciliation, type PaneAction } from './reconcile-panes'
import type { Tab, PaneContent } from '../../types/tab'
import type { FileSource } from '../../types/fs'
import type { RestoreChange } from './restore'
import { useTabStore } from '../../stores/useTabStore'
import { useEditorStore } from '../../stores/useEditorStore'
import { bufferKey } from '../editor-buffer-key'
import { findPane } from '../pane-tree'

const inapp: FileSource = { type: 'inapp' }
const daemon: FileSource = { type: 'daemon', hostId: 'h1' }

let seq = 0
function leafTab(content: PaneContent): Tab {
  seq += 1
  return {
    id: `tab-${seq}`,
    pinned: false,
    locked: false,
    createdAt: 0,
    layout: { type: 'leaf', pane: { id: `pane-${seq}`, content } },
  }
}

function editor(source: FileSource, filePath: string): PaneContent {
  return { kind: 'editor', source, filePath }
}

const empty: RestoreChange = { added: [], removed: [], modified: [] }

describe('planReconciliation', () => {
  it('closes an inapp editor whose path was removed', () => {
    const tab = leafTab(editor(inapp, '/buffer/a.md'))
    const actions = planReconciliation({ ...empty, removed: ['a.md'] }, { [tab.id]: tab }, [])
    expect(actions).toContainEqual<PaneAction>({
      kind: 'close-editor',
      tabId: tab.id,
      paneId: 'pane-' + seq,
      source: inapp,
      filePath: '/buffer/a.md',
    })
  })

  it('reloads an inapp editor whose content changed', () => {
    const tab = leafTab(editor(inapp, '/buffer/a.md'))
    const actions = planReconciliation({ ...empty, modified: ['a.md'] }, { [tab.id]: tab }, ['a.md'])
    expect(actions).toContainEqual<PaneAction>({
      kind: 'reload-editor',
      tabId: tab.id,
      paneId: 'pane-' + seq,
      source: inapp,
      filePath: '/buffer/a.md',
    })
  })

  it('closes an inapp image-preview whose path was removed and remounts a changed one', () => {
    const removedTab = leafTab({ kind: 'image-preview', source: inapp, filePath: '/buffer/x.png' })
    const removedId = 'pane-' + seq
    const changedTab = leafTab({ kind: 'pdf-preview', source: inapp, filePath: '/buffer/y.pdf' })
    const changedId = 'pane-' + seq
    const actions = planReconciliation(
      { added: [], removed: ['x.png'], modified: ['y.pdf'] },
      { [removedTab.id]: removedTab, [changedTab.id]: changedTab },
      ['y.pdf'],
    )
    expect(actions).toContainEqual<PaneAction>({ kind: 'close-preview', tabId: removedTab.id, paneId: removedId })
    expect(actions).toContainEqual<PaneAction>({ kind: 'remount-preview', tabId: changedTab.id, paneId: changedId })
  })

  it('ignores non-inapp panes and unchanged inapp panes', () => {
    const daemonTab = leafTab(editor(daemon, '/x/a.md'))
    const cleanTab = leafTab(editor(inapp, '/buffer/keep.md'))
    const actions = planReconciliation(
      { added: ['new.md'], removed: ['a.md'], modified: ['a.md'] },
      { [daemonTab.id]: daemonTab, [cleanTab.id]: cleanTab },
      ['new.md', 'keep.md'],
    )
    expect(actions).toEqual([])
  })

  it('enumerates panes across all tabs and both panes of a split', () => {
    const tab: Tab = {
      id: 'split-tab',
      pinned: false,
      locked: false,
      createdAt: 0,
      layout: {
        type: 'split',
        id: 's1',
        direction: 'h',
        children: [
          { type: 'leaf', pane: { id: 'L', content: editor(inapp, '/buffer/a.md') } },
          { type: 'leaf', pane: { id: 'R', content: { kind: 'image-preview', source: inapp, filePath: '/buffer/b.png' } } },
        ],
        sizes: [50, 50],
      },
    }
    const actions = planReconciliation(
      { added: [], removed: [], modified: ['a.md', 'b.png'] },
      { [tab.id]: tab },
      ['a.md', 'b.png'],
    )
    expect(actions).toContainEqual<PaneAction>({
      kind: 'reload-editor', tabId: 'split-tab', paneId: 'L', source: inapp, filePath: '/buffer/a.md',
    })
    expect(actions).toContainEqual<PaneAction>({ kind: 'remount-preview', tabId: 'split-tab', paneId: 'R' })
  })

  it('closes an open file pane when a modified path became a directory (file→dir, codex 2c-2 R1)', () => {
    const edTab = leafTab(editor(inapp, '/buffer/note.md'))
    const edId = 'pane-' + seq
    const pvTab = leafTab({ kind: 'image-preview', source: inapp, filePath: '/buffer/pic.png' })
    const pvId = 'pane-' + seq
    // Both paths are `modified` but NEITHER is a file in the restored tree
    // (they became directories) — restoredFiles is empty.
    const actions = planReconciliation(
      { added: [], removed: [], modified: ['note.md', 'pic.png'] },
      { [edTab.id]: edTab, [pvTab.id]: pvTab },
      [],
    )
    expect(actions).toContainEqual<PaneAction>({
      kind: 'close-editor', tabId: edTab.id, paneId: edId, source: inapp, filePath: '/buffer/note.md',
    })
    expect(actions).toContainEqual<PaneAction>({ kind: 'close-preview', tabId: pvTab.id, paneId: pvId })
  })
})

/** Live deps over the real tab + editor stores, with an in-memory readFile. */
function liveDeps(files: Record<string, { content: string; stat: { mtime: number; size: number } }>) {
  return {
    getTabs: () => useTabStore.getState().tabs,
    readFile: async (fullPath: string) => {
      const f = files[fullPath]
      if (!f) throw new Error(`no file at ${fullPath}`)
      return f
    },
    closeTabPane: (tabId: string, paneId: string) => useTabStore.getState().closePane(tabId, paneId),
    closeEditorPane: (paneId: string, key: string) => useEditorStore.getState().closePane(paneId, key),
    reloadBuffer: (key: string, content: string, stat: { mtime: number; size: number }) =>
      useEditorStore.getState().reloadBuffer(key, content, stat),
    remountPane: (tabId: string, paneId: string) => useTabStore.getState().remountPane(tabId, paneId),
  }
}

describe('applyReconciliation', () => {
  beforeEach(() => {
    useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null })
    useEditorStore.setState({ buffers: {}, paneStates: {} })
  })

  it('closes an inapp editor pane whose path was removed (tab + buffer)', async () => {
    const content = editor(inapp, '/buffer/gone.md')
    const tab = leafTab(content)
    const paneId = tab.layout.type === 'leaf' ? tab.layout.pane.id : ''
    const key = bufferKey(inapp, '/buffer/gone.md')
    useTabStore.setState({ tabs: { [tab.id]: tab }, tabOrder: [tab.id], activeTabId: tab.id })
    useEditorStore.getState().openBuffer(key, 'old', { language: 'markdown' })
    useEditorStore.getState().attachPane(paneId, key)

    await applyReconciliation({ added: [], removed: ['gone.md'], modified: [] }, [], liveDeps({}))

    expect(useTabStore.getState().tabs[tab.id]).toBeUndefined() // last pane → tab closed
    expect(useEditorStore.getState().buffers[key]).toBeUndefined()
  })

  it('reloads a changed clean editor so savedContent/isDirty/lastStat all update and a later save keeps restored bytes', async () => {
    const content = editor(inapp, '/buffer/a.md')
    const tab = leafTab(content)
    const paneId = tab.layout.type === 'leaf' ? tab.layout.pane.id : ''
    const key = bufferKey(inapp, '/buffer/a.md')
    useTabStore.setState({ tabs: { [tab.id]: tab }, tabOrder: [tab.id], activeTabId: tab.id })
    useEditorStore.getState().openBuffer(key, 'old content', { language: 'markdown' }, { mtime: 1, size: 11 })
    useEditorStore.getState().attachPane(paneId, key)

    await applyReconciliation(
      { added: [], removed: [], modified: ['a.md'] },
      ['a.md'],
      liveDeps({ '/buffer/a.md': { content: 'restored bytes', stat: { mtime: 999, size: 14 } } }),
    )

    const buf = useEditorStore.getState().buffers[key]
    expect(buf.content).toBe('restored bytes')
    expect(buf.savedContent).toBe('restored bytes')
    expect(buf.isDirty).toBe(false)
    expect(buf.lastStat).toEqual({ mtime: 999, size: 14 })
    // A subsequent save (markSaved) persists the restored content, not stale bytes.
    useEditorStore.getState().markSaved(key)
    expect(useEditorStore.getState().buffers[key].savedContent).toBe('restored bytes')
  })

  it('remounts a changed preview at a NEW pane id while preserving its split position; sibling untouched', async () => {
    const tab: Tab = {
      id: 'split-tab',
      pinned: false,
      locked: false,
      createdAt: 0,
      layout: {
        type: 'split',
        id: 's1',
        direction: 'h',
        children: [
          { type: 'leaf', pane: { id: 'L', content: { kind: 'image-preview', source: inapp, filePath: '/buffer/b.png' } } },
          { type: 'leaf', pane: { id: 'R', content: { kind: 'dashboard' } } },
        ],
        sizes: [50, 50],
      },
    }
    useTabStore.setState({ tabs: { [tab.id]: tab }, tabOrder: [tab.id], activeTabId: tab.id })

    await applyReconciliation({ added: [], removed: [], modified: ['b.png'] }, ['b.png'], liveDeps({}))

    const after = useTabStore.getState().tabs[tab.id].layout
    expect(after.type).toBe('split')
    if (after.type === 'split') {
      const left = after.children[0]
      const right = after.children[1]
      // Position preserved, id changed, content preserved.
      expect(left.type).toBe('leaf')
      if (left.type === 'leaf') {
        expect(left.pane.id).not.toBe('L')
        expect(left.pane.content).toEqual({ kind: 'image-preview', source: inapp, filePath: '/buffer/b.png' })
      }
      // Sibling pane untouched.
      expect(right.type === 'leaf' && right.pane.id).toBe('R')
    }
    // The old pane id no longer exists in the tree.
    expect(findPane(after, 'L')).toBeUndefined()
  })

  it('closes a removed-path preview pane', async () => {
    const tab = leafTab({ kind: 'pdf-preview', source: inapp, filePath: '/buffer/doc.pdf' })
    useTabStore.setState({ tabs: { [tab.id]: tab }, tabOrder: [tab.id], activeTabId: tab.id })

    await applyReconciliation({ added: [], removed: ['doc.pdf'], modified: [] }, [], liveDeps({}))

    expect(useTabStore.getState().tabs[tab.id]).toBeUndefined()
  })

  it('isolates a failing action (best-effort) and still applies the rest (codex R2 H2/H3)', async () => {
    // A reload whose readFile will throw (no file provided) + a removed preview
    // to close. The reload must fail in isolation without aborting the close.
    const edTab = leafTab(editor(inapp, '/buffer/bad.md'))
    const pvTab = leafTab({ kind: 'pdf-preview', source: inapp, filePath: '/buffer/gone.pdf' })
    useTabStore.setState({
      tabs: { [edTab.id]: edTab, [pvTab.id]: pvTab },
      tabOrder: [edTab.id, pvTab.id],
      activeTabId: edTab.id,
    })

    const result = await applyReconciliation(
      { added: [], removed: ['gone.pdf'], modified: ['bad.md'] },
      ['bad.md'], // bad.md is still a file → planned as reload; readFile will throw
      liveDeps({}), // empty → readFile('/buffer/bad.md') throws
    )

    // The failing reload is recorded, not thrown...
    expect(result.failed).toHaveLength(1)
    expect(result.failed[0].action.kind).toBe('reload-editor')
    // ...and the unrelated removed-preview close STILL happened.
    expect(useTabStore.getState().tabs[pvTab.id]).toBeUndefined()
  })
})
