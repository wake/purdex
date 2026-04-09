import { useState, useRef, useCallback, useEffect } from 'react'
import {
  ArrowLeft,
  ArrowRight,
  ArrowClockwise,
  X,
  DotsThree,
} from '@phosphor-icons/react'
import { normalizeUrl } from '../lib/url-utils'
import { BrowserToolbarMenu } from './BrowserToolbarMenu'

export interface BrowserToolbarProps {
  url: string
  canGoBack: boolean
  canGoForward: boolean
  isLoading: boolean
  onGoBack: () => void
  onGoForward: () => void
  onReload: () => void
  onStop: () => void
  onNavigate: (url: string) => void
  onOpenExternal: () => void
  onCopyUrl: () => void
  onPopOut?: () => void
}

export function BrowserToolbar({
  url,
  canGoBack,
  canGoForward,
  isLoading,
  onGoBack,
  onGoForward,
  onReload,
  onStop,
  onNavigate,
  onOpenExternal,
  onCopyUrl,
  onPopOut,
}: BrowserToolbarProps) {
  const [editValue, setEditValue] = useState('')
  const [isEditing, setIsEditing] = useState(false)
  const [menuOpen, setMenuOpen] = useState(false)
  const inputRef = useRef<HTMLInputElement>(null)

  // When not editing, display the prop URL directly. When editing, show editValue.
  const displayValue = isEditing ? editValue : url

  const handleKeyDown = useCallback(
    (e: React.KeyboardEvent) => {
      if (e.key === 'Enter') {
        const normalized = normalizeUrl(editValue)
        if (normalized) {
          onNavigate(normalized)
          setIsEditing(false)
          inputRef.current?.blur()
        }
      } else if (e.key === 'Escape') {
        setIsEditing(false)
        inputRef.current?.blur()
      }
    },
    [editValue, onNavigate],
  )

  useEffect(() => {
    const handler = () => {
      inputRef.current?.focus()
      inputRef.current?.select()
    }
    document.addEventListener('browser:focus-url', handler)
    return () => document.removeEventListener('browser:focus-url', handler)
  }, [])

  const navBtnClass = 'p-1 rounded hover:bg-surface-hover disabled:opacity-30 transition-colors'

  return (
    <div className="flex items-center gap-1 px-2 py-1 border-b border-border-subtle bg-surface-secondary">
      <button
        aria-label="Go back"
        className={navBtnClass}
        disabled={!canGoBack}
        onClick={onGoBack}
      >
        <ArrowLeft size={16} />
      </button>
      <button
        aria-label="Go forward"
        className={navBtnClass}
        disabled={!canGoForward}
        onClick={onGoForward}
      >
        <ArrowRight size={16} />
      </button>
      {isLoading ? (
        <button
          aria-label="Stop"
          className={navBtnClass}
          onClick={onStop}
        >
          <X size={16} />
        </button>
      ) : (
        <button
          aria-label="Reload"
          className={navBtnClass}
          onClick={onReload}
        >
          <ArrowClockwise size={16} />
        </button>
      )}

      <input
        ref={inputRef}
        role="textbox"
        className="flex-1 mx-1 px-2 py-0.5 rounded bg-surface-input text-xs font-mono text-text-primary outline-none focus:border-border-active focus:ring-1 focus:ring-border-active"
        value={displayValue}
        onChange={(e) => { setEditValue(e.target.value); setIsEditing(true) }}
        onFocus={() => { setEditValue(url); setIsEditing(true); inputRef.current?.select() }}
        onBlur={() => setIsEditing(false)}
        onKeyDown={handleKeyDown}
      />

      <div className="relative">
        <button
          aria-label="More"
          className={navBtnClass}
          onClick={() => setMenuOpen((v) => !v)}
        >
          <DotsThree size={16} weight="bold" />
        </button>
        {menuOpen && (
          <BrowserToolbarMenu
            onOpenExternal={onOpenExternal}
            onCopyUrl={onCopyUrl}
            onPopOut={onPopOut}
            onClose={() => setMenuOpen(false)}
          />
        )}
      </div>
    </div>
  )
}
