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

  // === Target existence guard ===========================================
  //
  // `setPaneContent` / `updatePaneInLayout` silently no-op for stale tab/pane
  // ids. Without a target guard the mover would inject nothing yet still retire
  // the source tab — a content-loss path violating AC4 ("content enters this
  // pane, THEN the source disappears").
  describe('target existence guard (no content-loss when target is stale)', () => {
    it('target tab does not exist → false, source preserved', () => {
      const source = seedTab(editorContent('/a.ts'))

      const result = moveTabContentIntoPane(source.id, 'missing-target', 'any-pane')

      expect(result).toBe(false)
      expect(useTabStore.getState().tabs[source.id]).toBeDefined()
    })

    it('target pane does not exist in the target tab → false, source preserved', () => {
      const source = seedTab(editorContent('/a.ts'))
      const target = seedTab({ kind: 'new-tab' })

      const result = moveTabContentIntoPane(source.id, target.id, 'ghost-pane')

      expect(result).toBe(false)
      expect(useTabStore.getState().tabs[source.id]).toBeDefined()
      // Target untouched
      expect(getPrimaryPane(useTabStore.getState().tabs[target.id].layout).content).toEqual({ kind: 'new-tab' })
    })

    it('editor source + stale target → no orphan paneState, buffer + source preserved', () => {
      const filePath = '/orphan.ts'
      const source = seedTab(editorContent(filePath))
      const sourcePaneId = primaryPaneId(source.id)
      const key = bufferKey(DAEMON_SOURCE, filePath)
      useEditorStore.getState().openBuffer(key, 'saved', { language: 'typescript' })
      useEditorStore.getState().updateContent(key, 'unsaved')
      useEditorStore.getState().attachPane(sourcePaneId, key)

      const result = moveTabContentIntoPane(source.id, 'missing-target', 'ghost-pane')

      expect(result).toBe(false)
      // Guard runs BEFORE the editor pre-bind → no orphan paneState created.
      expect(useEditorStore.getState().paneStates['ghost-pane']).toBeUndefined()
      // Buffer + source survive intact.
      expect(useEditorStore.getState().buffers[key]).toBeDefined()
      expect(useTabStore.getState().tabs[source.id]).toBeDefined()
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

  // === Pre-bind target pane to editor buffer (dirty-buffer loss race) =====
  //
  // A dirty editor tab's real content lives in `useEditorStore`, ref-counted
  // by the paneStates that reference its bufferKey. When the source EditorPane
  // unmounts (post-move) it runs `closePane(sourcePaneId, key)`, which drops
  // the buffer once NO paneState references it. If the target pane has not yet
  // registered its own reference (its `attachPane` is a mount-time effect that
  // runs AFTER the source unmount cleanup in the same commit), the ref-count
  // hits zero and the unsaved buffer is destroyed. The mover must pre-bind the
  // target pane to the buffer BEFORE retiring the source so the count never
  // reaches zero.
  describe('pre-binds the target pane to the editor buffer (prevents dirty loss)', () => {
    const filePath = '/dirty-move.ts'

    function seedDirtyEditorMove() {
      const content = editorContent(filePath)
      const source = seedTab(content)
      const sourcePaneId = primaryPaneId(source.id)
      const target = seedTab({ kind: 'new-tab' })
      const targetPaneId = primaryPaneId(target.id)

      const key = bufferKey(DAEMON_SOURCE, filePath)
      useEditorStore.getState().openBuffer(key, 'saved', { language: 'typescript' })
      useEditorStore.getState().updateContent(key, 'saved + unsaved edits')
      // The source EditorPane owns the only reference before the move.
      useEditorStore.getState().attachPane(sourcePaneId, key)
      expect(useEditorStore.getState().buffers[key].isDirty).toBe(true)

      return { sourceId: source.id, sourcePaneId, targetId: target.id, targetPaneId, key }
    }

    it('binds the target pane to the buffer key during the move', () => {
      const { sourceId, targetId, targetPaneId, key } = seedDirtyEditorMove()

      const result = moveTabContentIntoPane(sourceId, targetId, targetPaneId)

      expect(result).toBe(true)
      // The target pane now references the buffer — proof the move pre-bound it.
      expect(useEditorStore.getState().paneStates[targetPaneId]?.bufferKey).toBe(key)
    })

    it('keeps the dirty buffer alive when the source pane unmount races the move', () => {
      const { sourceId, sourcePaneId, targetId, targetPaneId, key } = seedDirtyEditorMove()

      moveTabContentIntoPane(sourceId, targetId, targetPaneId)
      // Simulate the source EditorPane's unmount cleanup firing AFTER the move
      // but BEFORE the target EditorPane mounts. Without the pre-bind, this
      // drops the ref-count to zero and destroys the unsaved buffer.
      useEditorStore.getState().closePane(sourcePaneId, key)

      expect(useEditorStore.getState().buffers[key]).toBeDefined()
      expect(useEditorStore.getState().buffers[key].isDirty).toBe(true)
      expect(useEditorStore.getState().buffers[key].content).toBe('saved + unsaved edits')
    })

    it('does not pre-bind editor state for non-editor movable content', () => {
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
      const targetPaneId = primaryPaneId(target.id)

      const result = moveTabContentIntoPane(source.id, target.id, targetPaneId)

      expect(result).toBe(true)
      // No editor paneState was created for the target pane.
      expect(useEditorStore.getState().paneStates[targetPaneId]).toBeUndefined()
    })
  })
})
