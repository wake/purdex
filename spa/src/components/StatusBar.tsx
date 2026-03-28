import { useState, useRef, useCallback } from 'react'
import { CaretUp } from '@phosphor-icons/react'
import type { Tab } from '../types/tab'
import { getPrimaryPane } from '../lib/pane-tree'
import { useSessionStore } from '../stores/useSessionStore'
import { useHostStore } from '../stores/useHostStore'
import { useAgentStore, getAgentLabel } from '../stores/useAgentStore'
import { useClickOutside } from '../hooks/useClickOutside'
import { useI18nStore } from '../stores/useI18nStore'

interface Props {
  activeTab: Tab | null
  onViewModeChange?: (tabId: string, paneId: string, mode: 'terminal' | 'stream') => void
}

const VIEW_MODE_COLORS: Record<string, string> = {
  terminal: 'bg-green-900/40 text-green-400 border-green-700/50',
  stream: 'bg-blue-900/40 text-blue-400 border-blue-700/50',
}

export function StatusBar({ activeTab, onViewModeChange }: Props) {
  const t = useI18nStore((s) => s.t)
  const [menuOpen, setMenuOpen] = useState(false)
  const menuRef = useRef<HTMLDivElement>(null)

  const sessions = useSessionStore((s) => s.sessions)
  const defaultHost = useHostStore((s) => s.defaultHost)

  // Read agent event for the active session (hooks must be called unconditionally)
  const sessionCode = activeTab?.layout
    ? getPrimaryPane(activeTab.layout).content
    : null
  const agentSessionCode = sessionCode && 'sessionCode' in sessionCode ? sessionCode.sessionCode : null
  const agentEvent = useAgentStore((s) => agentSessionCode ? s.events[agentSessionCode] : undefined)

  const closeMenu = useCallback(() => setMenuOpen(false), [])
  useClickOutside(menuRef, closeMenu)

  if (!activeTab) {
    return (
      <div className="h-6 bg-surface-secondary border-t border-border-subtle flex items-center px-3 text-[10px] text-text-muted flex-shrink-0">
        {t('status.no_active')}
      </div>
    )
  }

  const primary = getPrimaryPane(activeTab.layout)
  const { content } = primary

  if (content.kind !== 'session') {
    return (
      <div className="h-6 bg-surface-secondary border-t border-border-subtle flex items-center px-3 text-[10px] text-text-muted flex-shrink-0">
        <span>{content.kind}</span>
      </div>
    )
  }

  // Session pane — show host, session name, status, viewMode toggle
  const session = sessions.find((s) => s.code === content.sessionCode)
  const sessionName = session?.name ?? content.sessionCode
  const hostName = defaultHost.name
  const status = defaultHost.status

  const viewMode = content.mode
  const viewModes: ('terminal' | 'stream')[] = ['terminal', 'stream']

  return (
    <div className="h-6 bg-surface-secondary border-t border-border-subtle flex items-center px-3 text-[10px] text-text-muted gap-3 flex-shrink-0 relative z-10">
      <span>{hostName}</span>
      <span>{sessionName}</span>
      <span className={status === 'connected' ? 'text-green-500' : 'text-text-muted'}>
        {status}
      </span>
      {getAgentLabel(agentEvent) && (
        <span className="text-text-muted" data-testid="agent-label">
          {getAgentLabel(agentEvent)}
        </span>
      )}
      <span className="ml-auto flex items-center">
        <div className="relative" ref={menuRef}>
          <button
            title={t('nav.toggle_view')}
            onClick={() => setMenuOpen((v) => !v)}
            className={`flex items-center gap-1 px-2 py-0.5 rounded border text-[10px] cursor-pointer transition-colors ${VIEW_MODE_COLORS[viewMode] ?? 'bg-surface-secondary text-text-secondary border-border-default'}`}
          >
            {viewMode}
            <CaretUp size={10} className={`transition-transform ${menuOpen ? '' : 'rotate-180'}`} />
          </button>
          {menuOpen && (
            <div className="absolute bottom-full right-0 mb-1 bg-surface-elevated border border-border-default rounded-md shadow-lg py-1 min-w-[100px]">
              {viewModes.map((vm) => (
                <button
                  key={vm}
                  onClick={() => {
                    onViewModeChange?.(activeTab.id, primary.id, vm)
                    setMenuOpen(false)
                  }}
                  className={`w-full px-3 py-1 text-left text-[10px] cursor-pointer transition-colors hover:bg-surface-hover ${vm === viewMode ? 'text-white' : 'text-text-secondary'}`}
                >
                  {vm} {vm === viewMode && '\u2713'}
                </button>
              ))}
            </div>
          )}
        </div>
      </span>
    </div>
  )
}
