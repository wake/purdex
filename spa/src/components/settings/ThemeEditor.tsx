import { useState, useEffect, useRef, useMemo } from 'react'
import { CaretRight, CaretDown, FloppyDisk, ArrowCounterClockwise, X } from '@phosphor-icons/react'
import { THEME_TOKEN_KEYS, TOKEN_METADATA, tokensToCss } from '../../lib/theme-tokens'
import type { ThemeTokens, ThemeTokenKey } from '../../lib/theme-tokens'
import { getTheme } from '../../lib/theme-registry'
import { useThemeStore } from '../../stores/useThemeStore'

const TEMP_THEME_ATTR = '__theme-editor-preview'

const GROUP_LABELS: Record<string, string> = {
  surface: 'Surface',
  text: 'Text',
  border: 'Border',
  accent: 'Accent',
  terminal: 'Terminal',
  status: 'Status',
}

interface ThemeEditorProps {
  baseThemeId: string
  onClose: () => void
}

function groupTokens(): Record<string, ThemeTokenKey[]> {
  const groups: Record<string, ThemeTokenKey[]> = {}
  for (const key of THEME_TOKEN_KEYS) {
    const meta = TOKEN_METADATA[key]
    if (!groups[meta.group]) groups[meta.group] = []
    groups[meta.group].push(key)
  }
  return groups
}

