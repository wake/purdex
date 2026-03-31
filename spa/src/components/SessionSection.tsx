import { useSessionStore } from '../stores/useSessionStore'
import { useHostStore } from '../stores/useHostStore'
import { useI18nStore } from '../stores/useI18nStore'
import type { NewTabProviderProps } from '../lib/new-tab-registry'
import { TerminalWindow, Circle, Spinner } from '@phosphor-icons/react'

export function SessionSection({ onSelect }: NewTabProviderProps) {
  const sessionsMap = useSessionStore((s) => s.sessions)
  const hosts = useHostStore((s) => s.hosts)
  const hostOrder = useHostStore((s) => s.hostOrder)
  const runtime = useHostStore((s) => s.runtime)
  const t = useI18nStore((s) => s.t)

  const hasAnySessions = hostOrder.some((hid) => (sessionsMap[hid] ?? []).length > 0)

  if (!hasAnySessions) {
    return <p className="text-sm text-text-muted px-2">{t('session.no_sessions')}</p>
  }

  return (
    <div className="flex flex-col gap-1">
      {hostOrder.map((hostId) => {
        const host = hosts[hostId]
        if (!host) return null
        const sessions = sessionsMap[hostId] ?? []
        const hostRuntime = runtime[hostId]
        const isOffline = hostRuntime && hostRuntime.status !== 'connected'

        return (
          <div key={hostId}>
            {/* Host header — only show when multiple hosts */}
            {hostOrder.length > 1 && (
              <div className="flex items-center gap-1.5 px-3 py-1 mt-1">
                {hostRuntime?.status === 'reconnecting' ? (
                  <Spinner size={8} className="text-yellow-400 animate-spin" />
                ) : hostRuntime?.status === 'connected' ? (
                  <Circle size={8} weight="fill" className="text-green-400" />
                ) : hostRuntime ? (
                  <Circle size={8} weight="fill" className="text-red-400" />
                ) : (
                  <Circle size={8} weight="fill" className="text-text-muted" />
                )}
                <span className="text-xs text-text-muted font-semibold">{host.name}</span>
                {isOffline && (
                  <span className="text-xs text-text-muted ml-auto">{t('session.reconnecting')}</span>
                )}
              </div>
            )}
            {sessions.map((session) => (
              <button
                key={`${hostId}:${session.code}`}
                className="flex items-center gap-3 px-3 py-2 rounded-md hover:bg-white/10 text-left text-sm text-text-primary cursor-pointer transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
                disabled={!!isOffline}
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
      })}
    </div>
  )
}
