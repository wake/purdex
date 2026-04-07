import { useState, useRef, useEffect, useCallback } from 'react'
import { Globe } from '@phosphor-icons/react'
import { useI18nStore } from '../stores/useI18nStore'
import { useBrowserHistoryStore } from '../stores/useBrowserHistoryStore'
import type { NewTabProviderProps } from '../lib/new-tab-registry'

export function BrowserNewTabSection({ onSelect }: NewTabProviderProps) {
  const t = useI18nStore((s) => s.t)
  const urls = useBrowserHistoryStore((s) => s.urls)
  const addUrl = useBrowserHistoryStore((s) => s.addUrl)
  const [url, setUrl] = useState('')
  const [showDropdown, setShowDropdown] = useState(false)
  const [highlightIndex, setHighlightIndex] = useState(-1)
  const inputRef = useRef<HTMLInputElement>(null)
  const dropdownRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    inputRef.current?.focus()
  }, [])

  const filtered = url.trim()
    ? urls.filter((u) => u.toLowerCase().includes(url.toLowerCase()))
    : urls

  const submit = useCallback((value: string) => {
    if (!value.trim()) return
    const finalUrl = value.includes('://') ? value : `https://${value}`
    try {
      const parsed = new URL(finalUrl)
      if (!['http:', 'https:'].includes(parsed.protocol)) return
    } catch {
      return
    }
    addUrl(finalUrl)
    onSelect({ kind: 'browser', url: finalUrl })
  }, [addUrl, onSelect])

  const handleKeyDown = (e: React.KeyboardEvent) => {
    if (!showDropdown || filtered.length === 0) {
      if (e.key === 'ArrowDown' && filtered.length > 0) {
        setShowDropdown(true)
        setHighlightIndex(0)
        e.preventDefault()
        return
      }
      return
    }
    switch (e.key) {
      case 'ArrowDown':
        e.preventDefault()
        setHighlightIndex((prev) => Math.min(prev + 1, filtered.length - 1))
        break
      case 'ArrowUp':
        e.preventDefault()
        setHighlightIndex((prev) => {
          if (prev <= 0) { setShowDropdown(false); return -1 }
          return prev - 1
        })
        break
      case 'Enter':
        e.preventDefault()
        if (highlightIndex >= 0 && highlightIndex < filtered.length) {
          submit(filtered[highlightIndex])
        } else {
          submit(url)
        }
        break
      case 'Escape':
        e.preventDefault()
        setShowDropdown(false)
        setHighlightIndex(-1)
        break
    }
  }

  const handleInputChange = (e: React.ChangeEvent<HTMLInputElement>) => {
    setUrl(e.target.value)
    setShowDropdown(true)
    setHighlightIndex(-1)
  }

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault()
    if (highlightIndex >= 0 && highlightIndex < filtered.length) {
      submit(filtered[highlightIndex])
    } else {
      submit(url)
    }
  }

  // Close dropdown on outside click
  useEffect(() => {
    const handler = (e: MouseEvent) => {
      if (
        dropdownRef.current && !dropdownRef.current.contains(e.target as Node) &&
        inputRef.current && !inputRef.current.contains(e.target as Node)
      ) {
        setShowDropdown(false)
      }
    }
    document.addEventListener('mousedown', handler)
    return () => document.removeEventListener('mousedown', handler)
  }, [])

  // Scroll highlighted item into view
  useEffect(() => {
    if (highlightIndex < 0 || !dropdownRef.current) return
    const items = dropdownRef.current.querySelectorAll('[data-dropdown-item]')
    items[highlightIndex]?.scrollIntoView({ block: 'nearest' })
  }, [highlightIndex])

  return (
    <div className="relative px-2">
      <form onSubmit={handleSubmit} className="flex items-center gap-2">
        <Globe size={16} className="text-text-muted flex-shrink-0" />
        <input
          ref={inputRef}
          type="text"
          value={url}
          onChange={handleInputChange}
          onFocus={() => { if (urls.length > 0) setShowDropdown(true) }}
          onKeyDown={handleKeyDown}
          placeholder={t('browser.url_placeholder')}
          className="flex-1 bg-surface-input border border-border-default rounded-md text-text-primary text-xs px-3 py-1.5 focus:border-border-active focus:outline-none"
        />
      </form>
      {showDropdown && filtered.length > 0 && (
        <div
          ref={dropdownRef}
          className="absolute left-2 right-2 mt-1 bg-surface-elevated border border-border-default rounded-md shadow-lg max-h-48 overflow-y-auto z-10"
        >
          {filtered.map((historyUrl, i) => (
            <button
              key={historyUrl}
              data-dropdown-item
              type="button"
              onMouseDown={(e) => { e.preventDefault(); submit(historyUrl) }}
              onMouseEnter={() => setHighlightIndex(i)}
              className={`w-full text-left text-xs px-3 py-1.5 truncate cursor-pointer transition-colors ${
                i === highlightIndex ? 'bg-surface-hover text-text-primary' : 'text-text-secondary hover:bg-surface-hover'
              }`}
            >
              {historyUrl}
            </button>
          ))}
        </div>
      )}
    </div>
  )
}
