import { useState, useRef } from 'react'
import { useSessionStore } from '../stores/useSessionStore'
import { useHostStore } from '../stores/useHostStore'
import { useI18nStore } from '../stores/useI18nStore'
import { useSessionWatch } from '../hooks/useSessionWatch'
import { useSessionAgentIndicator } from '../hooks/useSessionAgentIndicator'
import { TabIcon } from './TabIcon'
import type { NewTabProviderProps } from '../lib/new-tab-registry'
import { createSession } from '../lib/host-api'
import type { Session } from '../lib/host-api'
import { TerminalWindow, Circle, Spinner, CaretDown, CaretRight, Plus } from '@phosphor-icons/react'

function SessionRow({ hostId, session, disabled, onSelect }: {
  hostId: string
  session: Session
  disabled: boolean
  onSelect: NewTabProviderProps['onSelect']
}) {
  const { agentIcon, agentStatus, subagentRefs, isUnread, tabIndicatorStyle } =
    useSessionAgentIndicator(hostId, session.code)
  const IconComponent = agentIcon ?? TerminalWindow
  return (
    <button
      data-session-btn
      className="flex items-center gap-3 px-3 py-2 rounded-md hover:bg-white/10 text-left text-sm text-text-primary cursor-pointer transition-colors disabled:opacity-50 disabled:cursor-not-allowed focus:outline-none focus:ring-1 focus:ring-accent-muted"
      disabled={disabled}
      tabIndex={0}
      onClick={() =>
        onSelect({ kind: 'tmux-session', hostId, sessionCode: session.code, mode: 'terminal', cachedName: session.name, tmuxInstance: '' })
      }
      onKeyDown={(e) => {
        const container = e.currentTarget.closest('[data-session-list]')
        if (!container) return
        const buttons = Array.from(container.querySelectorAll('button[data-session-btn]:not(:disabled)')) as HTMLElement[]
        const currentIndex = buttons.indexOf(e.currentTarget)
        if (currentIndex === -1) return
        switch (e.key) {
          case 'ArrowDown':
          case 'j':
            e.preventDefault()
            buttons[Math.min(currentIndex + 1, buttons.length - 1)]?.focus()
            break
          case 'ArrowUp':
          case 'k':
            e.preventDefault()
            buttons[Math.max(currentIndex - 1, 0)]?.focus()
            break
        }
      }}
    >
      <span className="relative inline-flex items-center justify-center w-4 h-4 flex-shrink-0">
        <TabIcon IconComponent={IconComponent} agentStatus={agentStatus} tabIndicatorStyle={tabIndicatorStyle} isActive={false} iconSize={14} subagentRefs={subagentRefs} isUnread={isUnread} />
      </span>
      <span className="truncate">{session.name}</span>
      <span className="text-xs text-text-secondary ml-auto">{session.code}</span>
    </button>
  )
}

function NewTabSessionForm({ hostId, onCreated, onCancel }: {
  hostId: string
  onCreated: (content: { code: string; name: string; mode: string }) => void
  onCancel: () => void
}) {
  const t = useI18nStore((s) => s.t)
  const [name, setName] = useState('')
  const [cwd, setCwd] = useState('~')
  const [mode, setMode] = useState('terminal')
  const [creating, setCreating] = useState(false)
  const [error, setError] = useState('')
  const creatingRef = useRef(false) // synchronous double-submit guard (fires before `creating` state commits)

  const disabledSubmit = creating || !name.trim()

  const handleCreate = async () => {
    if (creatingRef.current || !name.trim()) return
    creatingRef.current = true
    setCreating(true); setError('')
    try {
      const created = await createSession(hostId, name.trim(), cwd, mode)
      // Guard 1 — blank code = failed create.
      if (!created.code) { setError(t('hosts.create') + ' failed'); return }
      // Guard 2 — host still live (removed/disconnected during the await must not attach).
      const rt = useHostStore.getState().runtime[hostId]
      const live = !!useHostStore.getState().hosts[hostId] && !!rt && rt.status === 'connected' && rt.tmuxState !== 'unavailable'
      if (!live) { setError(t('hosts.create') + ' failed'); return }
      onCreated({ code: created.code, name: created.name, mode })
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed')
    } finally {
      creatingRef.current = false
      setCreating(false)
    }
  }

  return (
    <div className="mx-3 my-1 p-2 bg-surface-secondary border border-border-default rounded-md">
      <input placeholder={t('hosts.session_name')} value={name} autoFocus
        onChange={(e) => setName(e.target.value)}
        onKeyDown={(e) => { if (e.key === 'Enter') handleCreate() }}
        className="w-full bg-surface-primary border border-border-default rounded px-2 py-1 text-sm text-text-primary mb-1" />
      <input placeholder={t('hosts.session_cwd')} value={cwd}
        onChange={(e) => setCwd(e.target.value)}
        className="w-full bg-surface-primary border border-border-default rounded px-2 py-1 text-sm text-text-muted mb-1" />
      <select value={mode} onChange={(e) => setMode(e.target.value)}
        className="bg-surface-primary border border-border-default rounded px-2 py-1 text-sm text-text-primary">
        <option value="terminal">terminal</option>
        <option value="stream">stream</option>
      </select>
      {error && <p className="text-xs text-red-400 mt-1">{error}</p>}
      <div className="flex gap-2 mt-2">
        <button onClick={handleCreate} disabled={disabledSubmit}
          className="px-2 py-1 rounded text-xs bg-accent text-white cursor-pointer disabled:opacity-50">{t('hosts.create')}</button>
        <button onClick={onCancel}
          className="px-2 py-1 rounded text-xs bg-surface-tertiary text-text-secondary cursor-pointer">{t('common.cancel')}</button>
      </div>
    </div>
  )
}

