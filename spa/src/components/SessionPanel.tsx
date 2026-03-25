// spa/src/components/SessionPanel.tsx
import { useSessionStore } from '../stores/useSessionStore'
import { useStreamStore } from '../stores/useStreamStore'
import { Terminal, Lightning, CircleDashed, GearSix } from '@phosphor-icons/react'
import SessionStatusBadge, { type SessionStatus } from './SessionStatusBadge'
import { useI18nStore } from '../stores/useI18nStore'

function SessionIcon({ mode, code }: { mode: string; code: string }) {
  const props = { size: 16, 'data-testid': `session-icon-${code}` }
  switch (mode) {
    case 'stream': return <Lightning {...props} weight="fill" className="text-blue-400" />
    case 'jsonl': return <CircleDashed {...props} className="text-yellow-400" />
    default: return <Terminal {...props} className="text-text-secondary" />
  }
}

function deriveStatus(mode: string): SessionStatus {
  switch (mode) {
    case 'stream': return 'cc-running'
    case 'jsonl': return 'cc-running'
    default: return 'not-in-cc'
  }
}

function mapStatus(raw: string): SessionStatus {
  switch (raw) {
    case 'cc-idle': return 'cc-idle'
    case 'cc-running': return 'cc-running'
    case 'cc-waiting': return 'cc-waiting'
    case 'cc-unread': return 'cc-unread'
    case 'not-in-cc': return 'not-in-cc'
    default: return 'normal'
  }
}

interface Props {
  onSettingsOpen?: () => void
  onSelectSession?: (code: string) => void
  activeSessionCode?: string | null
}

export default function SessionPanel({ onSettingsOpen, onSelectSession, activeSessionCode }: Props) {
  const t = useI18nStore((s) => s.t)
  const { sessions, activeId, setActive } = useSessionStore()
  const sessionStatus = useStreamStore((s) => s.sessionStatus)

  function handleClick(code: string) {
    setActive(code)
    onSelectSession?.(code)
  }

  const isActive = (s: { code: string }) =>
    activeSessionCode != null ? s.code === activeSessionCode : activeId === s.code

  return (
    <div className="w-56 bg-surface-tertiary border-r border-border-subtle flex flex-col">
      <div className="p-3 flex-1 overflow-y-auto">
        <h2 className="text-xs uppercase text-text-secondary mb-3">{t('session.title')}</h2>
        <div className="space-y-1">
          {sessions.length === 0 && <p className="text-sm text-text-muted">{t('session.empty')}</p>}
          {sessions.map((s) => {
            const status = sessionStatus[s.code]
              ? mapStatus(sessionStatus[s.code])
              : deriveStatus(s.mode)
            return (
              <button
                key={s.code}
                onClick={() => handleClick(s.code)}
                className={`w-full text-left px-2 py-1.5 rounded text-sm cursor-pointer flex items-center gap-2 ${
                  isActive(s) ? 'bg-surface-secondary text-text-primary' : 'text-text-secondary hover:bg-surface-secondary/50'
                }`}
              >
                <SessionIcon mode={s.mode} code={s.code} />
                <SessionStatusBadge status={status} />
                <span className="flex-1 truncate">{s.name}</span>
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
