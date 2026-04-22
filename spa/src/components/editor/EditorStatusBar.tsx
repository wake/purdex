import { useState, useRef, useCallback, useEffect } from 'react'
import { CaretUp } from '@phosphor-icons/react'
import { useClickOutside } from '../../hooks/useClickOutside'
import { useHostStore } from '../../stores/useHostStore'
import type { FileSource } from '../../types/fs'
import type { EditorEol, EditorEncoding } from '../../stores/useEditorStore'

interface Props {
  source: FileSource
  line: number
  column: number
  language: string
  eol: EditorEol
  encoding: EditorEncoding
  isMarkdown: boolean
  editorMode: 'raw' | 'wysiwyg'
  onModeChange?: (mode: 'raw' | 'wysiwyg') => void
  onLanguageChange?: (language: string) => void
}

const EDITOR_MODE_COLORS = 'bg-[rgba(31,46,163,0.18)] text-blue-400 shadow-[0_0_18px_rgba(47,100,255,0.18)] hover:bg-[rgba(42,61,196,0.24)]'
const EDITOR_MODE_BUTTON_STYLE = {
  borderColor: 'color-mix(in oklab, var(--color-blue-400) 50%, transparent)',
}

const LANGUAGE_OPTIONS = [
  { value: 'plaintext', label: 'Plain Text' },
  { value: 'markdown', label: 'Markdown' },
  { value: 'typescript', label: 'TypeScript' },
  { value: 'typescriptreact', label: 'TypeScript React' },
  { value: 'javascript', label: 'JavaScript' },
  { value: 'javascriptreact', label: 'JavaScript React' },
  { value: 'json', label: 'JSON' },
  { value: 'html', label: 'HTML' },
  { value: 'css', label: 'CSS' },
  { value: 'yaml', label: 'YAML' },
  { value: 'go', label: 'Go' },
  { value: 'python', label: 'Python' },
  { value: 'rust', label: 'Rust' },
  { value: 'shell', label: 'Shell' },
]

function sourceLabel(source: FileSource, hosts: Record<string, { name: string }>): string {
  switch (source.type) {
    case 'inapp':
      return 'Purdex'
    case 'local':
      return 'Local'
    case 'daemon':
      return hosts[source.hostId]?.name ?? 'Unknown'
  }
}

function languageLabel(language: string): string {
  return LANGUAGE_OPTIONS.find((option) => option.value === language)?.label ?? language
}

export function EditorStatusBar({ source, line, column, language, eol, encoding, isMarkdown, editorMode, onModeChange, onLanguageChange }: Props) {
  const [modeMenuOpen, setModeMenuOpen] = useState(false)
  const [languageMenuOpen, setLanguageMenuOpen] = useState(false)
  const modeMenuRef = useRef<HTMLDivElement>(null)
  const languageMenuRef = useRef<HTMLDivElement>(null)
  const hosts = useHostStore((s) => s.hosts)
  const closeModeMenu = useCallback(() => setModeMenuOpen(false), [])
  const closeLanguageMenu = useCallback(() => setLanguageMenuOpen(false), [])
  useClickOutside(modeMenuRef, closeModeMenu)
  useClickOutside(languageMenuRef, closeLanguageMenu)

  useEffect(() => {
    if (!modeMenuOpen && !languageMenuOpen) return
    const handleKeyDown = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        closeModeMenu()
        closeLanguageMenu()
      }
    }
    document.addEventListener('keydown', handleKeyDown)
    return () => document.removeEventListener('keydown', handleKeyDown)
  }, [modeMenuOpen, languageMenuOpen, closeLanguageMenu, closeModeMenu])

  const currentModeLabel = editorMode === 'raw' ? 'Source' : 'Live Mode'
  const currentSource = sourceLabel(source, hosts)
  const currentLanguageLabel = languageLabel(language)
  const canUseLiveMode = isMarkdown || language === 'markdown'

  return (
    <div className="h-6 bg-surface-secondary border-t border-border-subtle flex items-center px-3 text-[10px] text-text-muted gap-3 flex-shrink-0 relative z-10">
      <span className="text-text-secondary select-none">{currentSource}</span>
      <div className="ml-auto flex items-center gap-3">
        <span className="text-text-secondary select-none">Ln {line}, Col {column}</span>
        <span className="text-text-secondary select-none">{eol.toUpperCase()}</span>
        <span className="text-text-secondary select-none">{encoding.toUpperCase().replace('UTF8', 'UTF-8')}</span>
        {onLanguageChange && (
          <div className="relative" ref={languageMenuRef}>
            <button
              type="button"
              title="Select language"
              onClick={() => setLanguageMenuOpen((value) => !value)}
              className="flex items-center gap-1 px-2 py-0.5 rounded border border-border-subtle text-[10px] text-text-secondary hover:bg-surface-hover transition-colors"
            >
              {currentLanguageLabel}
              <CaretUp size={10} className={`transition-transform ${languageMenuOpen ? '' : 'rotate-180'}`} />
            </button>
            {languageMenuOpen && (
              <div className="absolute bottom-full right-0 mb-1 bg-surface-elevated border border-border-default rounded-md shadow-lg py-1 min-w-[160px] max-h-60 overflow-y-auto">
                {LANGUAGE_OPTIONS.map((option) => (
                  <button
                    type="button"
                    key={option.value}
                    onClick={() => {
                      onLanguageChange(option.value)
                      setLanguageMenuOpen(false)
                    }}
                    className={`w-full px-3 py-1 text-left text-[10px] transition-colors hover:bg-surface-hover ${option.value === language ? 'text-white' : 'text-text-secondary'}`}
                  >
                    {option.label} {option.value === language && '\u2713'}
                  </button>
                ))}
              </div>
            )}
          </div>
        )}
        {onModeChange && (
          <div className="relative" ref={modeMenuRef}>
            <button
              type="button"
              title="Toggle editor mode"
              onClick={() => setModeMenuOpen((value) => !value)}
              className={`flex items-center gap-1 px-2 py-0.5 rounded border text-[10px] cursor-pointer transition-colors ${EDITOR_MODE_COLORS}`}
              style={EDITOR_MODE_BUTTON_STYLE}
            >
              {currentModeLabel}
              <CaretUp size={10} className={`transition-transform ${modeMenuOpen ? '' : 'rotate-180'}`} />
            </button>
            {modeMenuOpen && (
              <div className="absolute bottom-full right-0 mb-1 bg-surface-elevated border border-border-default rounded-md shadow-lg py-1 min-w-[120px]">
                {(['raw', 'wysiwyg'] as const).map((mode) => {
                  const label = mode === 'raw' ? 'Source' : 'Live Mode'
                  const disabled = mode === 'wysiwyg' && !canUseLiveMode
                  return (
                    <button
                      type="button"
                      key={mode}
                      disabled={disabled}
                      onClick={() => {
                        if (disabled) return
                        onModeChange(mode)
                        setModeMenuOpen(false)
                      }}
                      className={`w-full px-3 py-1 text-left text-[10px] transition-colors ${disabled ? 'cursor-not-allowed text-text-muted/60' : 'cursor-pointer hover:bg-surface-hover'} ${mode === editorMode ? 'text-white' : disabled ? '' : 'text-text-secondary'}`}
                    >
                      {label} {mode === editorMode && '\u2713'}
                    </button>
                  )
                })}
              </div>
            )}
          </div>
        )}
      </div>
    </div>
  )
}
