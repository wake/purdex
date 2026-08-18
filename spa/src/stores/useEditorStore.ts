// spa/src/stores/useEditorStore.ts
import { create } from 'zustand'
import type { editor } from 'monaco-editor'
import type { UntitledDocumentState } from '../types/tab'

export type EditorMode = 'raw' | 'wysiwyg'
export type EditorLanguageSource = 'extension' | 'template' | 'manual'
export type EditorEol = 'lf' | 'crlf'
export type EditorEncoding = 'utf8'

export interface EditorBufferMetadata {
  language: string
  languageSource: EditorLanguageSource
  eol: EditorEol
  encoding: EditorEncoding
  untitled?: UntitledDocumentState
}

export interface EditorBuffer extends EditorBufferMetadata {
  content: string
  savedContent: string
  isDirty: boolean
  lastStat: { mtime: number; size: number } | null
  modelId: string
  // Spec 2.4 — the shape of the file AS LOADED, so Live Mode can serialize back
  // into it. Deliberately not part of `EditorBufferMetadata`: `eol` there is
  // recomputed from the current draft on every `updateContent`, which is exactly
  // why it cannot answer "what did the file look like?". These are written only
  // by `openBuffer` / `reloadBuffer` and are immutable for the rest of the
  // buffer's life; keeping them out of the metadata type is also what stops a
  // future metadata caller from merging over them.
  sourceEol: EditorEol
  sourceTrailingNewline: boolean
  /**
   * Blank lines at the very start of the file. Tiptap drops them at parse time
   * and can never serialize them back, so without this record a file that opens
   * with a blank line cannot round-trip losslessly.
   */
  sourceLeadingBlankLines: number
}

/** The load-time half of `EditorBuffer`, as one movable unit. */
type SourceShape = Pick<EditorBuffer, 'sourceEol' | 'sourceTrailingNewline' | 'sourceLeadingBlankLines'>

export interface TiptapViewState {
  scrollTop: number
  selection: { type: 'text' | 'node'; from: number; to: number } | null
}

export interface EditorPaneState {
  bufferKey: string
  // null = no explicit user choice yet → resolve to the language default at
  // render time (markdown → 'wysiwyg' Live Mode, everything else → 'raw').
  // A concrete value means the user picked it; that survives remounts.
  editorMode: EditorMode | null
  showDiff: boolean
  cursorPosition: { line: number; column: number }
  monacoViewState: editor.ICodeEditorViewState | null
  tiptapViewState: TiptapViewState | null
}

interface EditorState {
  buffers: Record<string, EditorBuffer>
  paneStates: Record<string, EditorPaneState>
  openBuffer: (key: string, content: string, metadata: Partial<EditorBufferMetadata> & Pick<EditorBufferMetadata, 'language'>, stat?: { mtime: number; size: number }) => void
  attachPane: (paneId: string, bufferKey: string) => void
  updateContent: (key: string, content: string) => void
  markSaved: (key: string, stat?: { mtime: number; size: number }) => void
  renameBuffer: (oldKey: string, newKey: string, metadata?: Partial<EditorBufferMetadata>) => void
  setBufferLanguage: (key: string, language: string) => void
  closeBuffer: (key: string) => void
  closePane: (paneId: string, expectedBufferKey?: string) => void
  reloadBuffer: (key: string, content: string, stat?: { mtime: number; size: number }) => void
  setEditorMode: (paneId: string, mode: EditorMode) => void
  setShowDiff: (paneId: string, showDiff: boolean) => void
  updateCursor: (paneId: string, line: number, column: number) => void
  saveMonacoViewState: (paneId: string, viewState: editor.ICodeEditorViewState | null) => void
  saveTiptapViewState: (paneId: string, viewState: TiptapViewState | null) => void
  clearAllBuffers: () => void
}

let nextModelId = 1

function createPaneState(bufferKey: string): EditorPaneState {
  return {
    bufferKey,
    editorMode: null,
    showDiff: false,
    cursorPosition: { line: 1, column: 1 },
    monacoViewState: null,
    tiptapViewState: null,
  }
}

function createModelId(): string {
  const modelId = `editor-model-${nextModelId}`
  nextModelId += 1
  return modelId
}

function detectEol(content: string): EditorEol {
  return content.includes('\r\n') ? 'crlf' : 'lf'
}

/**
 * Blank lines before the first line with any content. A file made of nothing but
 * newlines has none by definition: counting them there would double up against
 * `sourceTrailingNewline`, which already accounts for that same text.
 */
function countLeadingBlankLines(content: string): number {
  const leading = /^(?:\r?\n)*/.exec(content)?.[0] ?? ''
  if (leading.length === content.length) return 0
  return (leading.match(/\n/g) ?? []).length
}

/**
 * The immutable-after-load half of the buffer (spec 2.4). Derived from the bytes
 * as loaded, never from the metadata override — a caller may declare `eol` for
 * the status bar, but the file's own shape is not up for negotiation.
 */
function detectSourceShape(content: string): SourceShape {
  return {
    sourceEol: detectEol(content),
    sourceTrailingNewline: content.endsWith('\n'),
    sourceLeadingBlankLines: countLeadingBlankLines(content),
  }
}

function normalizeMetadata(content: string, metadata: Partial<EditorBufferMetadata> & Pick<EditorBufferMetadata, 'language'>): EditorBufferMetadata {
  return {
    language: metadata.language,
    languageSource: metadata.languageSource ?? 'extension',
    eol: metadata.eol ?? detectEol(content),
    encoding: metadata.encoding ?? 'utf8',
    untitled: metadata.untitled,
  }
}

export const useEditorStore = create<EditorState>()((set) => ({
  buffers: {},
  paneStates: {},

  openBuffer: (key, content, metadata, stat) => set((s) => {
    if (s.buffers[key]) return s
    return {
      buffers: {
        ...s.buffers,
        [key]: {
          content,
          savedContent: content,
          isDirty: false,
          ...normalizeMetadata(content, metadata),
          ...detectSourceShape(content),
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
          eol: detectEol(content),
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

  renameBuffer: (oldKey, newKey, metadata) => set((s) => {
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
          ...metadata,
        },
      },
      paneStates,
    }
  }),

  setBufferLanguage: (key, language) => set((s) => {
    const buffer = s.buffers[key]
    if (!buffer) return s
    return {
      buffers: {
        ...s.buffers,
        [key]: {
          ...buffer,
          language,
          languageSource: 'manual',
        },
      },
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
          eol: detectEol(content),
          // A reload IS a load: the file on disk changed shape, so the shape the
          // serializer must write back changes with it.
          ...detectSourceShape(content),
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

  saveTiptapViewState: (paneId, viewState) => set((s) => {
    const paneState = s.paneStates[paneId]
    if (!paneState) return s
    return {
      paneStates: {
        ...s.paneStates,
        [paneId]: {
          ...paneState,
          tiptapViewState: viewState,
        },
      },
    }
  }),

  clearAllBuffers: () => {
    nextModelId = 1
    set({ buffers: {}, paneStates: {} })
  },
}))
