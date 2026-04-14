// spa/src/stores/useEditorStore.ts
import { create } from 'zustand'

export interface EditorBuffer {
  content: string
  savedContent: string
  isDirty: boolean
  language: string
  cursorPosition: { line: number; column: number }
  lastStat: { mtime: number; size: number } | null
}

interface EditorState {
  buffers: Record<string, EditorBuffer>
  openBuffer: (key: string, content: string, language: string, stat?: { mtime: number; size: number }) => void
  updateContent: (key: string, content: string) => void
  markSaved: (key: string) => void
  closeBuffer: (key: string) => void
  reloadBuffer: (key: string, content: string, stat?: { mtime: number; size: number }) => void
  updateCursor: (key: string, line: number, column: number) => void
  clearAllBuffers: () => void
}

export const useEditorStore = create<EditorState>()((set) => ({
  buffers: {},

  openBuffer: (key, content, language, stat) => set((s) => ({
    buffers: {
      ...s.buffers,
      [key]: {
        content,
        savedContent: content,
        isDirty: false,
        language,
        cursorPosition: { line: 1, column: 1 },
        lastStat: stat ?? null,
      },
    },
  })),

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

  markSaved: (key) => set((s) => {
    const buf = s.buffers[key]
    if (!buf) return s
    return {
      buffers: {
        ...s.buffers,
        [key]: {
          ...buf,
          savedContent: buf.content,
          isDirty: false,
        },
      },
    }
  }),

  closeBuffer: (key) => set((s) => {
    const { [key]: _removed, ...rest } = s.buffers // eslint-disable-line @typescript-eslint/no-unused-vars
    return { buffers: rest }
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

  updateCursor: (key, line, column) => set((s) => {
    const buf = s.buffers[key]
    if (!buf) return s
    return {
      buffers: {
        ...s.buffers,
        [key]: {
          ...buf,
          cursorPosition: { line, column },
        },
      },
    }
  }),

  clearAllBuffers: () => set({ buffers: {} }),
}))
