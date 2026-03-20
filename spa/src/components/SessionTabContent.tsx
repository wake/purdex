import { useCallback } from 'react'
import TerminalView from './TerminalView'
import ConversationView from './ConversationView'
import { getSessionName } from '../lib/tab-helpers'
import { useSessionStore } from '../stores/useSessionStore'
import { useStreamStore } from '../stores/useStreamStore'
import { handoff } from '../lib/api'
import type { TabRendererProps } from '../lib/tab-registry'

export function SessionTabContent({ tab, isActive, wsBase, daemonBase }: TabRendererProps) {
  const sessionName = getSessionName(tab)
  const viewMode = tab.viewMode ?? 'terminal'
  const fetchSessions = useSessionStore((s) => s.fetch)

  const session = useSessionStore((s) =>
    s.sessions.find((sess) => sess.name === sessionName) ?? null,
  )

  const handleHandoff = useCallback(async () => {
    if (!session) return
    try {
      useStreamStore.getState().setHandoffProgress(session.name, 'starting')
      await handoff(daemonBase, session.id, 'stream')
      await fetchSessions(daemonBase)
    } catch (e) {
      console.error('Handoff failed:', e)
      useStreamStore.getState().setHandoffProgress(session.name, '')
    }
  }, [session, daemonBase, fetchSessions])

  const handleHandoffToTerm = useCallback(async () => {
    if (!session) return
    try {
      useStreamStore.getState().setHandoffProgress(session.name, 'starting')
      await handoff(daemonBase, session.id, 'term')
      await fetchSessions(daemonBase)
    } catch (e) {
      console.error('Handoff to term failed:', e)
      useStreamStore.getState().setHandoffProgress(session.name, '')
    }
  }, [session, daemonBase, fetchSessions])

  if (!sessionName) {
    return (
      <div className="flex-1 flex items-center justify-center text-gray-600 text-sm">
        No session name
      </div>
    )
  }

  if (viewMode === 'stream') {
    return (
      <ConversationView
        sessionName={sessionName}
        onHandoff={handleHandoff}
        onHandoffToTerm={handleHandoffToTerm}
      />
    )
  }

  return (
    <TerminalView
      key={`${tab.id}-${viewMode}`}
      wsUrl={`${wsBase}/ws/terminal/${encodeURIComponent(sessionName)}`}
      visible={isActive}
    />
  )
}
