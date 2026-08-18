// spa/src/components/editor/EditorPane.tsx
import { lazy, Suspense, useEffect, useCallback, useState } from 'react'
import type { PaneRendererProps } from '../../lib/module-registry'
import { useEditorStore } from '../../stores/useEditorStore'
import { useEditorSettingsStore } from '../../stores/useEditorSettingsStore'
import { useTabStore } from '../../stores/useTabStore'
import { useWorkspaceStore } from '../../features/workspace/store'
import { useI18nStore } from '../../stores/useI18nStore'
import { getFsBackend } from '../../lib/fs-backend'
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
import { recordRecentFile } from '../../lib/recent-files/record-recent-file'
import { useRecentFilesStore } from '../../stores/useRecentFilesStore'
import { useUndoToast } from '../../stores/useUndoToast'
import type { FileSource } from '../../types/fs'
import type { UntitledDocumentState } from '../../types/tab'
import {
  createMetadata,
  untitledStoragePath,
  untitledSuggestedName,
} from '../../lib/editor-language'

const TiptapEditor = lazy(() =>
  import('./TiptapEditor').then((m) => ({ default: m.TiptapEditor }))
)

function isUntitledPath(filePath: string): boolean {
  return filePath.startsWith('untitled:')
}

function displayName(filePath: string, untitled?: UntitledDocumentState): string {
  return untitled?.name ?? fileName(filePath)
}

function renamePath(filePath: string, nextName: string, untitled?: UntitledDocumentState): string {
  return untitled ? `untitled:${nextName}` : siblingPath(filePath, nextName)
}

function fileName(filePath: string): string {
  return filePath.split('/').pop() ?? filePath
}

function siblingPath(filePath: string, nextBaseName: string): string {
  const separatorIndex = filePath.lastIndexOf('/')
  return separatorIndex === -1 ? nextBaseName : `${filePath.slice(0, separatorIndex)}/${nextBaseName}`
}

function isInvalidRename(name: string): boolean {
  const trimmed = name.trim()
  return trimmed === '' || trimmed === '.' || trimmed === '..' || trimmed.includes('/') || trimmed.includes('\\')
}

function isCaseOnlyRename(oldPath: string, nextPath: string): boolean {
  return oldPath !== nextPath && oldPath.toLowerCase() === nextPath.toLowerCase()
}

function renameWarningMessage(error: unknown): string {
  if (error instanceof Error) {
    if (/exist/i.test(error.message)) return 'File already exists'
    return error.message || 'Rename failed'
  }
  return 'Rename failed'
}

// Spec 3.2 (T3.3): the failure toast has to say *why* the save failed, so the
// reason is extracted from whatever the backend rejected with.
function saveErrorReason(error: unknown): string {
  if (error instanceof Error && error.message) return error.message
  return String(error)
}

// Spec 1.2: a load failure must never degrade into an empty buffer. Extract the
// most specific reason we can so the error surface tells the user *why* the file
// could not be read (empty string → the generic i18n fallback).
function loadErrorMessage(error: unknown): string {
  if (error instanceof Error) return error.message
  if (typeof error === 'string') return error
  return ''
}

/**
 * A pane-local load failure. Two shapes, because the two sources of failure carry
 * different text: `message` is a raw reason handed to us by the backend (already
 * a sentence, not translatable); `messageKey` is an i18n key for failures we
 * diagnose ourselves (spec 1.2b: no FS backend resolved at all). Keeping the key
 * unresolved until render means a locale switch retranslates the surface.
 */
