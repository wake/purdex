// spa/src/components/editor/EditorPane.tsx
import { lazy, Suspense, useEffect, useCallback } from 'react'
import type { PaneRendererProps } from '../../lib/module-registry'
import { useEditorStore } from '../../stores/useEditorStore'
import { useEditorSettingsStore } from '../../stores/useEditorSettingsStore'
import { useTabStore } from '../../stores/useTabStore'
import { useWorkspaceStore } from '../../features/workspace/store'
import { useI18nStore } from '../../stores/useI18nStore'
import { openInAppFile } from '../../lib/open-in-app-file'
import { MonacoWrapper } from './MonacoWrapper'
import { DiffView } from './DiffView'
import { EditorToolbar } from './EditorToolbar'
import { EditorStatusBar } from './EditorStatusBar'
import { RenamePopover } from '../RenamePopover'
import { findPane } from '../../lib/pane-tree'
import { bufferKey } from '../../lib/editor-buffer-key'
import { STORAGE_ROOT } from '../../lib/storage-paths'
import { createUniqueInAppFile } from '../../lib/inapp-namer'
import { getFsBackend } from '../../lib/fs-backend'
import { displayName, isInvalidRename, isUntitledPath } from './editor-pane-naming'
import { useRenamePopoverState } from './hooks/useRenamePopoverState'
import { useEditorPaneLoadState } from './hooks/useEditorPaneLoadState'
import { useEditorSaveFlow } from './hooks/useEditorSaveFlow'
import { useEditorRenameFlow } from './hooks/useEditorRenameFlow'
import { useLiveModeGate } from './hooks/useLiveModeGate'
import type { FileSource } from '../../types/fs'
import type { UntitledDocumentState } from '../../types/tab'

const TiptapEditor = lazy(() =>
  import('./TiptapEditor').then((m) => ({ default: m.TiptapEditor }))
)

function sourceIdentity(source: FileSource): string {
  return source.type === 'daemon' ? `daemon:${source.hostId}` : source.type
}

// Local helper — walks the tabStore layouts to find which tab owns a given pane.
// No shared util exists; inline scan is cheap and only runs on explicit user
// actions (breadcrumb popover switch, manage, new buffer).
function findTabIdForPane(paneId: string): string | undefined {
  const tabs = useTabStore.getState().tabs
  for (const [tabId, tab] of Object.entries(tabs)) {
    if (findPane(tab.layout, paneId)) return tabId
  }
  return undefined
}

/**
 * Resolve the workspace that hosts this pane, so a buffer-switch opens the
 * file into the right workspace (`openInAppFile` → `computeClusterInsertTarget`
 * / `insertTab` are workspace-scoped). Mirrors `StoragePane.resolveWorkspaceId`
 * (built here on the existing `findTabIdForPane`): map the owning tab → its
 * workspace, falling back to the active workspace when the pane isn't found in
 * any layout. Returns `null` when the pane has no owning workspace — we do NOT
 * guess the active workspace (R2-2); `openInAppFile` refuses a null id.
 */
function resolveWorkspaceId(paneId: string): string | null {
  const wsState = useWorkspaceStore.getState()
  const tabId = findTabIdForPane(paneId)
  if (tabId) {
    return wsState.findWorkspaceByTab(tabId)?.id ?? null
  }
  return null
}

// Outer component does kind guard to avoid hooks-after-early-return
export function EditorPane({ pane, isActive }: PaneRendererProps) {
  const content = pane.content
  if (content.kind !== 'editor') return null
  return <EditorPaneInner paneId={pane.id} source={content.source} filePath={content.filePath} untitled={content.untitled} isActive={isActive} />
}

