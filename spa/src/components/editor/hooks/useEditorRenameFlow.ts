// spa/src/components/editor/hooks/useEditorRenameFlow.ts
//
// Owns the naming popover's submit path: the in-editor rename (in-app, remote and
// untitled variants) and the recent-files remap it has to keep in step. In `save`
// mode the popover is really a first save, so it delegates straight back to the
// save flow.
import { useCallback } from 'react'
import { useEditorStore } from '../../../stores/useEditorStore'
import { useTabStore } from '../../../stores/useTabStore'
import { useRecentFilesStore } from '../../../stores/useRecentFilesStore'
import { getFsBackend } from '../../../lib/fs-backend'
import { bufferKey } from '../../../lib/editor-buffer-key'
import { createMetadata } from '../../../lib/editor-language'
import { isCaseOnlyRename, isInvalidRename, renamePath, renameWarningMessage } from '../editor-pane-naming'
import type { RenamePopoverControls, RenamePopoverMode } from './useRenamePopoverState'
import type { FileSource } from '../../../types/fs'
import type { UntitledDocumentState } from '../../../types/tab'

export interface EditorRenameFlowArgs {
  key: string
  source: FileSource
  filePath: string
  currentName: string
  renameMode: RenamePopoverMode
  saveUntitledBuffer: (name: string) => Promise<void>
  popover: RenamePopoverControls
  t: (key: string, params?: Record<string, string>) => string
}

export interface EditorRenameFlow {
  handleRenameSubmit: (nextName: string) => Promise<void>
}

export function useEditorRenameFlow({
  key,
  source,
  filePath,
  currentName,
  renameMode,
  saveUntitledBuffer,
  popover,
  t,
}: EditorRenameFlowArgs): EditorRenameFlow {
  const handleRenameSubmit = useCallback(async (nextName: string) => {
    const currentBuffer = useEditorStore.getState().buffers[key]
    if (!currentBuffer) return

    if (renameMode === 'save') {
      await saveUntitledBuffer(nextName)
      return
    }

    if (isInvalidRename(nextName)) {
      popover.setWarning('Invalid file name')
      return
    }

    if (nextName === currentName) {
      popover.close()
      return
    }

    const nextPath = renamePath(filePath, nextName, currentBuffer.untitled)
    const nextKey = bufferKey(source, nextPath)
    if (nextKey !== key && useEditorStore.getState().buffers[nextKey]) {
      popover.setWarning('File already exists')
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
      popover.close()
      return
    }

    const backend = getFsBackend(source)
    // Same silent-failure class as T1.2b: without a backend the rename cannot
    // happen, and returning quietly dismissed the popover as if it had.
    if (!backend) {
      popover.setWarning(t('editor.load_error.no_backend'))
      return
    }

    if (!isCaseOnlyRename(filePath, nextPath)) {
      try {
        await backend.stat(nextPath)
        popover.setWarning('File already exists')
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
      // other two go through `applyPathMutation`), and the ONLY one a remote file
      // can take — `source` carries the daemon host, so the remap stays scoped
      // to that host's entries.
      useRecentFilesStore.getState().renamePath(source, filePath, nextPath)
      popover.close()
    } catch (error) {
      popover.setWarning(renameWarningMessage(error))
    }
  }, [currentName, filePath, key, popover, renameMode, saveUntitledBuffer, source, t])

  return { handleRenameSubmit }
}
