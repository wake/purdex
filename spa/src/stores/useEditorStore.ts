// spa/src/stores/useEditorStore.ts
import { create } from 'zustand'
import type { editor } from 'monaco-editor'

export type EditorMode = 'raw' | 'wysiwyg'

export interface EditorBuffer {
  content: string
  savedContent: string
  isDirty: boolean
  language: string
  lastStat: { mtime: number; size: number } | null
  modelId: string
}

export interface EditorPaneState {
  bufferKey: string
  editorMode: EditorMode
  showDiff: boolean
  cursorPosition: { line: number; column: number }
  monacoViewState: editor.ICodeEditorViewState | null
}

interface EditorState {
  buffers: Record<string, EditorBuffer>
  paneStates: Record<string, EditorPaneState>
  openBuffer: (key: string, content: string, language: string, stat?: { mtime: number; size: number }) => void
  attachPane: (paneId: string, bufferKey: string) => void
  updateContent: (key: string, content: string) => void
  markSaved: (key: string, stat?: { mtime: number; size: number }) => void
  renameBuffer: (oldKey: string, newKey: string, language?: string) => void
  closeBuffer: (key: string) => void
  closePane: (paneId: string, expectedBufferKey?: string) => void
  reloadBuffer: (key: string, content: string, stat?: { mtime: number; size: number }) => void
  setEditorMode: (paneId: string, mode: EditorMode) => void
  setShowDiff: (paneId: string, showDiff: boolean) => void
  updateCursor: (paneId: string, line: number, column: number) => void
  saveMonacoViewState: (paneId: string, viewState: editor.ICodeEditorViewState | null) => void
  clearAllBuffers: () => void
}

let nextModelId = 1

function createPaneState(bufferKey: string): EditorPaneState {
  return {
    bufferKey,
    editorMode: 'raw',
    showDiff: false,
    cursorPosition: { line: 1, column: 1 },
    monacoViewState: null,
  }
}

function createModelId(): string {
  const modelId = `editor-model-${nextModelId}`
  nextModelId += 1
  return modelId
}

export const useEditorStore = create<EditorState>()((set) => ({
  buffers: {},
  paneStates: {},

  openBuffer: (key, content, language, stat) => set((s) => {
    if (s.buffers[key]) return s
    return {
      buffers: {
        ...s.buffers,
        [key]: {
          content,
          savedContent: content,
          isDirty: false,
          language,
          lastStat: stat ?? null,
          modelId: createModelId(),
        },
      },
    }
  }),

  attachPane: (paneId, bufferKey) => set((s) => {
    const currentPaneState = s.paneStates[paneId]
    if (!currentPaneState) {
      return {
        paneStates: {
          ...s.paneStates,
          [paneId]: createPaneState(bufferKey),
        },
      }
    }
    if (currentPaneState.bufferKey === bufferKey) return s

    const nextPaneStates = {
      ...s.paneStates,
      [paneId]: createPaneState(bufferKey),
    }
    const stillReferenced = Object.entries(nextPaneStates).some(([otherPaneId, state]) =>
      otherPaneId !== paneId && state.bufferKey === currentPaneState.bufferKey,
    )
    if (stillReferenced) {
      return { paneStates: nextPaneStates }
    }

    const { [currentPaneState.bufferKey]: _removedBuffer, ...restBuffers } = s.buffers
    return {
      buffers: restBuffers,
      paneStates: nextPaneStates,
    }
  }),

  updateContent: (key, content) => set((s) => {
    const buf = s.buffers[key]
    if (!buf) return s
    return {
      buffers: {
        ...s.buffers,
        [key]: {
          ...buf,
          content,
          isDirty: content !== buf.savedContent,
        },
      },
    }
  }),

  markSaved: (key, stat) => set((s) => {
    const buf = s.buffers[key]
    if (!buf) return s
    return {
      buffers: {
        ...s.buffers,
        [key]: {
          ...buf,
          savedContent: buf.content,
          isDirty: false,
          lastStat: stat ?? buf.lastStat,
        },
      },
    }
  }),

  renameBuffer: (oldKey, newKey, language) => set((s) => {
    const buffer = s.buffers[oldKey]
    if (!buffer || oldKey === newKey) return s

    const { [oldKey]: _removed, ...restBuffers } = s.buffers
    const paneStates = Object.fromEntries(
      Object.entries(s.paneStates).map(([paneId, paneState]) => [
        paneId,
        paneState.bufferKey === oldKey ? { ...paneState, bufferKey: newKey } : paneState,
      ]),
    )

    return {
      buffers: {
        ...restBuffers,
        [newKey]: {
          ...buffer,
          language: language ?? buffer.language,
        },
      },
      paneStates,
    }
  }),

  closeBuffer: (key) => set((s) => {
    const { [key]: _removed, ...rest } = s.buffers  
    return { buffers: rest }
  }),

  closePane: (paneId, expectedBufferKey) => set((s) => {
    const paneState = s.paneStates[paneId]
    if (!paneState) return s
    if (expectedBufferKey && paneState.bufferKey !== expectedBufferKey) return s

    const { [paneId]: _removed, ...restPaneStates } = s.paneStates
    const stillReferenced = Object.values(restPaneStates).some((state) => state.bufferKey === paneState.bufferKey)
    if (stillReferenced) {
      return { paneStates: restPaneStates }
    }

    const { [paneState.bufferKey]: _removedBuffer, ...restBuffers } = s.buffers
    return {
      buffers: restBuffers,
      paneStates: restPaneStates,
    }
  }),

  reloadBuffer: (key, content, stat) => set((s) => {
    const buf = s.buffers[key]
    if (!buf) return s
    return {
      buffers: {
        ...s.buffers,
        [key]: {
          ...buf,
          content,
          savedContent: content,
          isDirty: false,
          lastStat: stat ?? buf.lastStat,
        },
      },
    }
  }),

  setEditorMode: (paneId, mode) => set((s) => {
    const paneState = s.paneStates[paneId]
    if (!paneState) return s
    return {
      paneStates: {
        ...s.paneStates,
        [paneId]: {
          ...paneState,
          editorMode: mode,
        },
      },
    }
  }),

  setShowDiff: (paneId, showDiff) => set((s) => {
    const paneState = s.paneStates[paneId]
    if (!paneState) return s
    return {
      paneStates: {
        ...s.paneStates,
        [paneId]: {
          ...paneState,
          showDiff,
        },
      },
    }
  }),

  updateCursor: (paneId, line, column) => set((s) => {
    const paneState = s.paneStates[paneId]
    if (!paneState) return s
    return {
      paneStates: {
        ...s.paneStates,
        [paneId]: {
          ...paneState,
          cursorPosition: { line, column },
        },
      },
    }
  }),

  saveMonacoViewState: (paneId, viewState) => set((s) => {
    const paneState = s.paneStates[paneId]
    if (!paneState) return s
    return {
      paneStates: {
        ...s.paneStates,
        [paneId]: {
          ...paneState,
          monacoViewState: viewState,
        },
      },
    }
  }),

  clearAllBuffers: () => {
    nextModelId = 1
    set({ buffers: {}, paneStates: {} })
  },
}))
