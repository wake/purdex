import { describe, it, expect, beforeEach, vi, afterEach } from 'vitest'
import { moveTabContentIntoPane, MOVABLE_KINDS } from './pane-move'
import { useTabStore } from '../stores/useTabStore'
import { useWorkspaceStore } from '../features/workspace/store'
import { useHistoryStore } from '../stores/useHistoryStore'
import { useEditorStore } from '../stores/useEditorStore'
import { bufferKey } from './editor-buffer-key'
import { getPrimaryPane } from './pane-tree'
import { createTab, type Tab, type PaneContent } from '../types/tab'
import type { FileSource } from '../types/fs'

// --- Harness -------------------------------------------------------------
// Merge-mode setState with every mutable field listed explicitly so no state
// leaks between tests.
function resetStores() {
  useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null, visitHistory: [] })
  useWorkspaceStore.getState().reset()
  useHistoryStore.setState({ browseHistory: [], closedTabs: [] })
  useEditorStore.getState().clearAllBuffers()
}

const DAEMON_SOURCE: FileSource = { type: 'daemon', hostId: 'h1' }

function editorContent(filePath: string): PaneContent {
  return { kind: 'editor', source: DAEMON_SOURCE, filePath }
}

function makeTab(content: PaneContent): Tab {
  return createTab(content)
}

function primaryPaneId(tabId: string): string {
  return getPrimaryPane(useTabStore.getState().tabs[tabId].layout).id
}

/** Seed a tab into the global tab store + (optionally) a workspace. */
function seedTab(content: PaneContent, wsId?: string): Tab {
  const tab = makeTab(content)
  useTabStore.getState().addTab(tab)
  if (wsId) useWorkspaceStore.getState().addTabToWorkspace(wsId, tab.id)
  return tab
}

