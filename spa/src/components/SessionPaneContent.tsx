import { useCallback } from 'react'
import TerminalView from './TerminalView'
import ConversationView from './ConversationView'
import { useSessionStore } from '../stores/useSessionStore'
import { useStreamStore } from '../stores/useStreamStore'
import { useConfigStore } from '../stores/useConfigStore'
import { handoff } from '../lib/api'
import { useHostStore } from '../stores/useHostStore'
import type { PaneRendererProps } from '../lib/pane-registry'

const EMPTY_PRESETS: Array<{ name: string; command: string }> = []

export function SessionPaneContent({ pane, isActive }: PaneRendererProps) {
  const content = pane.content
  const sessionCode = content.kind === 'session' ? content.sessionCode : ''
  const mode = content.kind === 'session' ? content.mode : 'terminal'

  const daemonBase = useHostStore((s) => s.getDaemonBase('local'))
  const wsBase = useHostStore((s) => s.getWsBase('local'))
  const fetchSessions = useSessionStore((s) => s.fetch)
  const streamPresets = useConfigStore((s) => s.config?.stream?.presets ?? EMPTY_PRESETS)

  const session = useSessionStore((s) =>
    s.sessions.find((sess) => sess.code === sessionCode) ?? null,
  )

  const handleHandoff = useCallback(async () => {
    if (!session) return
    try {
      const preset = streamPresets[0]?.name ?? 'cc'
      useStreamStore.getState().setHandoffProgress(session.code, 'starting')
      await handoff(daemonBase, session.code, 'stream', preset)
      await fetchSessions(daemonBase)
    } catch (e) {
      console.error('Handoff failed:', e)
      useStreamStore.getState().setHandoffProgress(session.code, '')
    }
  }, [session, daemonBase, fetchSessions, streamPresets])

  const handleHandoffToTerm = useCallback(async () => {
    if (!session) return
    try {
      useStreamStore.getState().setHandoffProgress(session.code, 'starting')
      await handoff(daemonBase, session.code, 'term')
      await fetchSessions(daemonBase)
    } catch (e) {
      console.error('Handoff to term failed:', e)
      useStreamStore.getState().setHandoffProgress(session.code, '')
    }
  }, [session, daemonBase, fetchSessions])

  if (content.kind !== 'session') return null

  if (mode === 'stream') {
    return (
      <ConversationView
        sessionCode={sessionCode}
        onHandoff={handleHandoff}
        onHandoffToTerm={handleHandoffToTerm}
      />
    )
  }

  return (
    <TerminalView
      key={`${pane.id}-${mode}`}
      wsUrl={`${wsBase}/ws/terminal/${encodeURIComponent(sessionCode)}`}
      visible={isActive}
    />
  )
}
