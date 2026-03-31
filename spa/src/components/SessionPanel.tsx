// spa/src/components/SessionPanel.tsx
import { useSessionStore } from '../stores/useSessionStore'
import { useAgentStore } from '../stores/useAgentStore'
import { useHostStore } from '../stores/useHostStore'
import { Terminal, Lightning, CircleDashed, GearSix, Circle, Spinner } from '@phosphor-icons/react'
import SessionStatusBadge from './SessionStatusBadge'
import { useI18nStore } from '../stores/useI18nStore'
import { compositeKey } from '../lib/composite-key'

function SessionIcon({ mode, code }: { mode: string; code: string }) {
  const props = { size: 16, 'data-testid': `session-icon-${code}` }
  switch (mode) {
    case 'stream': return <Lightning {...props} weight="fill" className="text-blue-400" />
    case 'jsonl': return <CircleDashed {...props} className="text-yellow-400" />
    default: return <Terminal {...props} className="text-text-secondary" />
  }
}

interface Props {
  onSettingsOpen?: () => void
  onSelectSession?: (code: string) => void
  activeSessionCode?: string | null
}

export default function SessionPanel({ onSettingsOpen, onSelectSession, activeSessionCode }: Props) {
  const t = useI18nStore((s) => s.t)
  const sessionsMap = useSessionStore((s) => s.sessions)
  const activeCode = useSessionStore((s) => s.activeCode)
  const setActive = useSessionStore((s) => s.setActive)
  const agentStatuses = useAgentStore((s) => s.statuses)
  const hosts = useHostStore((s) => s.hosts)
  const hostOrder = useHostStore((s) => s.hostOrder)
  const runtime = useHostStore((s) => s.runtime)

  function handleClick(hostId: string, code: string) {
    setActive(hostId, code)
    onSelectSession?.(code)
  }

  const isActiveSession = (s: { code: string }) =>
    activeSessionCode != null ? s.code === activeSessionCode : activeCode === s.code

  return (
    <div className="w-56 bg-surface-tertiary border-r border-border-subtle flex flex-col">
      <div className="p-3 flex-1 overflow-y-auto">
        <h2 className="text-xs uppercase text-text-secondary mb-3">{t('session.title')}</h2>
        <div className="space-y-3">
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
                  <div className="flex items-center gap-1.5 mb-1 px-1">
                    {hostRuntime?.status === 'reconnecting' ? (
                      <Spinner size={8} className="text-yellow-400 animate-spin" />
                    ) : (
                      <Circle
                        size={8}
                        weight="fill"
                        className={isOffline ? 'text-red-400' : 'text-green-400'}
                      />
                    )}
                    <span className="text-xs text-text-muted font-semibold truncate">{host.name}</span>
                  </div>
                )}

                {/* Sessions */}
                <div className="space-y-1">
                  {sessions.length === 0 && (
                    <p className="text-xs text-text-muted px-2">
                      {isOffline ? t('session.reconnecting') : t('session.empty')}
                    </p>
                  )}
                  {sessions.map((s) => {
                    const ck = compositeKey(hostId, s.code)
                    const status = agentStatuses[ck]
                    return (
                      <button
                        key={ck}
                        onClick={() => handleClick(hostId, s.code)}
                        disabled={!!isOffline}
                        className={`w-full text-left px-2 py-1.5 rounded text-sm cursor-pointer flex items-center gap-2 disabled:opacity-50 disabled:cursor-not-allowed ${
                          isActiveSession(s) ? 'bg-surface-secondary text-text-primary' : 'text-text-secondary hover:bg-surface-secondary/50'
                        }`}
                      >
                        <SessionIcon mode={s.mode} code={s.code} />
                        <span className="flex-1 truncate">{s.name}</span>
                        {status && <SessionStatusBadge status={status} />}
                        <span className="text-xs text-text-muted">{s.mode}</span>
                      </button>
                    )
                  })}
                </div>
              </div>
            )
          })}
        </div>
      </div>
      <div className="p-3 border-t border-border-subtle">
        <button
          data-testid="settings-btn"
          onClick={onSettingsOpen}
          className="flex items-center gap-2 text-text-secondary hover:text-text-primary text-sm cursor-pointer w-full"
        >
          <GearSix size={16} />
          <span>{t('session.settings')}</span>
        </button>
      </div>
    </div>
  )
}
