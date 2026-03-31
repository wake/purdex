import { useMemo } from 'react'
import { useSessionStore } from '../stores/useSessionStore'
import type { NewTabProviderProps } from '../lib/new-tab-registry'
import { TerminalWindow } from '@phosphor-icons/react'

export function SessionSection({ onSelect }: NewTabProviderProps) {
  const sessionsMap = useSessionStore((s) => s.sessions)
  // Flatten all hosts' sessions for the new-tab picker
  const sessions = useMemo(() => {
    const result: Array<{ hostId: string; code: string; name: string }> = []
    for (const [hostId, list] of Object.entries(sessionsMap)) {
      for (const s of list) {
        result.push({ hostId, code: s.code, name: s.name })
      }
    }
    return result
  }, [sessionsMap])

  if (sessions.length === 0) {
    return <p className="text-sm text-text-muted px-2">No sessions available</p>
  }

  return (
    <div className="flex flex-col gap-1">
      {sessions.map((session) => (
        <button
          key={`${session.hostId}:${session.code}`}
          className="flex items-center gap-3 px-3 py-2 rounded-md hover:bg-white/10 text-left text-sm text-text-primary cursor-pointer transition-colors"
          onClick={() =>
            onSelect({ kind: 'session', hostId: session.hostId, sessionCode: session.code, mode: 'terminal' })
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
