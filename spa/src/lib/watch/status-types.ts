// The shape of `GET /api/watch/status` (internal/module/watch/module.go `Status`), read leniently: a field this App does
// not know is ignored, a field it needs but cannot read makes the whole answer unreadable (→ 'error').
export type HolderState = 'awake' | 'resting' | 'none'

export interface WatchHolderView {
  host_id: string
  state: HolderState
  session_id?: string
  /** RFC 3339. A Go zero time arrives as `0001-01-01T00:00:00Z`: `sinceMs` reads that as unknown. */
  since?: string
  /** A start or wake of this host's watcher is going on (`self.starting`). Absent from a daemon that does not carry it: unknown, read as false. */
  starting?: boolean
  /** RFC 3339 (`self.cooldown_until`): when a failed start or wake lets the next be tried. Absent: no cooldown, or a daemon without it. */
  cooldown_until?: string
}

/** `current` / `last`: where events go now, or went last (a remote or local holder, as this host last heard). */
export interface WatchWhere {
  alias: string
  host_id: string
  session_id: string
  state: 'awake' | 'resting'
  since?: string
}

export interface WatchWatcherView {
  host_id: string
  alias: string
  session_id: string
  state: string
  /** The tmux session of the watcher; absent from a daemon that does not carry it yet (then `pdx-watch`). */
  tmux_session?: string
}

export interface WatchStatus {
  self: WatchHolderView
  current?: WatchWhere
  last?: WatchWhere
  watcher?: WatchWatcherView
  enabled: boolean
  auto_wake: boolean
  mode: string
  targets: string[]
}

const STATES: readonly string[] = ['awake', 'resting', 'none']
const isObj = (v: unknown): v is Record<string, unknown> => typeof v === 'object' && v !== null && !Array.isArray(v)
const str = (v: unknown): string | undefined => (typeof v === 'string' ? v : undefined)

function parseWhere(v: unknown): WatchWhere | undefined {
  if (!isObj(v)) return undefined
  const host_id = str(v.host_id)
  if (host_id === undefined || (v.state !== 'awake' && v.state !== 'resting')) return undefined
  return { alias: str(v.alias) ?? '', host_id, session_id: str(v.session_id) ?? '', state: v.state, since: str(v.since) }
}

function parseWatcher(v: unknown): WatchWatcherView | undefined {
  if (!isObj(v)) return undefined
  const host_id = str(v.host_id)
  if (host_id === undefined) return undefined
  return { host_id, alias: str(v.alias) ?? '', session_id: str(v.session_id) ?? '', state: str(v.state) ?? '', tmux_session: str(v.tmux_session) || undefined }
}

/** null = unreadable (not an object, no `self`, or a `self.state` this App does not know). */
export function parseWatchStatus(raw: unknown): WatchStatus | null {
  if (!isObj(raw) || !isObj(raw.self)) return null
  const host_id = str(raw.self.host_id)
  const state = raw.self.state
  if (host_id === undefined || typeof state !== 'string' || !STATES.includes(state)) return null
  return {
    self: { host_id, state: state as HolderState, session_id: str(raw.self.session_id), since: str(raw.self.since),
      starting: typeof raw.self.starting === 'boolean' ? raw.self.starting : undefined, cooldown_until: str(raw.self.cooldown_until) || undefined },
    current: parseWhere(raw.current),
    last: parseWhere(raw.last),
    watcher: parseWatcher(raw.watcher),
    enabled: raw.enabled === true,
    auto_wake: raw.auto_wake === true,
    mode: str(raw.mode) ?? '',
    targets: Array.isArray(raw.targets) ? raw.targets.filter((x): x is string => typeof x === 'string') : [],
  }
}
