// spa/src/lib/register-themes.ts
import { registerTheme } from './theme-registry'
import type { ThemeTokens } from './theme-tokens'

const darkTokens: ThemeTokens = {
  'surface-primary': '#0a0a1a',
  'surface-secondary': '#12122a',
  'surface-tertiary': '#08081a',
  'surface-elevated': '#1e1e3e',
  'surface-hover': '#1a1a32',
  'surface-active': '#272444',
  'surface-input': '#2a2a2a',
  'text-primary': '#e0e0e0',
  'text-secondary': '#9ca3af',
  'text-muted': '#6b7280',
  'text-inverse': '#0a0a1a',
  'border-default': '#404040',
  'border-active': '#7a6aaa',
  'border-subtle': '#2a2a2a',
  'accent': '#7a6aaa',
  'accent-hover': '#8a7aba',
  'accent-muted': 'rgba(122, 106, 170, 0.2)',
  'terminal-bg': '#0a0a1a',
  'terminal-fg': '#e0e0e0',
  'terminal-cursor': '#e0e0e0',
  'status-error': '#4a3038',
  'status-warning': '#4a4028',
  'status-success': '#2a4a3a',
}

export function registerBuiltinThemes(): void {
  registerTheme({ id: 'dark', name: 'Dark', tokens: darkTokens, builtin: true })
}
