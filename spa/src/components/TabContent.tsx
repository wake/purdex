import TerminalView from './TerminalView'
import ConversationView from './ConversationView'
import type { Tab } from '../types/tab'

interface Props {
  allTabs: Tab[]
  activeTabId: string | null
  wsBase: string
  terminalKey?: number
  connectingMessage?: string
  onHandoff?: () => void
  onHandoffToTerm?: () => void
}

export function TabContent({
  allTabs, activeTabId, wsBase,
  terminalKey, connectingMessage,
  onHandoff, onHandoffToTerm,
}: Props) {
  if (allTabs.length === 0) {
    return (
      <div className="flex-1 flex items-center justify-center text-gray-600 text-sm">
        選擇或建立一個分頁開始使用
      </div>
    )
  }

  return (
    <div className="flex-1 overflow-hidden relative">
      {allTabs.map((tab) => {
        const isActive = tab.id === activeTabId

        return (
          <div
            key={tab.id}
            className="absolute inset-0"
            style={{ display: isActive ? 'flex' : 'none' }}
          >
            {tab.type === 'terminal' && tab.sessionName && (
              <TerminalView
                key={tab.type === 'terminal' && isActive ? terminalKey : undefined}
                wsUrl={`${wsBase}/ws/terminal/${tab.sessionName}`}
                visible={isActive}
                connectingMessage={isActive ? connectingMessage : undefined}
              />
            )}
            {tab.type === 'stream' && tab.sessionName && (
              <ConversationView
                sessionName={tab.sessionName}
                onHandoff={isActive ? onHandoff : undefined}
                onHandoffToTerm={isActive ? onHandoffToTerm : undefined}
              />
            )}
            {tab.type === 'editor' && (
              <div className="flex-1 flex items-center justify-center text-gray-500 text-sm">
                Editor: {tab.filePath ?? tab.label}（Phase 5 實作）
              </div>
            )}
          </div>
        )
      })}
    </div>
  )
}
