import { THEME_TOKEN_KEYS } from './theme-tokens'
import type { ThemeTokens, ThemeTokenKey } from './theme-tokens'
import type { ThemeImportPayload } from '../stores/useThemeStore'

export function parseAndValidate(raw: unknown): ThemeImportPayload | string {
  if (!raw || typeof raw !== 'object') return 'Invalid JSON'
  const obj = raw as Record<string, unknown>
  if (typeof obj.name !== 'string' || !obj.name.trim()) return 'Missing "name" field'
  if (!obj.tokens || typeof obj.tokens !== 'object') return 'Missing "tokens" field'
  const validKeys = new Set(THEME_TOKEN_KEYS as readonly string[])
  const tokens: Partial<ThemeTokens> = {}
  let hasValid = false
  for (const [k, v] of Object.entries(obj.tokens as Record<string, unknown>)) {
    if (validKeys.has(k) && typeof v === 'string') {
      tokens[k as ThemeTokenKey] = v
      hasValid = true
    }
  }
  if (!hasValid) return 'No valid token keys found'
  return { name: obj.name.trim(), tokens }
}