/** Normalize a color value to 6-digit hex for <input type="color"> */
function toHex6(value: string): string {
  // Only convert simple #rgb or #rrggbb hex values
  if (/^#[0-9a-fA-F]{6}$/.test(value)) return value
  if (/^#[0-9a-fA-F]{3}$/.test(value)) {
    const [, r, g, b] = value.match(/^#(.)(.)(.)$/)!
    return `#${r}${r}${g}${g}${b}${b}`
  }
  // For rgba or other formats, try the canvas approach
  try {
    const ctx = document.createElement('canvas').getContext('2d')
    if (ctx) {
      ctx.fillStyle = value
      const result = ctx.fillStyle
      if (/^#[0-9a-fA-F]{6}$/.test(result)) return result
    }
  } catch {
    // Fallback
  }
  return '#000000'
}

export function ThemeEditor({ baseThemeId, onClose }: ThemeEditorProps) {
  const baseTheme = getTheme(baseThemeId)
  const createCustomTheme = useThemeStore((s) => s.createCustomTheme)
  const setActiveTheme = useThemeStore((s) => s.setActiveTheme)

  const [name, setName] = useState(() => `${baseTheme?.name ?? 'Custom'} (Custom)`)
  const [tokens, setTokens] = useState<ThemeTokens>(() => ({ ...baseTheme!.tokens }))
  const [collapsedGroups, setCollapsedGroups] = useState<Set<string>>(new Set())
  const styleRef = useRef<HTMLStyleElement | null>(null)
  const originalThemeRef = useRef(document.documentElement.dataset.theme)

  const groups = useMemo(() => groupTokens(), [])

  // Apply live preview
  useEffect(() => {
    const style = document.createElement('style')
    style.setAttribute('data-theme-editor', 'true')
    document.head.appendChild(style)
    styleRef.current = style

    return () => {
      style.remove()
      // Restore original theme attr
      if (originalThemeRef.current !== undefined) {
        document.documentElement.dataset.theme = originalThemeRef.current
      }
    }
  }, [])

  useEffect(() => {
    if (!styleRef.current) return
    const css = tokensToCss(tokens)
    styleRef.current.textContent = `[data-theme="${TEMP_THEME_ATTR}"] { ${css} }`
    document.documentElement.dataset.theme = TEMP_THEME_ATTR
  }, [tokens])

  const handleTokenChange = (key: ThemeTokenKey, value: string) => {
    setTokens((prev) => ({ ...prev, [key]: value }))
  }

  const handleSave = () => {
    // Remove preview style
    styleRef.current?.remove()
    styleRef.current = null

    // Restore original before creating theme
    if (originalThemeRef.current !== undefined) {
      document.documentElement.dataset.theme = originalThemeRef.current
    }

    // Compute overrides relative to base
    const overrides: Partial<ThemeTokens> = {}
    for (const key of THEME_TOKEN_KEYS) {
      if (tokens[key] !== baseTheme!.tokens[key]) {
        overrides[key] = tokens[key]
      }
    }

    const newId = createCustomTheme(name, baseThemeId, overrides)
    setActiveTheme(newId)
    onClose()
  }

  const handleCancel = () => {
    // Cleanup handled by useEffect cleanup
    onClose()
  }

  const handleReset = () => {
    if (baseTheme) setTokens({ ...baseTheme.tokens })
  }

  const toggleGroup = (group: string) => {
    setCollapsedGroups((prev) => {
      const next = new Set(prev)
      if (next.has(group)) next.delete(group)
      else next.add(group)
      return next
    })
  }

  if (!baseTheme) return null

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/50"
      data-testid="theme-editor"
    >
      <div className="bg-surface-primary border border-border-default rounded-lg shadow-lg w-[520px] max-h-[80vh] flex flex-col">
        {/* Header */}
        <div className="flex items-center justify-between px-4 py-3 border-b border-border-subtle">
          <h3 className="text-sm font-medium text-text-primary">Theme Editor</h3>
          <button
            aria-label="Close editor"
            onClick={handleCancel}
            className="p-1 rounded text-text-muted hover:text-text-primary"
          >
            <X size={16} />
          </button>
        </div>

        {/* Name input */}
        <div className="px-4 py-3 border-b border-border-subtle">
          <label className="block text-xs text-text-secondary mb-1">Theme Name</label>
          <input
            type="text"
            aria-label="Theme name"
            value={name}
            onChange={(e) => setName(e.target.value)}
            className="w-full bg-surface-input border border-border-default rounded-md text-text-primary text-xs px-3 py-1.5 focus:border-border-active focus:outline-none"
          />
        </div>

        {/* Token groups */}
        <div className="flex-1 overflow-y-auto px-4 py-2">
          {Object.entries(groups).map(([group, keys]) => {
            const collapsed = collapsedGroups.has(group)
            return (
              <div key={group} className="mb-2">
                <button
                  onClick={() => toggleGroup(group)}
                  className="flex items-center gap-1 w-full py-1.5 text-xs font-medium text-text-secondary hover:text-text-primary"
                  aria-label={`Toggle ${GROUP_LABELS[group]} group`}
                >
                  {collapsed ? <CaretRight size={12} /> : <CaretDown size={12} />}
                  {GROUP_LABELS[group] ?? group}
                </button>
                {!collapsed && (
                  <div className="ml-4 space-y-1">
                    {keys.map((key) => {
                      const meta = TOKEN_METADATA[key]
                      const hexValue = toHex6(tokens[key])
                      return (
                        <div key={key} className="flex items-center gap-2 py-0.5" data-testid={`token-${key}`}>
                          <span className="text-xs text-text-secondary w-36 flex-shrink-0">{meta.label}</span>
                          <div
                            className="w-6 h-6 rounded border border-border-default flex-shrink-0"
                            style={{ backgroundColor: tokens[key] }}
                          />
                          <input
                            type="color"
                            value={hexValue}
                            onChange={(e) => handleTokenChange(key, e.target.value)}
                            className="w-6 h-6 cursor-pointer flex-shrink-0"
                            aria-label={`${meta.label} color picker`}
                          />
                          <input
                            type="text"
                            value={tokens[key]}
                            onChange={(e) => handleTokenChange(key, e.target.value)}
                            className="bg-surface-input border border-border-default rounded text-text-primary text-xs px-2 py-0.5 w-32 font-mono focus:border-border-active focus:outline-none"
                            aria-label={`${meta.label} hex value`}
                          />
                        </div>
                      )
                    })}
                  </div>
                )}
              </div>
            )
          })}
        </div>

        {/* Footer */}
        <div className="flex items-center justify-between px-4 py-3 border-t border-border-subtle">
          <button
            onClick={handleReset}
            className="flex items-center gap-1 text-xs text-text-secondary hover:text-text-primary"
            aria-label="Reset to base"
          >
            <ArrowCounterClockwise size={14} />
            Reset
          </button>
          <div className="flex items-center gap-2">
            <button
              onClick={handleCancel}
              className="px-3 py-1.5 text-xs text-text-secondary hover:text-text-primary rounded-md border border-border-default"
            >
              Cancel
            </button>
            <button
              onClick={handleSave}
              className="flex items-center gap-1 px-3 py-1.5 text-xs text-text-inverse bg-accent hover:bg-accent-hover rounded-md"
              aria-label="Save theme"
            >
              <FloppyDisk size={14} />
              Save
            </button>
          </div>
        </div>
      </div>
    </div>
  )
}
