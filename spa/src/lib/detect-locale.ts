export function detectLocale(browserLanguages: readonly string[], registeredIds: string[]): string {
  const idSet = new Set(registeredIds)
  // Pass 1: exact match
  for (const lang of browserLanguages) {
    if (idSet.has(lang)) return lang
  }
  // Pass 2: prefix match (en-US → en)
  for (const lang of browserLanguages) {
    const prefix = lang.split('-')[0]
    if (idSet.has(prefix)) return prefix
  }
  return 'en'
}
