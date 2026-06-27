import { describe, it, expect, beforeEach } from 'vitest'
import { openInAppFile } from './open-in-app-file'
import { useTabStore } from '../stores/useTabStore'
import { useWorkspaceStore } from '../features/workspace/store'
import {
  clearAllForHmr,
  registerFileOpener,
} from './file-opener-registry'
import { editorModuleDefinition } from './register-modules/editor-module'
import { getPrimaryPane } from './pane-tree'
import type { PaneContent } from '../types/tab'

// Register the REAL editor file-openers so registry dispatch
// (md→editor, png→image-preview, pdf→pdf-preview) is genuinely exercised,
// not mocked away.
function registerEditorOpeners(): void {
  clearAllForHmr()
  for (const opener of editorModuleDefinition.fileOpeners ?? []) {
    registerFileOpener({ ...opener, ownerModuleId: 'editor' })
  }
}

function setupWorkspace(): void {
  useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null })
  useWorkspaceStore.setState({
    workspaces: [
      { id: 'w1', name: 'W1', tabs: [], activeTabId: null, moduleConfig: {} },
    ],
    activeWorkspaceId: 'w1',
  })
}

/** The primary-pane content of a tab, or null. */
function primaryContent(tabId: string): PaneContent | null {
  const tab = useTabStore.getState().tabs[tabId]
  if (!tab) return null
  return getPrimaryPane(tab.layout).content
}

describe('openInAppFile', () => {
  beforeEach(() => {
    registerEditorOpeners()
    setupWorkspace()
  })

  it('opens a .md file as an editor pane (registry dispatch)', () => {
    const tabId = openInAppFile('/buffer/x.md', 'w1')
    expect(tabId).toBeTruthy()
    const content = primaryContent(tabId!)
    expect(content?.kind).toBe('editor')
    expect((content as { filePath: string }).filePath).toBe('/buffer/x.md')
    expect((content as { source: { type: string } }).source.type).toBe('inapp')
  })

  it('opens a .png file as an image-preview pane (registry dispatch)', () => {
    const tabId = openInAppFile('/buffer/p.png', 'w1')
    expect(primaryContent(tabId!)?.kind).toBe('image-preview')
  })

  it('opens a .pdf file as a pdf-preview pane (registry dispatch)', () => {
    const tabId = openInAppFile('/buffer/d.pdf', 'w1')
    expect(primaryContent(tabId!)?.kind).toBe('pdf-preview')
  })

  it('focuses the existing tab when the same file is already open (no duplicate)', () => {
    const first = openInAppFile('/buffer/x.md', 'w1')
    expect(useTabStore.getState().tabOrder).toHaveLength(1)

    const second = openInAppFile('/buffer/x.md', 'w1')
    expect(second).toBe(first)
    // No duplicate tab created.
    expect(useTabStore.getState().tabOrder).toHaveLength(1)
    // The existing tab is focused.
    expect(useTabStore.getState().activeTabId).toBe(first)
  })

  it('never reuses an unrelated editor pane — a different filePath gets a new tab', () => {
    const first = openInAppFile('/buffer/a.md', 'w1')
    const second = openInAppFile('/buffer/b.md', 'w1')
    expect(second).not.toBe(first)
    expect(useTabStore.getState().tabOrder).toHaveLength(2)
    expect(primaryContent(second!)).toMatchObject({
      kind: 'editor',
      filePath: '/buffer/b.md',
    })
  })

  it('places the new tab in the workspace via insertTab (ws.tabs + activeTabId synced)', () => {
    const tabId = openInAppFile('/buffer/x.md', 'w1')
    const ws = useWorkspaceStore.getState().workspaces.find((w) => w.id === 'w1')!
    expect(ws.tabs).toContain(tabId)
    expect(ws.activeTabId).toBe(tabId)
  })

  it('distinguishes same-basename files in different dirs (full-path identity)', () => {
    const a = openInAppFile('/buffer/dir1/x.md', 'w1')
    const b = openInAppFile('/buffer/dir2/x.md', 'w1')
    expect(b).not.toBe(a)
    expect(useTabStore.getState().tabOrder).toHaveLength(2)
  })

  it('returns undefined and opens nothing when no opener matches', () => {
    // A directory-like path with no matching opener (isDirectory false but
    // the editor opener matches any non-binary file, so simulate "no opener"
    // by clearing the registry).
    clearAllForHmr()
    const tabId = openInAppFile('/buffer/x.md', 'w1')
    expect(tabId).toBeUndefined()
    expect(useTabStore.getState().tabOrder).toHaveLength(0)
  })
})
