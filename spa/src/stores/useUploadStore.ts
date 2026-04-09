import { create } from 'zustand'
import { compositeKey } from '../lib/composite-key'

interface SessionUploadState {
  total: number
  completed: number
  failed: number
  currentFile: string
  error?: string
  status: 'uploading' | 'typing' | 'done' | 'error'
}

interface UploadState {
  sessions: Record<string, SessionUploadState>
  startUpload: (hostId: string, sessionCode: string, total: number, firstFile: string) => void
  fileCompleted: (hostId: string, sessionCode: string) => void
  fileFailed: (hostId: string, sessionCode: string, filename: string) => void
  nextFile: (hostId: string, sessionCode: string, filename: string) => void
  setDone: (hostId: string, sessionCode: string) => void
  dismiss: (hostId: string, sessionCode: string) => void
}

export const useUploadStore = create<UploadState>((set) => ({
  sessions: {},

  startUpload: (hostId, sessionCode, total, firstFile) => {
    const key = compositeKey(hostId, sessionCode)
    set((s) => ({
      sessions: {
        ...s.sessions,
        [key]: { total, completed: 0, failed: 0, currentFile: firstFile, status: 'uploading' },
      },
    }))
  },

  fileCompleted: (hostId, sessionCode) => {
    const key = compositeKey(hostId, sessionCode)
    set((s) => {
      const prev = s.sessions[key]
      if (!prev) return s
      const completed = prev.completed + 1
      const allDone = completed + prev.failed >= prev.total
      return {
        sessions: {
          ...s.sessions,
          [key]: {
            ...prev,
            completed,
            status: allDone ? (prev.failed > 0 ? 'error' : 'typing') : 'uploading',
          },
        },
      }
    })
  },

  fileFailed: (hostId, sessionCode, filename) => {
    const key = compositeKey(hostId, sessionCode)
    set((s) => {
      const prev = s.sessions[key]
      if (!prev) return s
      const failed = prev.failed + 1
      const allDone = prev.completed + failed >= prev.total
      return {
        sessions: {
          ...s.sessions,
          [key]: {
            ...prev,
            failed,
            error: filename,
            status: allDone ? 'error' : 'uploading',
          },
        },
      }
    })
  },

  nextFile: (hostId, sessionCode, filename) => {
    const key = compositeKey(hostId, sessionCode)
    set((s) => {
      const prev = s.sessions[key]
      if (!prev) return s
      return {
        sessions: { ...s.sessions, [key]: { ...prev, currentFile: filename } },
      }
    })
  },

  setDone: (hostId, sessionCode) => {
    const key = compositeKey(hostId, sessionCode)
    set((s) => {
      const prev = s.sessions[key]
      if (!prev || prev.status !== 'typing') return s
      return {
        sessions: { ...s.sessions, [key]: { ...prev, status: 'done' } },
      }
    })
  },

  dismiss: (hostId, sessionCode) => {
    const key = compositeKey(hostId, sessionCode)
    set((s) => {
      const prev = s.sessions[key]
      // Don't dismiss while uploading — prevents concurrent drop from corrupting state.
      if (prev?.status === 'uploading') return s
      // eslint-disable-next-line @typescript-eslint/no-unused-vars
      const { [key]: _, ...rest } = s.sessions
      return { sessions: rest }
    })
  },
}))
