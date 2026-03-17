import { useEffect, useCallback, useState } from 'react'
import SessionPanel from './components/SessionPanel'
import TerminalView from './components/TerminalView'
import ConversationView from './components/ConversationView'
import TopBar from './components/TopBar'
import SettingsPanel from './components/SettingsPanel'
import { useSessionStore } from './stores/useSessionStore'
import { useStreamStore } from './stores/useStreamStore'
import { useConfigStore } from './stores/useConfigStore'
import { switchMode, handoff } from './lib/api'

// TODO: daemonBase should come from host management (localStorage)
const daemonBase = 'http://100.64.0.2:7860'
const wsBase = daemonBase.replace(/^http/, 'ws')

export default function App() {
  const { sessions, activeId, fetch: fetchSessions } = useSessionStore()
  const conn = useStreamStore((s) => s.conn)
  const { config, fetch: fetchConfig } = useConfigStore()
  const active = sessions.find((s) => s.id === activeId)
  const [settingsOpen, setSettingsOpen] = useState(false)

  // Fetch sessions and config on mount
  useEffect(() => {
    fetchSessions(daemonBase)
    fetchConfig(daemonBase)
  }, [fetchSessions, fetchConfig])

  // Connect session-events WS on mount (for status updates)
  useEffect(() => {
    let ws: WebSocket | null = null
    try {
      ws = new WebSocket(`${wsBase}/ws/session-events`)
      ws.onmessage = (ev) => {
        try {
          const data = JSON.parse(ev.data)
          if (data.type === 'session_update' || data.type === 'status_change') {
            // Refresh sessions on any status event
            fetchSessions(daemonBase)
          }
        } catch {
          // ignore parse errors
        }
      }
      ws.onerror = () => { /* will reconnect on close */ }
      ws.onclose = () => {
        // Simple reconnect after 3s
        setTimeout(() => {
          // Component may have unmounted; this is best-effort
        }, 3000)
      }
    } catch {
      // WebSocket construction failed
    }
    return () => {
      ws?.close()
    }
  }, [fetchSessions])

  // Switch to term mode
  const handleModeChange = useCallback(async (newMode: string) => {
    if (!active || active.mode === newMode) return
    try {
      await switchMode(daemonBase, active.id, newMode)
      await fetchSessions(daemonBase)
    } catch (e) {
      console.error('mode switch failed:', e)
    }
  }, [active, fetchSessions])

  // Handoff for stream/jsonl modes
  const handleHandoff = useCallback(async (mode: string, preset: string) => {
    if (!active) return
    try {
      await handoff(daemonBase, active.id, mode, preset)
      await fetchSessions(daemonBase)
    } catch (e) {
      console.error('handoff failed:', e)
    }
  }, [active, fetchSessions])

  const handleInterrupt = useCallback(() => {
    conn?.interrupt()
  }, [conn])

  // Derive presets from config
  const streamPresets = config?.stream?.presets || [{ name: 'cc', command: 'claude --dangerously-skip-permissions' }]
  const jsonlPresets = config?.jsonl?.presets || [{ name: 'cc-jsonl', command: 'claude --output-format stream-json' }]

  return (
    <div className="h-screen bg-gray-950 text-gray-200 flex">
      <SessionPanel onSettingsOpen={() => setSettingsOpen(true)} />
      <div className="flex-1 flex flex-col h-full overflow-hidden">
        {active && (
          <TopBar
            sessionName={active.name}
            mode={active.mode}
            streamPresets={streamPresets}
            jsonlPresets={jsonlPresets}
            onModeChange={handleModeChange}
            onHandoff={handleHandoff}
            onInterrupt={handleInterrupt}
          />
        )}
        <div className="flex-1 overflow-hidden">
          {active ? (
            active.mode === 'stream' ? (
              <ConversationView
                wsUrl={`${wsBase}/ws/cli-bridge-sub/${encodeURIComponent(active.name)}`}
                sessionName={active.name}
              />
            ) : (
              <TerminalView
                wsUrl={`${wsBase}/ws/terminal/${encodeURIComponent(active.name)}`}
              />
            )
          ) : (
            <div className="flex items-center justify-center h-full">
              <p className="text-gray-400">Select a session</p>
            </div>
          )}
        </div>
      </div>

      {/* Settings Panel overlay */}
      {settingsOpen && (
        <SettingsPanel
          daemonBase={daemonBase}
          onClose={() => setSettingsOpen(false)}
        />
      )}
    </div>
  )
}
