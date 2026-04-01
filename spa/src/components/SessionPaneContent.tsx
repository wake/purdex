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
  const hostId = content.kind === 'session' ? content.hostId : ''
  const mode = content.kind === 'session' ? content.mode : 'terminal'

  const daemonBase = useHostStore((s) => s.getDaemonBase(hostId))
  const wsBase = useHostStore((s) => s.getWsBase(hostId))
  const fetchHost = useSessionStore((s) => s.fetchHost)
  const streamPresets = useConfigStore((s) => s.config?.stream?.presets ?? EMPTY_PRESETS)

  const session = useSessionStore((s) =>
    (s.sessions[hostId] ?? []).find((sess) => sess.code === sessionCode) ?? null,
  )

  const handleHandoff = useCallback(async () => {
    if (!session) return
    try {
      const preset = streamPresets[0]?.name ?? 'cc'
      useStreamStore.getState().setHandoffProgress(hostId, session.code, 'starting')
      await handoff(daemonBase, session.code, 'stream', preset)
      await fetchHost(hostId, daemonBase)
    } catch (e) {
      console.error('Handoff failed:', e)
      useStreamStore.getState().setHandoffProgress(hostId, session.code, '')
    }
  }, [session, hostId, daemonBase, fetchHost, streamPresets])

  const handleHandoffToTerm = useCallback(async () => {
    if (!session) return
    try {
      useStreamStore.getState().setHandoffProgress(hostId, session.code, 'starting')
      await handoff(daemonBase, session.code, 'terminal')
      await fetchHost(hostId, daemonBase)
    } catch (e) {
      console.error('Handoff to term failed:', e)
      useStreamStore.getState().setHandoffProgress(hostId, session.code, '')
    }
  }, [session, hostId, daemonBase, fetchHost])

  if (content.kind !== 'session') return null

  if (mode === 'stream') {
    return (
      <ConversationView
        hostId={hostId}
        sessionCode={sessionCode}
        isActive={isActive}
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
      hostId={hostId}
      sessionCode={sessionCode}
    />
  )
}
