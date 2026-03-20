import { useState, useRef, useEffect } from 'react'
import { CaretUp } from '@phosphor-icons/react'

interface Props {
  hostName: string | null
  sessionName: string | null
  status: string | null
  viewMode: string | null
  viewModes: string[] | null
  onViewModeChange: (viewMode: string) => void
}

const VIEW_MODE_COLORS: Record<string, string> = {
  terminal: 'bg-green-900/40 text-green-400 border-green-700/50',
  stream: 'bg-blue-900/40 text-blue-400 border-blue-700/50',
}

export function StatusBar({ hostName, sessionName, status, viewMode, viewModes, onViewModeChange }: Props) {
  const [menuOpen, setMenuOpen] = useState(false)
  const menuRef = useRef<HTMLDivElement>(null)

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

  if (!sessionName) {
    return (
      <div className="h-6 bg-[#12122a] border-t border-gray-800 flex items-center px-3 text-[10px] text-gray-600 flex-shrink-0">
        No active session
      </div>
    )
  }

  const showBadge = viewModes && viewModes.length > 1 && viewMode

  return (
    <div className="h-6 bg-[#12122a] border-t border-gray-800 flex items-center px-3 text-[10px] text-gray-600 gap-3 flex-shrink-0">
      <span>{hostName}</span>
      <span>{sessionName}</span>
      <span className={status === 'connected' ? 'text-green-500' : 'text-gray-600'}>
        {status}
      </span>
      <span className="ml-auto flex items-center">
        {showBadge && (
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
                    onClick={() => { onViewModeChange(vm); setMenuOpen(false) }}
                    className={`w-full px-3 py-1 text-left text-[10px] cursor-pointer transition-colors hover:bg-[#2a2a5a] ${vm === viewMode ? 'text-white' : 'text-gray-400'}`}
                  >
                    {vm} {vm === viewMode && '✓'}
                  </button>
                ))}
              </div>
            )}
          </div>
        )}
      </span>
    </div>
  )
}