export function SessionSection({ onSelect }: NewTabProviderProps) {
  useSessionWatch()
  const sessionsMap = useSessionStore((s) => s.sessions)
  const hosts = useHostStore((s) => s.hosts)
  const hostOrder = useHostStore((s) => s.hostOrder)
  const runtime = useHostStore((s) => s.runtime)
  const t = useI18nStore((s) => s.t)
  const [expanded, setExpanded] = useState<Record<string, boolean>>({})
  const [creatingHost, setCreatingHost] = useState<string | null>(null)

  if (hostOrder.length === 0) {
    return <p className="text-sm text-text-muted px-2">{t('session.no_sessions')}</p>
  }

  return (
    <div className="flex flex-col gap-1" data-session-list>
      {hostOrder.map((hostId) => {
        const host = hosts[hostId]
        if (!host) return null
        const sessions = sessionsMap[hostId] ?? []
        const hostRuntime = runtime[hostId]
        const isOffline = hostRuntime && hostRuntime.status !== 'connected'
        const isExpanded = expanded[hostId] !== false
        const createDisabled = !hostRuntime || hostRuntime.status !== 'connected' || hostRuntime.tmuxState === 'unavailable'

        const statusDot = hostRuntime?.status === 'reconnecting' ? (
          <Spinner size={8} className="text-yellow-400 animate-spin" />
        ) : hostRuntime?.status === 'connected' ? (
          <Circle size={8} weight="fill" className="text-green-400" />
        ) : hostRuntime ? (
          <Circle size={8} weight="fill" className="text-red-400" />
        ) : (
          <Circle size={8} weight="fill" className="text-text-muted" />
        )

        return (
          <div key={hostId}>
            <div className="flex items-center gap-1.5 px-3 py-1 mt-1 w-full">
              {hostOrder.length > 1 ? (
                <button
                  data-testid={`host-header-${hostId}`}
                  aria-expanded={isExpanded}
                  onClick={() => setExpanded((prev) => ({ ...prev, [hostId]: !isExpanded }))}
                  className="flex items-center gap-1.5 flex-1 min-w-0 cursor-pointer"
                >
                  {isExpanded ? <CaretDown size={10} className="text-text-muted" /> : <CaretRight size={10} className="text-text-muted" />}
                  {statusDot}
                  <span className="text-xs text-text-muted font-semibold">{host.name}</span>
                  {isOffline && (
                    <span className="text-xs text-text-muted ml-auto">{t('session.reconnecting')}</span>
                  )}
                </button>
              ) : (
                <span className="flex items-center gap-1.5 flex-1 min-w-0">
                  {statusDot}
                  <span className="text-xs text-text-muted font-semibold">{host.name}</span>
                  {isOffline && (
                    <span className="text-xs text-text-muted ml-auto">{t('session.reconnecting')}</span>
                  )}
                </span>
              )}
              <button
                data-testid={`new-session-${hostId}`}
                disabled={createDisabled}
                onClick={() => setCreatingHost((h) => (h === hostId ? null : hostId))}
                className="ml-auto p-1 rounded hover:bg-white/10 text-text-muted hover:text-text-primary cursor-pointer disabled:opacity-40 disabled:cursor-not-allowed"
                title={t('hosts.new_session')}
              >
                <Plus size={14} />
              </button>
            </div>
            {isExpanded && creatingHost === hostId && (
              <NewTabSessionForm
                hostId={hostId}
                onCancel={() => setCreatingHost(null)}
                onCreated={({ code, name, mode }) => {
                  setCreatingHost(null)
                  onSelect({ kind: 'tmux-session', hostId, sessionCode: code, mode: mode as 'terminal' | 'stream', cachedName: name, tmuxInstance: '' })
                }}
              />
            )}
            {isExpanded && sessions.map((session) => (
              <SessionRow
                key={`${hostId}:${session.code}`}
                hostId={hostId}
                session={session}
                disabled={!!isOffline}
                onSelect={onSelect}
              />
            ))}
          </div>
        )
      })}
    </div>
  )
}
