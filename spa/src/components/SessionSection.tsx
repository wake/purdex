import { useState } from 'react'
import { useSessionStore } from '../stores/useSessionStore'
import { useHostStore } from '../stores/useHostStore'
import { useI18nStore } from '../stores/useI18nStore'
import { useSessionWatch } from '../hooks/useSessionWatch'
import { useSessionAgentIndicator } from '../hooks/useSessionAgentIndicator'
import { TabIcon } from './TabIcon'
import type { NewTabProviderProps } from '../lib/new-tab-registry'
import { isHostLive } from '../lib/host-live'
import { useHostLook } from '../lib/host-look'
import type { Session } from '../lib/host-api'
import { SessionLauncher } from './session-launcher/SessionLauncher'
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
        onSelect({ kind: 'tmux-session', hostId, sessionCode: session.code, mode: 'terminal', cachedName: session.name, tmuxInstance: session.tmux_instance ?? '' })
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
      {/* Cap the name at half-width ONLY when a pane title shares the row, so
          the title keeps room; with no title a long name uses the full width. */}
      <span className={`truncate${session.pane_title ? ' max-w-[50%]' : ''}`}>{session.name}</span>
      <span className="text-xs text-text-secondary flex-shrink-0">{session.code}</span>
      {session.pane_title && (
        <span className="text-xs text-text-secondary truncate min-w-0 flex-1">{session.pane_title}</span>
      )}
    </button>
  )
}

const EMPTY_SESSIONS: Session[] = []

export interface HostSessionSectionProps extends NewTabProviderProps {
  hostId: string
}

/** One host's sessions block on the New Tab page (provider id `sessions:<hostId>`). */
export function HostSessionSection({ hostId, onSelect }: HostSessionSectionProps) {
  useSessionWatch()
  const host = useHostStore((s) => s.hosts[hostId])
  const look = useHostLook(hostId)
  const hostRuntime = useHostStore((s) => s.runtime[hostId])
  const sessions = useSessionStore((s) => s.sessions[hostId]) ?? EMPTY_SESSIONS
  const t = useI18nStore((s) => s.t)
  const [isExpanded, setExpanded] = useState(true)
  const [creating, setCreating] = useState(false)

  if (!host) return null

  const isOffline = hostRuntime && hostRuntime.status !== 'connected'
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

  // data-session-list scopes j/k / arrow navigation to this host's block.
  return (
    <div className="flex flex-col gap-1" data-session-list>
      <div className="flex items-center gap-1.5 px-3 py-1 mt-1 w-full">
        <button
          data-testid={`host-header-${hostId}`}
          aria-expanded={isExpanded}
          onClick={() => setExpanded(!isExpanded)}
          className="flex items-center gap-1.5 min-w-0 cursor-pointer"
        >
          {isExpanded
            ? <CaretDown size={12} className="text-text-secondary hover:text-text-primary" />
            : <CaretRight size={12} className="text-text-secondary hover:text-text-primary" />}
          {statusDot}
          <span className="text-sm font-bold text-text-primary truncate">{look.name}</span>
        </button>
        {isOffline && (
          <span className="text-xs text-text-muted">{t('session.reconnecting')}</span>
        )}
        <button
          data-testid={`new-session-${hostId}`}
          disabled={createDisabled}
          onClick={() => {
            const opening = !creating
            setCreating(opening)
            // Opening on a collapsed host must reveal the launcher (which is
            // gated behind isExpanded) - expand so the "+" isn't a no-op.
            if (opening) setExpanded(true)
          }}
          className="ml-auto p-1 rounded bg-accent text-white hover:bg-accent/80 cursor-pointer disabled:opacity-40 disabled:cursor-not-allowed"
          title={t('hosts.new_session')}
        >
          <Plus size={14} weight="bold" />
        </button>
      </div>
      {isExpanded && creating && (
        <div className="mx-3 my-1">
          <SessionLauncher
            hostId={hostId}
            disabled={createDisabled}
            onCancel={() => setCreating(false)}
            onLaunched={(session) => {
              // Backstop: the launcher only hands over a session whose host is
              // still live, but attaching to a host removed in between would
              // bind the pane to a dead daemon. Closing follows the attach —
              // never before it — so a refusal here cannot make the launcher
              // vanish with neither a pane nor a word.
              if (!isHostLive(hostId)) return
              setCreating(false)
              onSelect({
                kind: 'tmux-session',
                hostId,
                sessionCode: session.code,
                mode: 'terminal',
                cachedName: session.name,
                // Generation from the create response itself (spec §4.5 of tab
                // rebuild); '' on old daemons, adopted from the next payload.
                tmuxInstance: session.tmux_instance ?? '',
              })
            }}
          />
        </div>
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
}
