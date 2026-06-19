// spa/src/components/editor/EditorPane.tsx
import { lazy, Suspense, useEffect, useCallback, useState } from 'react'
import type { PaneRendererProps } from '../../lib/module-registry'
import { useEditorStore } from '../../stores/useEditorStore'
import { useTabStore } from '../../stores/useTabStore'
import { useI18nStore } from '../../stores/useI18nStore'
import { getFsBackend } from '../../lib/fs-backend'
import { MonacoWrapper } from './MonacoWrapper'
import { DiffView } from './DiffView'
import { EditorToolbar } from './EditorToolbar'
import { EditorStatusBar } from './EditorStatusBar'
import { RenamePopover } from '../RenamePopover'
import { findPane } from '../../lib/pane-tree'
import { bufferKey } from '../../lib/editor-buffer-key'
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
  const isMarkdown = buffer?.language === 'markdown'
  const editorMode = paneState?.editorMode ?? 'raw'
  const effectiveEditorMode = isMarkdown ? editorMode : 'raw'
  const showDiff = paneState?.showDiff ?? false
  const canSave = buffer ? (buffer.isDirty || !buffer.lastStat) : false
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

  // Load file on mount, cleanup buffer on unmount
  useEffect(() => {
    let stale = false
    if (useEditorStore.getState().buffers[key]) return // already loaded
    if (isUntitled) {
      useEditorStore.getState().openBuffer(key, '', createMetadata(source, filePath, untitled))
      return
    }

    const backend = getFsBackend(source)
    if (!backend) return

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
      .catch(() => {
        if (stale) return
        // New file — open empty buffer
        useEditorStore.getState().openBuffer(key, '', createMetadata(source, filePath, untitled))
      })

    return () => { stale = true }
  }, [filePath, isUntitled, key, sourceId, source, untitled])

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

  const saveUntitledBuffer = useCallback(async (name: string) => {
    const buf = useEditorStore.getState().buffers[key]
    const backend = getFsBackend(source)
    if (!buf || !backend || !untitled) return

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
      useEditorStore.getState().setShowDiff(paneId, false)
      setRenameAnchorRect(null)
      setRenameInitialValue(undefined)
      setRenameWarning(undefined)
    } catch (err) {
      console.error('[editor] Save failed:', err)
    }
  }, [filePath, key, paneId, source, untitled])

  const handleSave = useCallback(async (anchorRect?: DOMRect) => {
    const buf = useEditorStore.getState().buffers[key]
    if (!buf || (!buf.isDirty && buf.lastStat)) return
    if (buf.untitled) {
      if (!buf.untitled.hasBeenRenamed) {
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
    if (!backend) return
    try {
      const encoded = new TextEncoder().encode(buf.content)
      await backend.write(filePath, encoded)
      const newStat = await backend.stat(filePath)
      useEditorStore.getState().markSaved(key, { mtime: newStat.mtime, size: newStat.size })
      useEditorStore.getState().setShowDiff(paneId, false)
    } catch (err) {
      console.error('[editor] Save failed:', err)
    }
  }, [filePath, key, paneId, saveUntitledBuffer, source])

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
      setRenameAnchorRect(null)
      setRenameInitialValue(undefined)
      setRenameWarning(undefined)
    } catch (error) {
      setRenameWarning(renameWarningMessage(error))
    }
  }, [currentName, filePath, key, renameMode, saveUntitledBuffer, source])

  if (!buffer) {
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
          // Dirty-guard (spec v1.3 §4.8): prompt before swapping content of a
          // dirty pane. Smart-open from EditorBuffersPane intentionally
          // bypasses this — that flow has different mental semantics.
          const currentKey = bufferKey({ type: 'inapp' }, filePath)
          const currentBuf = useEditorStore.getState().buffers[currentKey]
          if (currentBuf?.isDirty && !window.confirm(t('editor.buffers.confirm_switch_dirty'))) return

          const tabId = findTabIdForPane(paneId)
          if (!tabId) return
          useTabStore.getState().setPaneContent(tabId, paneId, {
            kind: 'editor',
            source: { type: 'inapp' },
            filePath: newKey,
          })
          // NOTE: NEVER call `attachPane` here. EditorPane's own
          // `useEffect(() => attachPane(paneId, key), [paneId, key])`
          // rebinds the editor store when React re-renders with the new key.
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

          const path = `/buffer/Untitled-${Date.now()}.md`
          const backend = getFsBackend({ type: 'inapp' })
          if (!backend) return
          await backend.write(path, new Uint8Array(0))
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
            initialViewState={paneState?.monacoViewState ?? null}
            onChange={(value) => useEditorStore.getState().updateContent(key, value)}
            onCursorChange={handleCursorChange}
            onViewStateChange={handleViewStateChange}
            onSave={handleSave}
          />
        ) : (
          <Suspense fallback={<div className="flex-1 flex items-center justify-center text-text-muted text-xs">Loading editor...</div>}>
            <TiptapEditor
              content={buffer.content}
              isActive={isActive}
              onChange={(md) => useEditorStore.getState().updateContent(key, md)}
              onSave={handleSave}
            />
          </Suspense>
        )}
      </div>
      <EditorStatusBar
        source={source}
        line={paneState?.cursorPosition.line ?? 1}
        column={paneState?.cursorPosition.column ?? 1}
        language={buffer.language}
        eol={buffer.eol}
        encoding={buffer.encoding}
        isMarkdown={isMarkdown}
        editorMode={effectiveEditorMode}
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