function EditorPaneInner({ paneId, source, filePath, untitled, isActive }: { paneId: string; source: FileSource; filePath: string; untitled?: UntitledDocumentState; isActive: boolean }) {
  const t = useI18nStore((s) => s.t)
  const key = bufferKey(source, filePath)
  const sourceId = sourceIdentity(source)
  const isUntitled = isUntitledPath(filePath)
  const currentName = displayName(filePath, untitled)
  const buffer = useEditorStore((s) => s.buffers[key])
  const paneState = useEditorStore((s) => s.paneStates[paneId])
  const contentWidth = useEditorSettingsStore((s) => s.contentWidth)
  // Only trust paneState once attachPane has rebound it to THIS buffer. Right after
  // a buffer switch, the first render still sees paneState belonging to the previous
  // buffer (attachPane is a post-commit effect). Deriving the aligned view
  // synchronously — falling back to fresh-pane defaults (raw + null viewState) when
  // it hasn't caught up — keeps the stale paneState off-screen. This fixes the #863
  // `Loading editor…` flicker without leaking the old buffer's mode/viewState/cursor
  // onto the new one (attachPane rebuilds paneState to these exact defaults anyway).
  const alignedPaneState = paneState?.bufferKey === key ? paneState : undefined
  const isMarkdown = buffer?.language === 'markdown'
  // Spec 2.3: markdown whose content cannot survive a Live Mode round trip
  // (raw HTML, front matter, footnotes, anything the default-deny assessment
  // does not recognise) must not open there — the loss happens at parse time,
  // before the user touches anything.
  const liveModeGate = useLiveModeGate(buffer?.content, isMarkdown, t)
  // Mode resolution, in order of precedence:
  //   1. stale/unaligned paneState → raw. Deriving raw while paneState hasn't
  //      rebound to THIS buffer keeps the #863 invariant: Tiptap (lazy) never
  //      mounts against a stale paneState, and no `Loading editor…` Suspense
  //      flicker paints during a buffer switch. The wysiwyg default only kicks
  //      in once aligned.
  //   2. aligned + explicit user choice (concrete editorMode) → that choice; it
  //      wins and survives remounts. This is above the safety gate on purpose
  //      (spec 2.3): the gate governs the DEFAULT, and a user who deliberately
  //      switches an unsafe file into Live Mode is making an informed choice.
  //   3. aligned + no choice (editorMode null) → language default: markdown opens
  //      in Live Mode (wysiwyg) unless the gate blocks it; everything else raw.
  const editorMode = alignedPaneState
    ? (alignedPaneState.editorMode ?? (isMarkdown && !liveModeGate.forcesRaw ? 'wysiwyg' : 'raw'))
    : 'raw'
  const effectiveEditorMode = isMarkdown ? editorMode : 'raw'
  // Explain raw ONLY when the gate is what produced it. Raw that the user chose,
  // or that a non-markdown language implies, needs no explanation; neither does
  // the transient stale-paneState window (no aligned state yet).
  const rawReason = alignedPaneState && !alignedPaneState.editorMode && isMarkdown
    ? liveModeGate.reason
    : undefined
  const showDiff = alignedPaneState?.showDiff ?? false
  // Spec 1.3: a missing `lastStat` alone does NOT make a buffer savable. Only a
  // never-saved *untitled* buffer needs that escape hatch (it has no file behind
  // it yet, so there is nothing to compare against). A loaded file whose stat is
  // absent must not masquerade as modified — that was what made every remote
  // file look dirty the moment it opened.
  const canSave = buffer ? (buffer.isDirty || (!!buffer.untitled && !buffer.lastStat)) : false

  const renamePopover = useRenamePopoverState(filePath)
  const { activeLoadError, retryLoad } = useEditorPaneLoadState({
    key, source, sourceId, filePath, untitled, isUntitled,
  })
  const { handleSave, saveUntitledBuffer, saveButtonRef } = useEditorSaveFlow({
    key, source, filePath, paneId, untitled, popover: renamePopover.controls, t,
  })
  const { handleRenameSubmit } = useEditorRenameFlow({
    key, source, filePath, currentName,
    renameMode: renamePopover.mode,
    saveUntitledBuffer,
    popover: renamePopover.controls,
    t,
  })

  const handleCursorChange = useCallback((line: number, column: number) => {
    useEditorStore.getState().updateCursor(paneId, line, column)
  }, [paneId])

  const handleViewStateChange = useCallback((viewState: import('monaco-editor').editor.ICodeEditorViewState | null) => {
    useEditorStore.getState().saveMonacoViewState(paneId, viewState)
  }, [paneId])

  useEffect(() => {
    useEditorStore.getState().attachPane(paneId, key)
  }, [paneId, key])

  useEffect(() => {
    if (!isMarkdown && editorMode !== 'raw') {
      useEditorStore.getState().setEditorMode(paneId, 'raw')
    }
  }, [editorMode, isMarkdown, paneId])

  // Cleanup pane state only when the pane is truly gone, not just hidden by tab switching.
  useEffect(() => {
    return () => {
      const currentPane = Object.values(useTabStore.getState().tabs)
        .map((tab) => findPane(tab.layout, paneId))
        .find(Boolean)
      const stillSameEditor = currentPane?.content.kind === 'editor' &&
        currentPane.content.filePath === filePath &&
        sourceIdentity(currentPane.content.source) === sourceId
      if (!stillSameEditor) {
        useEditorStore.getState().closePane(paneId, key)
      }
    }
  }, [filePath, key, paneId, sourceId])

  // Detect external file changes when tab becomes active
  useEffect(() => {
    if (!isActive) return
    if (isUntitled) return

    const buf = useEditorStore.getState().buffers[key]
    if (!buf) return

    const backend = getFsBackend(source)
    if (!backend) return

    backend.stat(filePath)
      .then((stat) => {
        const currentBuf = useEditorStore.getState().buffers[key]
        if (!currentBuf?.lastStat) return
        if (stat.mtime === currentBuf.lastStat.mtime && stat.size === currentBuf.lastStat.size) return

        return backend.read(filePath).then((data) => {
          const text = new TextDecoder().decode(data)
          const latestBuf = useEditorStore.getState().buffers[key]
          if (!latestBuf || text === latestBuf.savedContent) return

          if (!latestBuf.isDirty) {
            useEditorStore.getState().reloadBuffer(key, text, { mtime: stat.mtime, size: stat.size })
          } else {
            console.warn(`[editor] External change detected for ${filePath}, buffer is dirty`)
          }
        })
      })
      .catch(() => {}) // File may have been deleted
    // eslint-disable-next-line react-hooks/exhaustive-deps -- only re-check on tab activation, not on source/filePath change
  }, [isActive, isUntitled, key, source])

  if (!buffer) {
    // Spec 1.2: a failed load renders an explicit, retryable error instead of an
    // empty editor. No buffer exists here, so there is nothing to save over the
    // real file.
    if (activeLoadError !== null) {
      return (
        <div
          data-testid="editor-load-error"
          className="h-full w-full flex flex-col items-center justify-center gap-2 px-6 text-center"
        >
          <div className="text-xs text-red-400">{t('editor.load_error.title')}</div>
          <div className="max-w-full text-xs break-words text-text-muted">
            {activeLoadError.messageKey
              ? t(activeLoadError.messageKey)
              : activeLoadError.message || t('editor.load_error.unknown')}
          </div>
          <button
            type="button"
            data-testid="editor-load-error-retry"
            onClick={retryLoad}
            className="mt-1 rounded-md bg-accent px-3 py-1.5 text-xs text-text-inverse hover:bg-accent-hover"
          >
            {t('editor.load_error.retry')}
          </button>
        </div>
      )
    }
    return <div className="flex-1 flex items-center justify-center text-text-muted text-xs">Loading...</div>
  }

  return (
    <div className="h-full w-full flex flex-col overflow-hidden">
      <EditorToolbar
        source={source}
        filePath={filePath}
        displayPath={untitled ? untitled.name : undefined}
        isDirty={buffer.isDirty}
        canSave={canSave}
        showDiff={showDiff}
        rawReason={rawReason}
        onSave={handleSave}
        saveButtonRef={saveButtonRef}
        onDiff={() => useEditorStore.getState().setShowDiff(paneId, !showDiff)}
        onRenameStart={(anchorRect) => renamePopover.controls.openRename(anchorRect)}
        onBufferSwitch={(newKey) => {
          // Dirty-guard (spec v1.3 §4.8): prompt before leaving a dirty pane.
          const currentKey = bufferKey({ type: 'inapp' }, filePath)
          const currentBuf = useEditorStore.getState().buffers[currentKey]
          if (currentBuf?.isDirty && !window.confirm(t('editor.buffers.confirm_switch_dirty'))) return

          // T6 (spec §3.3): route through the opener registry so a nested
          // png/pdf resolves to the right preview pane (open-or-focus) instead
          // of the old hardcoded `{ kind: 'editor' }` swapped in place. The
          // current pane's buffer is left intact; the target opens or focuses
          // its own tab in this workspace. `openInAppFile` is async (stat-gate,
          // R2-1) and self-aborts on a missing/refused target — fire-and-forget.
          void openInAppFile(newKey, resolveWorkspaceId(paneId))
        }}
        onManage={() => {
          useTabStore.getState().openSingletonTab({ kind: 'editor-buffers' })
        }}
        onNewBuffer={async () => {
          // v1.4 §4.8 extension (F7): mirror the onBufferSwitch dirty
          // guard here. A user clicking "New buffer" from the popover
          // while the current pane has unsaved edits would otherwise
          // have their work discarded on pane swap.
          const currentKey = bufferKey({ type: 'inapp' }, filePath)
          const currentBuf = useEditorStore.getState().buffers[currentKey]
          if (currentBuf?.isDirty && !window.confirm(t('editor.buffers.confirm_switch_dirty'))) return

          // Eager unified namer (#854): atomically reserve a unique empty file
          // (collision-free even on a rapid double-click) and open its real path.
          let path: string
          try {
            path = await createUniqueInAppFile(STORAGE_ROOT, 'md')
          } catch {
            return
          }
          const tabId = findTabIdForPane(paneId)
          if (!tabId) return
          useTabStore.getState().setPaneContent(tabId, paneId, {
            kind: 'editor',
            source: { type: 'inapp' },
            filePath: path,
          })
        }}
      />
      <div className="flex-1 min-h-0 overflow-hidden">
        {showDiff ? (
          <DiffView
            original={buffer.savedContent}
            modified={buffer.content}
            language={buffer.language}
          />
        ) : effectiveEditorMode === 'raw' ? (
          <MonacoWrapper
            key={buffer.modelId}
            content={buffer.content}
            language={buffer.language}
            modelId={buffer.modelId}
            isActive={isActive}
            initialViewState={alignedPaneState?.monacoViewState ?? null}
            onChange={(value) => useEditorStore.getState().updateContent(key, value)}
            onCursorChange={handleCursorChange}
            onViewStateChange={handleViewStateChange}
            onSave={handleSave}
          />
        ) : (
          /* wysiwyg path. effectiveEditorMode === 'wysiwyg' requires alignedPaneState:
             the mode resolution derives raw whenever paneState is stale/unaligned (the
             markdown → wysiwyg default only applies once aligned), so paneState is
             guaranteed already rebound to THIS buffer here — the stale-paneState window
             can never reach this branch (it derives raw and renders Monaco instead).
             Mounting TiptapEditor against the aligned paneState is therefore safe: its
             one-shot restore reads the correct tiptapViewState and didRestoreRef is never
             locked against a stale state (supersedes PR #862's R3 post-commit gating). */
          <Suspense fallback={<div className="flex-1 flex items-center justify-center text-text-muted text-xs">Loading editor...</div>}>
            <TiptapEditor
              key={buffer.modelId}
              content={buffer.content}
              isActive={isActive}
              contentWidth={contentWidth}
              initialViewState={alignedPaneState?.tiptapViewState ?? null}
              onChange={(md) => useEditorStore.getState().updateContent(key, md)}
              onViewStateChange={(vs) => useEditorStore.getState().saveTiptapViewState(paneId, vs)}
              onSave={handleSave}
            />
          </Suspense>
        )}
      </div>
      <EditorStatusBar
        source={source}
        line={alignedPaneState?.cursorPosition.line ?? 1}
        column={alignedPaneState?.cursorPosition.column ?? 1}
        language={buffer.language}
        eol={buffer.eol}
        encoding={buffer.encoding}
        isMarkdown={isMarkdown}
        editorMode={effectiveEditorMode}
        contentWidth={contentWidth}
        // Width toggle is a Live-Mode-only control: while DiffView is mounted the
        // Tiptap surface is gone, so withhold the handler (EditorStatusBar hides
        // the toggle when onContentWidthChange is absent) even though editorMode
        // may still read 'wysiwyg' (AC4: raw/diff show no toggle).
        onContentWidthChange={showDiff ? undefined : (value) => useEditorSettingsStore.getState().setContentWidth(value)}
        onLanguageChange={(language) => useEditorStore.getState().setBufferLanguage(key, language)}
        onModeChange={(mode) => {
          if (!isMarkdown && mode === 'wysiwyg') return
          useEditorStore.getState().setEditorMode(paneId, mode)
        }}
      />
      {renamePopover.anchorRect && (
        <RenamePopover
          anchorRect={renamePopover.anchorRect}
          currentName={currentName}
          initialValue={renamePopover.initialValue}
          allowUnchangedSubmit={renamePopover.mode === 'save'}
          onConfirm={handleRenameSubmit}
          onCancel={renamePopover.controls.close}
          error={renamePopover.warning}
          onClearError={() => renamePopover.controls.setWarning(undefined)}
          placeholder="File name"
          validateName={(trimmedDraft, currentName) => {
            if (!trimmedDraft || trimmedDraft === currentName) return undefined
            return isInvalidRename(trimmedDraft) ? 'Invalid file name' : undefined
          }}
        />
      )}
    </div>
  )
}
