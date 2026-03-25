export const THEME_TOKEN_KEYS = [
  // Surface
  'surface-primary', 'surface-secondary', 'surface-tertiary',
  'surface-elevated', 'surface-hover', 'surface-active', 'surface-input',
  // Text
  'text-primary', 'text-secondary', 'text-muted', 'text-inverse',
  // Border
  'border-default', 'border-active', 'border-subtle',
  // Accent
  'accent', 'accent-hover', 'accent-muted',
  // Terminal
  'terminal-bg', 'terminal-fg', 'terminal-cursor',
  // Status
  'status-error', 'status-warning', 'status-success',
] as const

export type ThemeTokenKey = (typeof THEME_TOKEN_KEYS)[number]
export type ThemeTokens = Record<ThemeTokenKey, string>

interface TokenMeta {
  label: string
  group: 'surface' | 'text' | 'border' | 'accent' | 'terminal' | 'status'
}

export const TOKEN_METADATA: Record<ThemeTokenKey, TokenMeta> = {
  'surface-primary':   { label: 'Primary Background',    group: 'surface' },
  'surface-secondary': { label: 'Secondary Background',  group: 'surface' },
  'surface-tertiary':  { label: 'Tertiary Background',   group: 'surface' },
  'surface-elevated':  { label: 'Elevated Surface',      group: 'surface' },
  'surface-hover':     { label: 'Hover State',           group: 'surface' },
  'surface-active':    { label: 'Active/Selected',       group: 'surface' },
  'surface-input':     { label: 'Input Background',      group: 'surface' },
  'text-primary':      { label: 'Primary Text',          group: 'text' },
  'text-secondary':    { label: 'Secondary Text',        group: 'text' },
  'text-muted':        { label: 'Muted Text',            group: 'text' },
  'text-inverse':      { label: 'Inverse Text',          group: 'text' },
  'border-default':    { label: 'Default Border',        group: 'border' },
  'border-active':     { label: 'Active Border',         group: 'border' },
  'border-subtle':     { label: 'Subtle Border',         group: 'border' },
  'accent':            { label: 'Accent',                group: 'accent' },
  'accent-hover':      { label: 'Accent Hover',          group: 'accent' },
  'accent-muted':      { label: 'Accent Muted',          group: 'accent' },
  'terminal-bg':       { label: 'Terminal Background',    group: 'terminal' },
  'terminal-fg':       { label: 'Terminal Foreground',    group: 'terminal' },
  'terminal-cursor':   { label: 'Terminal Cursor',        group: 'terminal' },
  'status-error':      { label: 'Error',                  group: 'status' },
  'status-warning':    { label: 'Warning',                group: 'status' },
  'status-success':    { label: 'Success',                group: 'status' },
}

/** Strip characters that could escape a CSS custom property context */
function sanitizeCssValue(value: string): string {
  return value.replace(/[{}<>]/g, '').replace(/;/g, '')
}

/** Convert ThemeTokens to CSS variable declarations */
export function tokensToCss(tokens: ThemeTokens): string {
  return Object.entries(tokens)
    .map(([key, value]) => `--${key}: ${sanitizeCssValue(value)};`)
    .join(' ')
}
