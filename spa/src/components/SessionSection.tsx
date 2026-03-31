import { useSessionStore } from '../stores/useSessionStore'
import { useHostStore } from '../stores/useHostStore'
import type { NewTabProviderProps } from '../lib/new-tab-registry'
import { TerminalWindow } from '@phosphor-icons/react'

export function SessionSection({ onSelect }: NewTabProviderProps) {
  const sessions = useSessionStore((s) => s.sessions)
  const hostId = useHostStore((s) => s.hostOrder[0])

  if (sessions.length === 0) {
    return <p className="text-sm text-text-muted px-2">No sessions available</p>
  }

  return (
    <div className="flex flex-col gap-1">
      {sessions.map((session) => (
        <button
          key={session.code}
          className="flex items-center gap-3 px-3 py-2 rounded-md hover:bg-white/10 text-left text-sm text-text-primary cursor-pointer transition-colors"
          onClick={() =>
            onSelect({ kind: 'session', hostId, sessionCode: session.code, mode: 'terminal' })
          }
        >
          <TerminalWindow size={16} className="text-text-secondary flex-shrink-0" />
          <span className="truncate">{session.name}</span>
          <span className="text-xs text-text-secondary ml-auto">{session.code}</span>
        </button>
      ))}
    </div>
  )
}
