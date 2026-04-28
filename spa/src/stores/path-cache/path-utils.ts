// Pure helpers for the path cache. Kept outside the Zustand store so the
// rehydrate sanitizer can replay the same invariants `add()` enforces (R2-F1
// caught the original sanitizer skipping these checks).

export const MAX_DIRS_PER_SCOPE = 50
export const MAX_DIR_BYTES = 4096       // PATH_MAX-class
export const MAX_CWD_BYTES = 4096

// NUL — disallowed in hostIds and paths so the composite key is unambiguous
// even when hostId contains colons (R2-A3 was about that ambiguity).
const SCOPE_SEP = '\u0000'

export function scopeKey(hostId: string, cwd: string): string {
  return `${hostId}${SCOPE_SEP}${cwd}`
}

export function parseScopeKey(key: string): { hostId: string; cwd: string } | null {
  const idx = key.indexOf(SCOPE_SEP)
  if (idx <= 0 || idx === key.length - 1) return null
  return { hostId: key.slice(0, idx), cwd: key.slice(idx + 1) }
}

function hasControlOrNul(s: string): boolean {
  for (let i = 0; i < s.length; i++) {
    const c = s.charCodeAt(i)
    if (c < 0x20 || c === 0x7f) return true
  }
  return false
}

export function normalizeDir(raw: unknown): string | null {
  if (typeof raw !== 'string' || !raw.startsWith('/')) return null
  if (raw.length > MAX_DIR_BYTES) return null
  if (hasControlOrNul(raw)) return null
  const parts: string[] = []
  for (const seg of raw.split('/')) {
    if (seg === '' || seg === '.') continue
    if (seg === '..') {
      if (parts.length > 0) parts.pop()
      continue
    }
    parts.push(seg)
  }
  return parts.length === 0 ? '/' : '/' + parts.join('/')
}

export function normalizeCwd(raw: unknown): string | null {
  if (typeof raw !== 'string' || !raw.startsWith('/')) return null
  if (raw.length > MAX_CWD_BYTES) return null
  if (hasControlOrNul(raw)) return null
  const trimmed = raw.replace(/\/+$/, '')
  return trimmed === '' ? '/' : trimmed
}

export function dirname(p: string): string {
  const idx = p.lastIndexOf('/')
  if (idx <= 0) return '/'
  return p.substring(0, idx)
}

export interface PathCacheEntry {
  dir: string         // already normalized
  sessionCode: string // tag for lookup priority
  touchedAt: number   // ms epoch
}

/** Push entry to head, dedup existing dir, cap. */
export function upsertEntry(
  list: PathCacheEntry[] | undefined,
  entry: PathCacheEntry,
): PathCacheEntry[] {
  const existing = list ?? []
  const filtered = existing.filter((e) => e.dir !== entry.dir)
  return [entry, ...filtered].slice(0, MAX_DIRS_PER_SCOPE)
}

/** Sort lookup candidates: same-session first, then by recency. */
export function rankCandidates(
  list: PathCacheEntry[],
  currentSessionCode: string | undefined,
): PathCacheEntry[] {
  if (!currentSessionCode) {
    return [...list].sort((a, b) => b.touchedAt - a.touchedAt)
  }
  const same: PathCacheEntry[] = []
  const other: PathCacheEntry[] = []
  for (const e of list) {
    if (e.sessionCode === currentSessionCode) same.push(e)
    else other.push(e)
  }
  same.sort((a, b) => b.touchedAt - a.touchedAt)
  other.sort((a, b) => b.touchedAt - a.touchedAt)
  return same.concat(other)
}

/** Sanitize one persisted entries list — drop invalid, replay normalize, cap. */
export function sanitizeEntries(raw: unknown): PathCacheEntry[] {
  if (!Array.isArray(raw)) return []
  const cleaned: PathCacheEntry[] = []
  const seen = new Set<string>()
  for (const e of raw) {
    if (!e || typeof e !== 'object') continue
    const obj = e as Record<string, unknown>
    const dir = normalizeDir(obj.dir)
    if (!dir) continue
    if (seen.has(dir)) continue
    const sessionCode = typeof obj.sessionCode === 'string' ? obj.sessionCode : ''
    const touchedAt = typeof obj.touchedAt === 'number' && Number.isFinite(obj.touchedAt) ? obj.touchedAt : 0
    cleaned.push({ dir, sessionCode, touchedAt })
    seen.add(dir)
    if (cleaned.length >= MAX_DIRS_PER_SCOPE) break
  }
  return cleaned
}