type LoadError = { message: string; messageKey?: undefined } | { messageKey: string; message?: undefined }

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
  // Mode resolution, in order of precedence:
  //   1. stale/unaligned paneState → raw. Deriving raw while paneState hasn't
  //      rebound to THIS buffer keeps the #863 invariant: Tiptap (lazy) never
  //      mounts against a stale paneState, and no `Loading editor…` Suspense
  //      flicker paints during a buffer switch. The wysiwyg default only kicks
  //      in once aligned.
  //   2. aligned + explicit user choice (concrete editorMode) → that choice; it
  //      wins and survives remounts.
  //   3. aligned + no choice (editorMode null) → language default: markdown opens
  //      in Live Mode (wysiwyg), everything else raw.
  const editorMode = alignedPaneState
    ? (alignedPaneState.editorMode ?? (isMarkdown ? 'wysiwyg' : 'raw'))
    : 'raw'
  const effectiveEditorMode = isMarkdown ? editorMode : 'raw'
  const showDiff = alignedPaneState?.showDiff ?? false
  // Spec 1.3: a missing `lastStat` alone does NOT make a buffer savable. Only a
  // never-saved *untitled* buffer needs that escape hatch (it has no file behind
  // it yet, so there is nothing to compare against). A loaded file whose stat is
  // absent must not masquerade as modified — that was what made every remote
  // file look dirty the moment it opened.
  const canSave = buffer ? (buffer.isDirty || (!!buffer.untitled && !buffer.lastStat)) : false
  // Per-pane load failure (spec 1.2). Local state on purpose: the store never
  // learns about a failed load because no buffer is created for it.
  const [loadError, setLoadError] = useState<LoadError | null>(null)
  const [loadAttempt, setLoadAttempt] = useState(0)
  const [renameAnchorRect, setRenameAnchorRect] = useState<DOMRect | null>(null)
  const [renameMode, setRenameMode] = useState<'rename' | 'save'>('rename')
  const [renameInitialValue, setRenameInitialValue] = useState<string>()
  const [renameWarning, setRenameWarning] = useState<string>()

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
    setRenameAnchorRect(null)
    setRenameWarning(undefined)
  }, [filePath])

  useEffect(() => {
    if (!isMarkdown && editorMode !== 'raw') {
      useEditorStore.getState().setEditorMode(paneId, 'raw')
    }
  }, [editorMode, isMarkdown, paneId])

  // Load file on mount, cleanup buffer on unmount.
  // `loadAttempt` is a deps-only retry trigger (see handleRetryLoad).
  useEffect(() => {
    let stale = false
    setLoadError(null)
    if (useEditorStore.getState().buffers[key]) return // already loaded
    // Only an untitled pane may open an empty buffer (spec 1.2): it has no file
    // behind it yet. Everything else — including "not found" — becomes a load
    // error, because a buffer opened over an unreadable file would silently
    // truncate that file on the next save.
    if (isUntitled) {
      useEditorStore.getState().openBuffer(key, '', createMetadata(source, filePath, untitled))
      return
    }

    // Spec 1.2b: no backend at all (a `local` source outside Electron, a daemon
    // source whose host was removed) used to bail out silently and leave the pane
    // spinning on "Loading…" forever. Same silent-failure class as 1.2, reached by
    // a different route — so it surfaces through the same retryable error state.
    const backend = getFsBackend(source)
    if (!backend) {
      setLoadError({ messageKey: 'editor.load_error.no_backend' })
      return
    }

    backend.read(filePath)
      .then((data) => {
        if (stale) return
        const text = new TextDecoder().decode(data)
        const metadata = createMetadata(source, filePath, untitled)
        return backend.stat(filePath).then((stat) => {
          if (stale) return
          useEditorStore.getState().openBuffer(key, text, metadata, { mtime: stat.mtime, size: stat.size })
        })
      })
      .catch((error: unknown) => {
        if (stale) return
        setLoadError({ message: loadErrorMessage(error) })
      })

    return () => { stale = true }
  }, [filePath, isUntitled, key, sourceId, source, untitled, loadAttempt])

  const handleRetryLoad = useCallback(() => {
    setLoadError(null)
    setLoadAttempt((attempt) => attempt + 1)
  }, [])

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

  // T3.3: one place that turns a save attempt into user-visible feedback. It
  // reuses the existing bottom-centre toast (`useUndoToast` / `GlobalUndoToast`)
  // rather than adding a second notification surface.
  const showSaveToast = useCallback((messageKey: string, params?: Record<string, string>) => {
    useUndoToast.getState().show(t(messageKey, params))
  }, [t])

  const saveUntitledBuffer = useCallback(async (name: string) => {
    const buf = useEditorStore.getState().buffers[key]
    const backend = getFsBackend(source)
    // No buffer / not an untitled pane are preconditions, not outcomes: there is
    // no document to report on.
    if (!buf || !untitled) return
    // A missing backend IS an outcome, and the same one T3.3-4b reports on the
    // ordinary save path. Returning silently here left the user believing the
    // first save of a new document had landed when nothing was written at all.
    if (!backend) {
      showSaveToast('editor.save.failed', { reason: t('editor.load_error.no_backend') })
      return
    }

    const trimmedName = name.trim()
    if (isInvalidRename(trimmedName)) {
      setRenameWarning('Invalid file name')
      return
    }

    const nextPath = untitledStoragePath(trimmedName)
    const nextKey = bufferKey(source, nextPath)
    if (nextKey !== key && useEditorStore.getState().buffers[nextKey]) {
      setRenameWarning('File already exists')
      return
    }

    try {
      const encoded = new TextEncoder().encode(buf.content)
      await backend.write(nextPath, encoded)
      const newStat = await backend.stat(nextPath)
      const nextMetadata = buf.languageSource === 'manual'
        ? { language: buf.language, languageSource: 'manual' as const, untitled: undefined }
        : { ...createMetadata(source, nextPath), untitled: undefined }
      useTabStore.getState().renameEditorPanes(source, filePath, nextPath)
      useEditorStore.getState().renameBuffer(key, nextKey, nextMetadata)
      useEditorStore.getState().markSaved(nextKey, { mtime: newStat.mtime, size: newStat.size })
      recordRecentFile({ kind: 'editor', source, filePath: nextPath })
      useEditorStore.getState().setShowDiff(paneId, false)
      setRenameAnchorRect(null)
      setRenameInitialValue(undefined)
      setRenameWarning(undefined)
      // Confirming the name IS a save outcome (the file now exists on disk), so
      // it reports like any other save. Only *opening* the popover is silent.
      showSaveToast('editor.save.saved', { name: fileName(nextPath) })
    } catch (err) {
      showSaveToast('editor.save.failed', { reason: saveErrorReason(err) })
    }
  }, [filePath, key, paneId, showSaveToast, source, t, untitled])

  const handleSave = useCallback(async (anchorRect?: DOMRect) => {
    const buf = useEditorStore.getState().buffers[key]
    // No buffer: the pane is showing the loading / load-error surface, so there
    // is no document and no save attempt to report.
    if (!buf) return
    // T3.3: an untouched, already-saved buffer is an explicit "nothing to do"
    // outcome. This branch used to swallow every ⌘S without a trace, which is
    // what made the key feel dead.
    if (!buf.isDirty && buf.lastStat) {
      showSaveToast('editor.save.unchanged')
      return
    }
    if (buf.untitled) {
      if (!buf.untitled.hasBeenRenamed) {
        // Opening the name popover is not a save outcome — no toast here; the
        // one that follows the user's confirmation comes from saveUntitledBuffer.
        if (!anchorRect) return
        setRenameMode('save')
        setRenameAnchorRect(anchorRect)
        setRenameInitialValue(untitledSuggestedName(buf.untitled))
        setRenameWarning(undefined)
        return
      }
      await saveUntitledBuffer(buf.untitled.name)
      return
    }

    const backend = getFsBackend(source)
    // An unresolvable backend is a failed save, not a no-op: without this the
    // content silently stays unsaved (same silent-failure class as 1.2b).
    if (!backend) {
      showSaveToast('editor.save.failed', { reason: t('editor.load_error.no_backend') })
      return
    }
    try {
      const encoded = new TextEncoder().encode(buf.content)
      await backend.write(filePath, encoded)
      const newStat = await backend.stat(filePath)
      useEditorStore.getState().markSaved(key, { mtime: newStat.mtime, size: newStat.size })
      recordRecentFile({ kind: 'editor', source, filePath })
      useEditorStore.getState().setShowDiff(paneId, false)
      showSaveToast('editor.save.saved', { name: fileName(filePath) })
    } catch (err) {
      // Replaces a console.error the user could never see; the buffer stays
      // dirty and unmarked, so the toast is the only signal they get.
      showSaveToast('editor.save.failed', { reason: saveErrorReason(err) })
    }
  }, [filePath, key, paneId, saveUntitledBuffer, showSaveToast, source, t])

  const handleRenameSubmit = useCallback(async (nextName: string) => {
    const currentBuffer = useEditorStore.getState().buffers[key]
    if (!currentBuffer) return

    if (renameMode === 'save') {
      await saveUntitledBuffer(nextName)
      return
    }

    if (isInvalidRename(nextName)) {
      setRenameWarning('Invalid file name')
      return
    }

    if (nextName === currentName) {
      setRenameAnchorRect(null)
      setRenameInitialValue(undefined)
      setRenameWarning(undefined)
      return
    }

    const nextPath = renamePath(filePath, nextName, currentBuffer.untitled)
    const nextKey = bufferKey(source, nextPath)
    if (nextKey !== key && useEditorStore.getState().buffers[nextKey]) {
      setRenameWarning('File already exists')
      return
    }

    if (currentBuffer.untitled) {
      const nextUntitled: UntitledDocumentState = {
        ...currentBuffer.untitled,
        name: nextName,
        hasBeenRenamed: true,
      }
      useTabStore.getState().renameEditorPanes(source, filePath, nextPath, { untitled: nextUntitled })
      const nextMetadata = currentBuffer.languageSource === 'manual'
        ? { language: currentBuffer.language, languageSource: 'manual' as const, untitled: nextUntitled }
        : createMetadata(source, nextPath, nextUntitled)
      useEditorStore.getState().renameBuffer(key, nextKey, nextMetadata)
      setRenameAnchorRect(null)
      setRenameInitialValue(undefined)
      setRenameWarning(undefined)
      return
    }

    const backend = getFsBackend(source)
    if (!backend) return

    if (!isCaseOnlyRename(filePath, nextPath)) {
      try {
        await backend.stat(nextPath)
        setRenameWarning('File already exists')
        return
      } catch {
        // Missing target is expected.
      }
    }

    try {
      if (currentBuffer?.lastStat) {
        await backend.rename(filePath, nextPath)
      }
      useTabStore.getState().renameEditorPanes(source, filePath, nextPath)
      const nextMetadata = currentBuffer?.languageSource === 'manual'
        ? { language: currentBuffer.language, languageSource: 'manual' as const }
        : createMetadata(source, nextPath)
      useEditorStore.getState().renameBuffer(key, nextKey, nextMetadata)
      // T3.2: the in-editor rename is the third path-mutating call site (the
      // other two go through `remapPanesUnder`), and the ONLY one a remote file
      // can take — `source` carries the daemon host, so the remap stays scoped
      // to that host's entries.
      useRecentFilesStore.getState().renamePath(source, filePath, nextPath)
      setRenameAnchorRect(null)
      setRenameInitialValue(undefined)
      setRenameWarning(undefined)
    } catch (error) {
      setRenameWarning(renameWarningMessage(error))
    }
  }, [currentName, filePath, key, renameMode, saveUntitledBuffer, source])

  if (!buffer) {
    // Spec 1.2: a failed load renders an explicit, retryable error instead of an
    // empty editor. No buffer exists here, so there is nothing to save over the
    // real file.
    if (loadError !== null) {
      return (
        <div
          data-testid="editor-load-error"
          className="h-full w-full flex flex-col items-center justify-center gap-2 px-6 text-center"
        >
          <div className="text-xs text-red-400">{t('editor.load_error.title')}</div>
          <div className="max-w-full text-xs break-words text-text-muted">
            {loadError.messageKey
              ? t(loadError.messageKey)
              : loadError.message || t('editor.load_error.unknown')}
          </div>
          <button
            type="button"
            data-testid="editor-load-error-retry"
            onClick={handleRetryLoad}
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
        onSave={handleSave}
        onDiff={() => useEditorStore.getState().setShowDiff(paneId, !showDiff)}
        onRenameStart={(anchorRect) => {
          setRenameMode('rename')
          setRenameAnchorRect(anchorRect)
          setRenameInitialValue(undefined)
          setRenameWarning(undefined)
        }}
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
      {renameAnchorRect && (
        <RenamePopover
          anchorRect={renameAnchorRect}
          currentName={currentName}
          initialValue={renameInitialValue}
          allowUnchangedSubmit={renameMode === 'save'}
          onConfirm={handleRenameSubmit}
          onCancel={() => {
            setRenameAnchorRect(null)
            setRenameInitialValue(undefined)
            setRenameWarning(undefined)
          }}
          error={renameWarning}
          onClearError={() => setRenameWarning(undefined)}
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
