// spa/src/components/SessionPicker.tsx
import { useState, useRef, useEffect } from 'react'
import { Terminal, Lightning } from '@phosphor-icons/react'
import type { Session } from '../lib/api'

interface Props {
  sessions: Session[]
  existingTabSessionNames: string[]
  onSelect: (session: Session) => void
  onClose: () => void
}

export function SessionPicker({ sessions, existingTabSessionNames, onSelect, onClose }: Props) {
  const [search, setSearch] = useState('')
  const inputRef = useRef<HTMLInputElement>(null)

  useEffect(() => {
    inputRef.current?.focus()
  }, [])

  const filtered = sessions.filter((s) =>
    s.name.toLowerCase().includes(search.toLowerCase()),
  )

  const hasTab = (name: string) => existingTabSessionNames.includes(name)

  return (
    <div className="fixed inset-0 z-50 flex items-start justify-center pt-[15vh]" onClick={onClose}>
      <div
        className="bg-surface-elevated border border-border-default rounded-xl shadow-2xl w-[380px] max-h-[60vh] flex flex-col overflow-hidden"
        onClick={(e) => e.stopPropagation()}
      >
        {/* Search */}
        <div className="p-3 border-b border-border-default">
          <input
            ref={inputRef}
            type="text"
            placeholder="搜尋 session..."
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            className="w-full bg-surface-primary border border-border-default rounded-md px-3 py-2 text-sm text-white placeholder-text-muted outline-none focus:border-accent"
          />
        </div>
        {/* List */}
        <div className="flex-1 overflow-y-auto">
          {filtered.map((s) => (
            <button
              key={s.code}
              onClick={() => onSelect(s)}
              className="w-full px-4 py-2.5 flex items-center gap-2 text-sm text-left hover:bg-surface-hover cursor-pointer transition-colors"
            >
              {s.mode === 'stream' ? <Lightning size={16} className="text-blue-400 flex-shrink-0" /> : <Terminal size={16} className="text-text-secondary flex-shrink-0" />}
              <span className="flex-1 text-text-primary">{s.name}</span>
              <span className="text-xs text-text-muted">{s.mode}</span>
              {hasTab(s.name) && <span className="text-xs text-purple-400">已開啟</span>}
            </button>
          ))}
          {filtered.length === 0 && (
            <div className="px-4 py-6 text-center text-text-muted text-sm">無符合的 session</div>
          )}
        </div>
      </div>
    </div>
  )
}
