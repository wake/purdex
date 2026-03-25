import { THEME_TOKEN_KEYS } from './theme-tokens'
import type { ThemeTokens, ThemeTokenKey } from './theme-tokens'
import type { ThemeImportPayload } from '../stores/useThemeStore'

/** Check that a token value looks like a valid CSS color (hex, rgb, rgba, hsl, hsla, named) */
const CSS_COLOR_RE =
  /^(#[0-9a-f]{3,8}|rgba?\([\d\s,./%]+\)|hsla?\([\d\s,./%]+\)|[a-z]{3,20})$/i

/** Characters that must never appear in an imported token value */
const DANGEROUS_RE = /[{}<>@]/

function isValidTokenValue(value: string): boolean {
  if (DANGEROUS_RE.test(value)) return false
  return CSS_COLOR_RE.test(value.trim())
}

export function parseAndValidate(raw: unknown): ThemeImportPayload | string {
  if (!raw || typeof raw !== 'object') return 'Invalid JSON'
  const obj = raw as Record<string, unknown>
  if (typeof obj.name !== 'string' || !obj.name.trim()) return 'Missing "name" field'
  if (!obj.tokens || typeof obj.tokens !== 'object') return 'Missing "tokens" field'
  const validKeys = new Set(THEME_TOKEN_KEYS as readonly string[])
  const tokens: Partial<ThemeTokens> = {}
  let hasValid = false
  for (const [k, v] of Object.entries(obj.tokens as Record<string, unknown>)) {
    if (validKeys.has(k) && typeof v === 'string' && isValidTokenValue(v)) {
      tokens[k as ThemeTokenKey] = v.trim()
      hasValid = true
    }
  }
  if (!hasValid) return 'No valid token keys found'
  return { name: obj.name.trim(), tokens }
}
