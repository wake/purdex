// spa/src/lib/nex/conversation-search.ts — the 已退出 / 已消失 rows' cwd display and search
// (conversation entity spec §13.3: cwd with home shortened to `~`; search is metadata only).
import type { ConversationRow } from './conversations-api'

/**
 * `cwd` with the host's home shortened: `home` itself → `~`, a path under it → `~/…`, anything else unchanged.
 * A trailing slash on `home` is ignored; an empty home, or `/`, shortens nothing.
 */
export function shortenHome(cwd: string, home: string): string {
  const h = home.replace(/\/+$/, '')
  if (h === '') return cwd
  if (cwd === h) return '~'
  if (cwd.startsWith(`${h}/`)) return `~${cwd.slice(h.length)}`
  return cwd
}

/**
 * Case-insensitive substring match of the trimmed query over the title, the cwd as displayed (`~/…`) and as it is,
 * the first human prompt and the session id. A blank query matches every row.
 */
export function matchesConversationQuery(row: ConversationRow, q: string, home: string): boolean {
  const needle = q.trim().toLowerCase()
  if (needle === '') return true
  const cwd = row.cwd ?? ''
  const fields = [row.title, cwd === '' ? '' : shortenHome(cwd, home), cwd, row.first_prompt ?? '', row.session_id]
  return fields.some((s) => s.toLowerCase().includes(needle))
}
