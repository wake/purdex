import SessionPanel from './components/SessionPanel'
import TerminalView from './components/TerminalView'
import { useSessionStore } from './stores/useSessionStore'

export default function App() {
  const { sessions, activeId } = useSessionStore()
  const active = sessions.find((s) => s.id === activeId)

  // TODO: make daemon base URL configurable via host management
  const daemonBase = 'localhost:7860'
  const wsProtocol = location.protocol === 'https:' ? 'wss:' : 'ws:'

  return (
    <div className="h-screen bg-gray-950 text-gray-200 flex">
      <SessionPanel />
      <div className="flex-1">
        {active ? (
          <TerminalView wsUrl={`${wsProtocol}//${daemonBase}/ws/terminal/${active.name}`} />
        ) : (
          <div className="flex items-center justify-center h-full">
            <p className="text-gray-500">Select a session</p>
          </div>
        )}
      </div>
    </div>
  )
}
