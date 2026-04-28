// PathHint v1 minimal — must mirror daemon `internal/module/agent/path_hint.go`.
// Dir-level only (no `path`, no `basename`). HostId is carried by the WS
// envelope (HostEvent), never in this payload.

export const PATH_HINT_SCHEMA_VERSION = 1 as const

export const PATH_HINT_KIND = ['read', 'write', 'edit'] as const
export type PathHintKind = (typeof PATH_HINT_KIND)[number]

export interface PathHint {
  schemaVersion: 1
  agentId: string  // 'cc' today; 'codex' / 'opencode' reserved for future bumps
  sessionCode: string
  cwd: string      // agent's working dir; cache scope key
  dir: string      // touched file's parent dir (absolute)
  kind: PathHintKind
  timestamp: string  // ISO 8601 from daemon time.Time MarshalJSON
}

export function isValidPathHintKind(v: unknown): v is PathHintKind {
  return typeof v === 'string' && (PATH_HINT_KIND as readonly string[]).includes(v)
}
