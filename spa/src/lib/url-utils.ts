const ALLOWED_SCHEMES = ['http:', 'https:']

export function normalizeUrl(input: string): string | null {
  const trimmed = input.trim()
  if (!trimmed) return null

  let urlStr = trimmed
  if (!trimmed.includes('://')) {
    urlStr = `https://${trimmed}`
  }

  try {
    const parsed = new URL(urlStr)
    if (!ALLOWED_SCHEMES.includes(parsed.protocol)) return null
    return parsed.href
  } catch {
    return null
  }
}
