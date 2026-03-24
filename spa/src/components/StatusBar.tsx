import { useState, useRef, useEffect } from 'react'
import { CaretUp } from '@phosphor-icons/react'
import type { Tab } from '../types/tab'
import { getPrimaryPane } from '../lib/pane-tree'
import { useSessionStore } from '../stores/useSessionStore'
import { useHostStore } from '../stores/useHostStore'

interface Props {
  activeTab: Tab | null
  onViewModeChange?: (tabId: string, paneId: string, mode: 'terminal' | 'stream') => void
}

const VIEW_MODE_COLORS: Record<string, string> = {
  terminal: 'bg-green-900/40 text-green-400 border-green-700/50',
  stream: 'bg-blue-900/40 text-blue-400 border-blue-700/50',
}

export function StatusBar({ activeTab, onViewModeChange }: Props) {
  const [menuOpen, setMenuOpen] = useState(false)
  const menuRef = useRef<HTMLDivElement>(null)

  const sessions = useSessionStore((s) => s.sessions)
  const defaultHost = useHostStore((s) => s.defaultHost)

  useEffect(() => {
    if (!menuOpen) return
    const handler = (e: MouseEvent) => {
      if (menuRef.current && !menuRef.current.contains(e.target as Node)) {
        setMenuOpen(false)
      }
    }
    document.addEventListener('mousedown', handler)
    return () => document.removeEventListener('mousedown', handler)
  }, [menuOpen])

  if (!activeTab) {
    return (
      <div className="h-6 bg-[#12122a] border-t border-gray-800 flex items-center px-3 text-[10px] text-gray-600 flex-shrink-0">
        No active session
      </div>
    )
  }

  const primary = getPrimaryPane(activeTab.layout)
  const { content } = primary

  if (content.kind !== 'session') {
    return (
      <div className="h-6 bg-[#12122a] border-t border-gray-800 flex items-center px-3 text-[10px] text-gray-600 flex-shrink-0">
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
    <div className="h-6 bg-[#12122a] border-t border-gray-800 flex items-center px-3 text-[10px] text-gray-600 gap-3 flex-shrink-0">
      <span>{hostName}</span>
      <span>{sessionName}</span>
      <span className={status === 'connected' ? 'text-green-500' : 'text-gray-600'}>
        {status}
      </span>
      <span className="ml-auto flex items-center">
        <div className="relative" ref={menuRef}>
          <button
            title="切換檢視模式"
            onClick={() => setMenuOpen((v) => !v)}
            className={`flex items-center gap-1 px-2 py-0.5 rounded border text-[10px] cursor-pointer transition-colors ${VIEW_MODE_COLORS[viewMode] ?? 'bg-gray-800 text-gray-400 border-gray-700'}`}
          >
            {viewMode}
            <CaretUp size={10} className={`transition-transform ${menuOpen ? '' : 'rotate-180'}`} />
          </button>
          {menuOpen && (
            <div className="absolute bottom-full right-0 mb-1 bg-[#1e1e3e] border border-gray-700 rounded-md shadow-lg py-1 min-w-[100px]">
              {viewModes.map((vm) => (
                <button
                  key={vm}
                  onClick={() => {
                    onViewModeChange?.(activeTab.id, primary.id, vm)
                    setMenuOpen(false)
                  }}
                  className={`w-full px-3 py-1 text-left text-[10px] cursor-pointer transition-colors hover:bg-[#2a2a5a] ${vm === viewMode ? 'text-white' : 'text-gray-400'}`}
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
