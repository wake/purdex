import { create } from 'zustand'

interface SessionUploadState {
  total: number
  completed: number
  failed: number
  currentFile: string
  error?: string
  status: 'uploading' | 'done' | 'error'
}

interface UploadState {
  sessions: Record<string, SessionUploadState>
  startUpload: (session: string, total: number, firstFile: string) => void
  fileCompleted: (session: string) => void
  fileFailed: (session: string, filename: string) => void
  nextFile: (session: string, filename: string) => void
  dismiss: (session: string) => void
}

export const useUploadStore = create<UploadState>((set) => ({
  sessions: {},

  startUpload: (session, total, firstFile) =>
    set((s) => ({
      sessions: {
        ...s.sessions,
        [session]: { total, completed: 0, failed: 0, currentFile: firstFile, status: 'uploading' },
      },
    })),

  fileCompleted: (session) =>
    set((s) => {
      const prev = s.sessions[session]
      if (!prev) return s
      const completed = prev.completed + 1
      const allDone = completed + prev.failed >= prev.total
      return {
        sessions: {
          ...s.sessions,
          [session]: {
            ...prev,
            completed,
            status: allDone ? (prev.failed > 0 ? 'error' : 'done') : 'uploading',
          },
        },
      }
    }),

  fileFailed: (session, filename) =>
    set((s) => {
      const prev = s.sessions[session]
      if (!prev) return s
      const failed = prev.failed + 1
      const allDone = prev.completed + failed >= prev.total
      return {
        sessions: {
          ...s.sessions,
          [session]: {
            ...prev,
            failed,
            error: filename,
            status: allDone ? 'error' : 'uploading',
          },
        },
      }
    }),

  nextFile: (session, filename) =>
    set((s) => {
      const prev = s.sessions[session]
      if (!prev) return s
      return {
        sessions: { ...s.sessions, [session]: { ...prev, currentFile: filename } },
      }
    }),

  dismiss: (session) =>
    set((s) => {
      // eslint-disable-next-line @typescript-eslint/no-unused-vars
      const { [session]: _, ...rest } = s.sessions
      return { sessions: rest }
    }),
}))
