export interface LocaleImportPayload {
  name: string
  baseLocale?: string
  translations: Record<string, string>
}

export function parseAndValidateLocale(raw: unknown): LocaleImportPayload | string {
  if (!raw || typeof raw !== 'object') return 'Invalid JSON'
  const obj = raw as Record<string, unknown>
  if (typeof obj.name !== 'string' || !obj.name.trim()) return 'Missing "name" field'
  if (!obj.translations || typeof obj.translations !== 'object') return 'Missing "translations" field'
  const translations: Record<string, string> = {}
  let hasValid = false
  for (const [k, v] of Object.entries(obj.translations as Record<string, unknown>)) {
    if (typeof v === 'string') {
      translations[k] = v
      hasValid = true
    }
  }
  if (!hasValid) return 'No valid translation keys found'
  return {
    name: obj.name.trim(),
    ...(typeof obj.baseLocale === 'string' ? { baseLocale: obj.baseLocale } : {}),
    translations,
  }
}