describe('moveTabContentIntoPane', () => {
  beforeEach(() => {
    resetStores()
  })

  it('exposes the MOVABLE_KINDS allowlist', () => {
    expect(MOVABLE_KINDS).toEqual(['editor', 'tmux-session', 'image-preview', 'pdf-preview'])
  })

  // === Guard no-ops =====================================================

  describe('guard no-ops (return false, state unchanged)', () => {
    it('source tab does not exist', () => {
      const target = seedTab({ kind: 'new-tab' })
      const targetPane = primaryPaneId(target.id)
      const before = useTabStore.getState().tabs

      const result = moveTabContentIntoPane('missing-source', target.id, targetPane)

      expect(result).toBe(false)
      expect(useTabStore.getState().tabs).toBe(before)
    })

    it('source tab is locked', () => {
      const target = seedTab({ kind: 'new-tab' })
      const source = seedTab(editorContent('/a.ts'))
      useTabStore.getState().toggleLock(source.id)
      const targetPane = primaryPaneId(target.id)

      const result = moveTabContentIntoPane(source.id, target.id, targetPane)

      expect(result).toBe(false)
      // Source still present, target untouched
      expect(useTabStore.getState().tabs[source.id]).toBeDefined()
      expect(getPrimaryPane(useTabStore.getState().tabs[target.id].layout).content).toEqual({ kind: 'new-tab' })
    })

    it('source has more than one leaf (countLeaves > 1)', () => {
      const target = seedTab({ kind: 'new-tab' })
      const source = seedTab(editorContent('/a.ts'))
      // Split the source so it has 2 leaves
      const sourcePane = primaryPaneId(source.id)
      useTabStore.getState().splitPaneBlank(source.id, sourcePane, 'h')
      const targetPane = primaryPaneId(target.id)

      const result = moveTabContentIntoPane(source.id, target.id, targetPane)

      expect(result).toBe(false)
      expect(useTabStore.getState().tabs[source.id]).toBeDefined()
      expect(getPrimaryPane(useTabStore.getState().tabs[target.id].layout).content).toEqual({ kind: 'new-tab' })
    })

    it('source primary content kind is not in the allowlist', () => {
      const target = seedTab({ kind: 'new-tab' })
      // 'dashboard' is not a movable kind
      const source = seedTab({ kind: 'dashboard' })
      const targetPane = primaryPaneId(target.id)

      const result = moveTabContentIntoPane(source.id, target.id, targetPane)

      expect(result).toBe(false)
      expect(useTabStore.getState().tabs[source.id]).toBeDefined()
      expect(getPrimaryPane(useTabStore.getState().tabs[target.id].layout).content).toEqual({ kind: 'new-tab' })
    })

    it('source === target', () => {
      const source = seedTab(editorContent('/a.ts'))
      const sourcePane = primaryPaneId(source.id)
      const before = useTabStore.getState().tabs

      const result = moveTabContentIntoPane(source.id, source.id, sourcePane)

      expect(result).toBe(false)
      expect(useTabStore.getState().tabs).toBe(before)
    })
  })

  // === Happy path =======================================================

  it('injects source primary content into the target pane and removes the source tab', () => {
    const ws = useWorkspaceStore.getState().addWorkspace('WS')
    const content = editorContent('/hello.ts')
    const source = seedTab(content, ws.id)
    const target = seedTab({ kind: 'new-tab' }, ws.id)
    const targetPane = primaryPaneId(target.id)

    const result = moveTabContentIntoPane(source.id, target.id, targetPane)

    expect(result).toBe(true)
    // Target pane now holds the source primary content
    expect(getPrimaryPane(useTabStore.getState().tabs[target.id].layout).content).toEqual(content)
    // Source tab removed globally
    expect(useTabStore.getState().tabs[source.id]).toBeUndefined()
    // Source removed from its workspace
    const updatedWs = useWorkspaceStore.getState().workspaces.find((w) => w.id === ws.id)!
    expect(updatedWs.tabs).not.toContain(source.id)
    expect(updatedWs.tabs).toContain(target.id)
  })

  it('moves tmux-session content (allowlisted kind)', () => {
    const content: PaneContent = {
      kind: 'tmux-session',
      hostId: 'h1',
      sessionCode: 'abc',
      mode: 'terminal',
      cachedName: 'sess',
      tmuxInstance: 'default',
    }
    const source = seedTab(content)
    const target = seedTab({ kind: 'new-tab' })
    const targetPane = primaryPaneId(target.id)

    const result = moveTabContentIntoPane(source.id, target.id, targetPane)

    expect(result).toBe(true)
    expect(getPrimaryPane(useTabStore.getState().tabs[target.id].layout).content).toEqual(content)
    expect(useTabStore.getState().tabs[source.id]).toBeUndefined()
  })

  // === Active fallback ==================================================

  describe('active fallback (delegated to closeTabInWorkspace)', () => {
    it('(a) source is workspace-active but NOT global-active → global active preserved, ws active moves to fallback', () => {
      const ws = useWorkspaceStore.getState().addWorkspace('WS')
      const source = seedTab(editorContent('/a.ts'), ws.id)
      const sibling = seedTab({ kind: 'new-tab' }, ws.id)
      // A standalone tab holds the global active focus
      const globalActive = seedTab({ kind: 'new-tab' })
      // ws-active = source; global-active = standalone
      useWorkspaceStore.getState().setWorkspaceActiveTab(ws.id, source.id)
      useTabStore.getState().setActiveTab(globalActive.id)
      useTabStore.setState({ visitHistory: [] }) // force adjacent fallback → sibling

      const target = seedTab({ kind: 'new-tab' }, ws.id)
      const targetPane = primaryPaneId(target.id)

      const result = moveTabContentIntoPane(source.id, target.id, targetPane)

      expect(result).toBe(true)
      // Global active unchanged (source was not global-active)
      expect(useTabStore.getState().activeTabId).toBe(globalActive.id)
      // ws active moved to the adjacent fallback = sibling
      const updatedWs = useWorkspaceStore.getState().workspaces.find((w) => w.id === ws.id)!
      expect(updatedWs.activeTabId).toBe(sibling.id)
    })

    it('(b) source is also global-active → global active moves to the fallback id', () => {
      const ws = useWorkspaceStore.getState().addWorkspace('WS')
      const source = seedTab(editorContent('/a.ts'), ws.id)
      const sibling = seedTab({ kind: 'new-tab' }, ws.id)
      useWorkspaceStore.getState().setWorkspaceActiveTab(ws.id, source.id)
      useTabStore.getState().setActiveTab(source.id)
      useTabStore.setState({ visitHistory: [] }) // force adjacent fallback → sibling

      const target = seedTab({ kind: 'new-tab' }, ws.id)
      const targetPane = primaryPaneId(target.id)

      const result = moveTabContentIntoPane(source.id, target.id, targetPane)

      expect(result).toBe(true)
      // Global active moved to the fallback = sibling (the only remaining ws tab in scope)
      expect(useTabStore.getState().activeTabId).toBe(sibling.id)
      const updatedWs = useWorkspaceStore.getState().workspaces.find((w) => w.id === ws.id)!
      expect(updatedWs.activeTabId).toBe(sibling.id)
    })
  })

  // === No dirty-confirm, buffer preserved ===============================

  describe('does not trigger dirty-confirm and preserves editor buffer', () => {
    let confirmSpy: ReturnType<typeof vi.spyOn>

    beforeEach(() => {
      confirmSpy = vi.spyOn(window, 'confirm').mockReturnValue(true)
    })
    afterEach(() => {
      confirmSpy.mockRestore()
    })

    it('window.confirm is never called and the dirty buffer survives', () => {
      const ws = useWorkspaceStore.getState().addWorkspace('WS')
      const filePath = '/dirty.ts'
      const content = editorContent(filePath)
      const source = seedTab(content, ws.id)
      const target = seedTab({ kind: 'new-tab' }, ws.id)
      const targetPane = primaryPaneId(target.id)

      // Seed a dirty editor buffer for the source file
      const key = bufferKey(DAEMON_SOURCE, filePath)
      useEditorStore.getState().openBuffer(key, 'saved', { language: 'typescript' })
      useEditorStore.getState().updateContent(key, 'saved + unsaved edits')
      expect(useEditorStore.getState().buffers[key].isDirty).toBe(true)

      const result = moveTabContentIntoPane(source.id, target.id, targetPane)

      expect(result).toBe(true)
      // (1) confirm never fired — pull-in is a move, not a destroy
      expect(confirmSpy).not.toHaveBeenCalled()
      // (2) buffer still present (content lives in the global editor store)
      expect(useEditorStore.getState().buffers[key]).toBeDefined()
      expect(useEditorStore.getState().buffers[key].isDirty).toBe(true)
    })
  })
})
