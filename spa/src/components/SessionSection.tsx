import { useSessionStore } from '../stores/useSessionStore'
import type { NewTabProviderProps } from '../lib/new-tab-registry'
import { TerminalWindow } from '@phosphor-icons/react'

export function SessionSection({ onSelect }: NewTabProviderProps) {
  const sessions = useSessionStore((s) => s.sessions)

  if (sessions.length === 0) {
    return <p className="text-sm text-gray-600 px-2">No sessions available</p>
  }

  return (
    <div className="flex flex-col gap-1">
      {sessions.map((session) => (
        <button
          key={session.code}
          className="flex items-center gap-3 px-3 py-2 rounded-md hover:bg-white/10 text-left text-sm text-gray-300 cursor-pointer transition-colors"
          onClick={() =>
            onSelect({ kind: 'session', sessionCode: session.code, mode: 'terminal' })
          }
        >
          <TerminalWindow size={16} className="text-gray-400 flex-shrink-0" />
          <span className="truncate">{session.name}</span>
          <span className="text-xs text-gray-400 ml-auto">{session.code}</span>
        </button>
      ))}
    </div>
  )
}
