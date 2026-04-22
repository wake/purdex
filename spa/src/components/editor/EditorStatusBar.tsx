import { useState, useRef, useCallback, useEffect } from 'react'
import { CaretUp } from '@phosphor-icons/react'
import { useClickOutside } from '../../hooks/useClickOutside'
import { useHostStore } from '../../stores/useHostStore'
import type { FileSource } from '../../types/fs'

interface Props {
  source: FileSource
  line: number
  column: number
  isMarkdown: boolean
  editorMode: 'raw' | 'wysiwyg'
  onModeChange?: (mode: 'raw' | 'wysiwyg') => void
}

const EDITOR_MODE_COLORS = 'bg-blue-900/40 text-blue-400 border-blue-700/50'
const INAPP_BADGE_COLORS = 'bg-violet-900/40 text-violet-300 border-violet-700/50'

function sourceLabel(source: FileSource, hosts: Record<string, { name: string }>): { label: string; badgeClass?: string } {
  switch (source.type) {
    case 'inapp':
      return { label: 'Purdex', badgeClass: INAPP_BADGE_COLORS }
    case 'local':
      return { label: 'Local' }
    case 'daemon':
      return { label: hosts[source.hostId]?.name ?? 'Unknown' }
  }
}

export function EditorStatusBar({ source, line, column, isMarkdown, editorMode, onModeChange }: Props) {
  const [menuOpen, setMenuOpen] = useState(false)
  const menuRef = useRef<HTMLDivElement>(null)
  const hosts = useHostStore((s) => s.hosts)
  const closeMenu = useCallback(() => setMenuOpen(false), [])
  useClickOutside(menuRef, closeMenu)

  useEffect(() => {
    if (!menuOpen) return
    const handleKeyDown = (e: KeyboardEvent) => {
      if (e.key === 'Escape') closeMenu()
    }
    document.addEventListener('keydown', handleKeyDown)
    return () => document.removeEventListener('keydown', handleKeyDown)
  }, [menuOpen, closeMenu])

  const currentModeLabel = editorMode === 'raw' ? 'Source' : 'Live Preview'
  const currentSource = sourceLabel(source, hosts)

  return (
    <div className="h-6 bg-surface-secondary border-t border-border-subtle flex items-center px-3 text-[10px] text-text-muted gap-3 flex-shrink-0 relative z-10">
      {currentSource.badgeClass ? (
        <span className={`px-[7px] rounded-[3px] border text-[10px] leading-4 ${currentSource.badgeClass}`}>
          {currentSource.label}
        </span>
      ) : (
        <span className="text-text-secondary select-none">{currentSource.label}</span>
      )}
      <span className="ml-auto flex items-center gap-3">
        <span>Ln {line}, Col {column}</span>
        {isMarkdown && onModeChange && (
          <div className="relative" ref={menuRef}>
            <button
              title="Toggle editor mode"
              onClick={() => setMenuOpen((v) => !v)}
              className={`flex items-center gap-1 px-2 py-0.5 rounded border text-[10px] cursor-pointer transition-colors ${EDITOR_MODE_COLORS}`}
            >
              {currentModeLabel}
              <CaretUp size={10} className={`transition-transform ${menuOpen ? '' : 'rotate-180'}`} />
            </button>
            {menuOpen && (
              <div className="absolute bottom-full right-0 mb-1 bg-surface-elevated border border-border-default rounded-md shadow-lg py-1 min-w-[120px]">
                {(['raw', 'wysiwyg'] as const).map((mode) => {
                  const label = mode === 'raw' ? 'Source' : 'Live Preview'
                  return (
                    <button
                      key={mode}
                      onClick={() => {
                        onModeChange(mode)
                        setMenuOpen(false)
                      }}
                      className={`w-full px-3 py-1 text-left text-[10px] cursor-pointer transition-colors hover:bg-surface-hover ${mode === editorMode ? 'text-white' : 'text-text-secondary'}`}
                    >
                      {label} {mode === editorMode && '\u2713'}
                    </button>
                  )
                })}
              </div>
            )}
          </div>
        )}
      </span>
    </div>
  )
}
