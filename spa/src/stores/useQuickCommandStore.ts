import { create } from 'zustand'
import { persist } from 'zustand/middleware'
import { purdexStorage, STORAGE_KEYS, syncManager } from '../lib/storage'

export interface QuickCommand {
  id: string
  name: string
  command: string
  icon?: string
  category?: string
  hostOnly?: boolean
}

interface QuickCommandState {
  global: QuickCommand[]
  byHost: Record<string, QuickCommand[]>

  addCommand: (cmd: QuickCommand, hostId?: string) => void
  updateCommand: (id: string, patch: Partial<QuickCommand>, hostId?: string) => void
  removeCommand: (id: string, hostId?: string) => void
  getCommands: (hostId: string) => QuickCommand[]
}

const DEFAULT_COMMANDS: QuickCommand[] = [
  { id: 'start-cc', name: 'Start Claude Code', command: 'claude -p --verbose --output-format stream-json', category: 'agent' },
  { id: 'start-codex', name: 'Start Codex', command: 'codex', category: 'agent' },
]

export const useQuickCommandStore = create<QuickCommandState>()(
  persist(
    (set, get) => ({
      global: DEFAULT_COMMANDS,
      byHost: {},

      addCommand: (cmd, hostId) =>
        set((state) => {
          if (hostId) {
            const hostCmds = [...(state.byHost[hostId] ?? []), cmd]
            return { byHost: { ...state.byHost, [hostId]: hostCmds } }
          }
          return { global: [...state.global, cmd] }
        }),

      updateCommand: (id, patch, hostId) =>
        set((state) => {
          const update = (cmds: QuickCommand[]) =>
            cmds.map((c) => (c.id === id ? { ...c, ...patch } : c))
          if (hostId) {
            const hostCmds = update(state.byHost[hostId] ?? [])
            return { byHost: { ...state.byHost, [hostId]: hostCmds } }
          }
          return { global: update(state.global) }
        }),

      removeCommand: (id, hostId) =>
        set((state) => {
          if (hostId) {
            const hostCmds = (state.byHost[hostId] ?? []).filter((c) => c.id !== id)
            return { byHost: { ...state.byHost, [hostId]: hostCmds } }
          }
          return { global: state.global.filter((c) => c.id !== id) }
        }),

      getCommands: (hostId) => {
        const { global, byHost } = get()
        const hostCmds = byHost[hostId] ?? []
        if (hostCmds.length === 0) return global

        const merged = [
          ...global.map((g) => {
            const override = hostCmds.find((h) => h.id === g.id)
            return override ?? g
          }),
          ...hostCmds.filter((h) => !global.some((g) => g.id === h.id)),
        ]
        return merged
      },
    }),
    {
      name: STORAGE_KEYS.QUICK_COMMANDS,
      storage: purdexStorage,
      version: 1,
      partialize: (state) => ({
        global: state.global,
        byHost: state.byHost,
      }),
    },
  ),
)

syncManager.register(STORAGE_KEYS.QUICK_COMMANDS, useQuickCommandStore)
