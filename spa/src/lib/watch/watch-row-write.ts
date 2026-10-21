// spa/src/lib/watch/watch-row-write.ts — W5c1: writing the 守門員 settings. The `watch` host-config row is written whole
// (`PUT /api/hostconfig/watch`, `{items, baseRevision}`; the same compare-and-set write as lib/host-config-api.ts): a 409 carries the daemon's
// copy, any other refusal carries the daemon's plain-text reason. The defaults mirror internal/watchsettings `Default()`.
import { hostFetch } from '../host-api'
import { HostConfigApiError, HostConfigConflictError } from '../host-config-api'
import { parseWatchRow, type WatchRow } from './watch-row'

export const WATCH_DEFAULTS = {
  enabled: true, x_cores: 1.5, y_mb: 1024, t_s: 60, pressure_level: 2, pressure_t_s: 120, page_rate: 50, page_t_s: 60,
  targets: ['air26', 'mlab'], mode: 'patrol', patrol_idle_min: 120, auto_wake: true,
  agent_command: 'claude --dangerously-skip-permissions --model opus', workdir: '~/Workspace/wake/mlab-purdex-watch', start_prompt: '', wake_prompt: '',
} as const

export type WatchKey = keyof typeof WATCH_DEFAULTS

/** The fields a PUT must carry (the daemon refuses a row without them). */
const REQUIRED: WatchKey[] = ['enabled', 'x_cores', 'y_mb', 't_s', 'pressure_level', 'pressure_t_s', 'page_rate', 'page_t_s']

/** Where a value comes from: the row sets it, or the daemon's default stands. A never-written row (revision 0) sets nothing. */
export function isSet(w: WatchRow, key: WatchKey): boolean {
  if (w.revision === 0) return false
  if (key === 'agent_command') return w.agentCommand !== ''
  const v = w.row[key]
  if (v === undefined || v === null) return false
  return !((key === 'start_prompt' || key === 'wake_prompt') && v === '')
}

/** The value shown for a field: the row's, else the default. */
export function valueOf(w: WatchRow, key: WatchKey): unknown {
  if (key === 'agent_command') return w.agentCommand || WATCH_DEFAULTS.agent_command
  return w.row[key] ?? WATCH_DEFAULTS[key]
}

/** The whole row with one field changed: every field the daemon sent (unknown ones too) goes back, the old `model` spelling becomes
 *  the command it stood for (the daemon refuses both at once), and a required threshold the row lacks is filled from the default. */
export function rowWith(w: WatchRow, key: WatchKey, value: unknown): Record<string, unknown> {
  const items: Record<string, unknown> = { ...w.row }
  if ('model' in items) {
    delete items.model
    if (w.agentCommand) items.agent_command = w.agentCommand
  }
  for (const k of REQUIRED) if (items[k] === undefined) items[k] = WATCH_DEFAULTS[k]
  items[key] = value
  return items
}

/**
 * The stored row after a write. The daemon answers a PUT (200) and a lost compare-and-set (409) alike with the GET's shape
 * `{items, revision, invalid?, dropped?}`: internal/module/hostconfig/handler.go:177 `writeJSON(w, status, collectionOf("put", key, entry))`,
 * the struct at handler.go:18-23, `collectionOf` at handler.go:59-72 (a stored row is normalised, so `items` is always an object). So
 * the answer is the new row and there is no second GET; one that does not read is a failure, not a case to work around.
 * 409 → HostConfigConflictError carrying that same shape.
 */
export async function putWatchRow(hostId: string, items: Record<string, unknown>, baseRevision: number): Promise<WatchRow> {
  const res = await hostFetch(hostId, '/api/hostconfig/watch', {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ items, baseRevision }),
  })
  if (res.status === 409) {
    throw new HostConfigConflictError((await res.json().catch(() => ({ items: null, revision: 0 }))) as { items: unknown; revision: number })
  }
  if (!res.ok) {
    const text = (await res.text().catch(() => '')).trim()
    throw new HostConfigApiError(res.status, text || `${res.status} ${res.statusText}`.trim())
  }
  const stored = parseWatchRow(await res.json().catch(() => null))
  if (!stored) throw new HostConfigApiError(res.status, 'unreadable answer')
  return stored
}
