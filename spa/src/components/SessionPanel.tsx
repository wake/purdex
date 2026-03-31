// spa/src/components/SessionPanel.tsx
import { useMemo } from 'react'
import { useSessionStore } from '../stores/useSessionStore'
import { useAgentStore } from '../stores/useAgentStore'
import { Terminal, Lightning, CircleDashed, GearSix } from '@phosphor-icons/react'
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

  // Flatten all hosts' sessions for display (multi-host grouping comes in Task 17)
  const sessions = useMemo(() => {
    const result: Array<{ hostId: string; code: string; name: string; cwd: string; mode: string; cc_session_id: string; cc_model: string; has_relay: boolean }> = []
    for (const [hostId, list] of Object.entries(sessionsMap)) {
      for (const s of list) {
        result.push({ ...s, hostId })
      }
    }
    return result
  }, [sessionsMap])

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
        <div className="space-y-1">
          {sessions.length === 0 && <p className="text-sm text-text-muted">{t('session.empty')}</p>}
          {sessions.map((s) => {
            const ck = compositeKey(s.hostId, s.code)
            const status = agentStatuses[ck]
            return (
              <button
                key={ck}
                onClick={() => handleClick(s.hostId, s.code)}
                className={`w-full text-left px-2 py-1.5 rounded text-sm cursor-pointer flex items-center gap-2 ${
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
