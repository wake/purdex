// spa/src/lib/daemon-id.ts — the ONE validator for a daemon identity
// (`HostConfig.daemonId`, `/api/info` → `host_id`; host-daemon-id spec D1).
//
// No imports on purpose: useHostStore, host-color (the persisted-state
// sanitizer) and the profile applier all use it, and it must never pull any of
// them into a cycle.
//
// What the daemon emits (internal/config/hostid.go `EnsureHostID`):
//   `<shortHostname>:<randomCode(6)>`
//   - shortHostname = os.Hostname() cut at the first '.', lowercased; "unknown"
//     when the hostname cannot be read;
//   - randomCode    = 6 chars of [0-9a-z].
// The pattern is a little wider than that — dots, underscores and a code of any
// length — so a hostname label with `_` or a hand-set `host_id` of the same
// shape still passes. Anything else (whitespace, control or Unicode format
// characters such as bidi overrides, non-ASCII, upper case, a missing or extra
// colon, over DAEMON_ID_MAX_LENGTH) is not an identity this SPA stores, syncs or
// trusts: it is treated like `""` — "the daemon has no stable id".

export const DAEMON_ID_MAX_LENGTH = 128

const DAEMON_ID_RE = /^[a-z0-9._-]+:[0-9a-z]+$/

export function isValidDaemonId(value: unknown): value is string {
  return typeof value === 'string' && value.length <= DAEMON_ID_MAX_LENGTH && DAEMON_ID_RE.test(value)
}
