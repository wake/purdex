import { useState, useEffect, useRef, useMemo } from 'react'
import { CaretRight, CaretDown, FloppyDisk, ArrowCounterClockwise, X } from '@phosphor-icons/react'
import { THEME_TOKEN_KEYS, TOKEN_METADATA, tokensToCss } from '../../lib/theme-tokens'
import type { ThemeTokens, ThemeTokenKey } from '../../lib/theme-tokens'
import { getTheme } from '../../lib/theme-registry'
import { useThemeStore } from '../../stores/useThemeStore'
import { useI18nStore } from '../../stores/useI18nStore'

const TEMP_THEME_ATTR = '__theme-editor-preview'

const GROUP_I18N_KEYS: Record<string, string> = {
  surface: 'theme.group.surface',
  text: 'theme.group.text',
  border: 'theme.group.border',
  accent: 'theme.group.accent',
  terminal: 'theme.group.terminal',
  status: 'theme.group.status',
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
  // Already hex
  if (/^#[0-9a-f]{6}$/i.test(value)) return value.toLowerCase()
  if (/^#[0-9a-f]{3}$/i.test(value)) {
    const [, r, g, b] = value.match(/^#(.)(.)(.)$/)!
    return `#${r}${r}${g}${g}${b}${b}`.toLowerCase()
  }
  // Has alpha component — can't represent as hex6, keep original
  if (/rgba|hsla/i.test(value) || /\/\s*[\d.]+/.test(value)) return value
  // Use canvas for other formats (rgb, hsl, named colors)
  const ctx = document.createElement('canvas').getContext('2d')
  if (ctx) {
    ctx.fillStyle = value
    const result = ctx.fillStyle
    if (result.startsWith('#')) return result.toLowerCase()
  }
  return value
}

export function ThemeEditor({ baseThemeId, onClose }: ThemeEditorProps) {
  const baseTheme = getTheme(baseThemeId)
  const createCustomTheme = useThemeStore((s) => s.createCustomTheme)
  const setActiveTheme = useThemeStore((s) => s.setActiveTheme)
  const isEditingCustom = useThemeStore((s) => baseThemeId in s.customThemes)
  const updateCustomTheme = useThemeStore((s) => s.updateCustomTheme)
  const t = useI18nStore((s) => s.t)

  const [name, setName] = useState(() => isEditingCustom ? (baseTheme?.name ?? 'Custom') : `${baseTheme?.name ?? 'Custom'} (Custom)`)
  const [tokens, setTokens] = useState<ThemeTokens>(() => ({ ...baseTheme!.tokens }))
  const [collapsedGroups, setCollapsedGroups] = useState<Set<string>>(new Set())
  const styleRef = useRef<HTMLStyleElement | null>(null)
  const originalThemeRef = useRef(document.documentElement.dataset.theme)
  const savedRef = useRef(false)

  const groups = useMemo(() => groupTokens(), [])

  // Apply live preview
  useEffect(() => {
    const style = document.createElement('style')
    style.setAttribute('data-theme-editor', 'true')
    document.head.appendChild(style)
    styleRef.current = style
    const savedTheme = originalThemeRef.current

    return () => {
      style.remove()
      // Restore original theme attr — skip if user saved a new theme
      if (!savedRef.current && savedTheme !== undefined) {
        document.documentElement.dataset.theme = savedTheme
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

    if (isEditingCustom) {
      updateCustomTheme(baseThemeId, { name, tokens })
      setActiveTheme(baseThemeId)
    } else {
      const newId = createCustomTheme(name, baseThemeId, overrides)
      setActiveTheme(newId)
    }
    savedRef.current = true
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
          <h3 className="text-sm font-medium text-text-primary">{t('theme.editor.title')}</h3>
          <button
            aria-label={t('theme.editor.close')}
            onClick={handleCancel}
            className="p-1 rounded text-text-muted hover:text-text-primary"
          >
            <X size={16} />
          </button>
        </div>

        {/* Name input */}
        <div className="px-4 py-3 border-b border-border-subtle">
          <label className="block text-xs text-text-secondary mb-1">{t('theme.editor.name_label')}</label>
          <input
            type="text"
            aria-label={t('theme.editor.name_aria')}
            value={name}
            onChange={(e) => setName(e.target.value)}
            className="w-full bg-surface-input border border-border-default rounded-md text-text-primary text-xs px-3 py-1.5 focus:border-border-active focus:outline-none"
          />
        </div>

        {/* Token groups */}
        <div className="flex-1 overflow-y-auto px-4 py-2">
          {Object.entries(groups).map(([group, keys]) => {
            const collapsed = collapsedGroups.has(group)
            const groupKey = GROUP_I18N_KEYS[group] ?? group
            return (
              <div key={group} className="mb-2">
                <button
                  onClick={() => toggleGroup(group)}
                  className="flex items-center gap-1 w-full py-1.5 text-xs font-medium text-text-secondary hover:text-text-primary"
                  aria-label={t('theme.editor.toggle_group', { group: t(groupKey) })}
                >
                  {collapsed ? <CaretRight size={12} /> : <CaretDown size={12} />}
                  {t(groupKey)}
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
                          {/^#[0-9a-f]{6}$/i.test(hexValue) && (
                          <input
                            type="color"
                            value={hexValue}
                            onChange={(e) => handleTokenChange(key, e.target.value)}
                            className="w-6 h-6 cursor-pointer flex-shrink-0"
                            aria-label={t('theme.editor.color_picker', { label: meta.label })}
                          />
                          )}
                          <input
                            type="text"
                            value={tokens[key]}
                            onChange={(e) => handleTokenChange(key, e.target.value)}
                            className="bg-surface-input border border-border-default rounded text-text-primary text-xs px-2 py-0.5 w-32 font-mono focus:border-border-active focus:outline-none"
                            aria-label={t('theme.editor.hex_value', { label: meta.label })}
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
            aria-label={t('theme.editor.reset')}
          >
            <ArrowCounterClockwise size={14} />
            {t('common.reset')}
          </button>
          <div className="flex items-center gap-2">
            <button
              onClick={handleCancel}
              className="px-3 py-1.5 text-xs text-text-secondary hover:text-text-primary rounded-md border border-border-default"
            >
              {t('common.cancel')}
            </button>
            <button
              onClick={handleSave}
              disabled={!name.trim()}
              className="flex items-center gap-1 px-3 py-1.5 text-xs text-text-inverse bg-accent hover:bg-accent-hover rounded-md disabled:opacity-50 disabled:cursor-not-allowed"
              aria-label={t('theme.editor.save')}
            >
              <FloppyDisk size={14} />
              {t('common.save')}
            </button>
          </div>
        </div>
      </div>
    </div>
  )
}
