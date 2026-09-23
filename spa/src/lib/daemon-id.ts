// spa/src/lib/daemon-id.ts — the ONE validator for a daemon identity
// (`HostConfig.daemonId`, `/api/info` → `host_id`; host-daemon-id spec D1).
//
// No imports on purpose: useHostStore, host-color (the persisted-state
// sanitizer) and the profile applier all use it, and it must never pull any of
// them into a cycle.
//
// The contract MIRRORS THE DAEMON and only excludes what is unsafe to store or
// render. internal/config/hostid.go `EnsureHostID` keeps ANY non-empty
// `host_id` already in config.toml unchanged, and a generated one is
// `strings.ToLower(<first label of os.Hostname()>) + ":" + <6 base36>` with no
// other normalisation — so upper case (hand-set), extra colons, spaces, Unicode
// letters and long hostnames are all real identities, and refusing them would
// make a real daemon "have no stable id" here. What is refused:
//   - a non-string, or `""` (the daemon's own "no stable id");
//   - more than DAEMON_ID_MAX_LENGTH UTF-16 code units (`String.length`);
//   - any control (`\p{Cc}`: C0/C1, NUL, newline, tab, DEL) or format
//     (`\p{Cf}`: bidi overrides / isolates / marks, zero-width chars, BOM)
//     character — they can break a log line, spoof the text around them when
//     rendered, or make two ids that look equal compare different.
// A refused id is treated like `""` wherever it is met.

/** In UTF-16 code units (`String.prototype.length`), not code points or bytes. */
export const DAEMON_ID_MAX_LENGTH = 512

const UNSAFE_CHAR_RE = /[\p{Cc}\p{Cf}]/u

export function isValidDaemonId(value: unknown): value is string {
  return typeof value === 'string' && value.length > 0 && value.length <= DAEMON_ID_MAX_LENGTH && !UNSAFE_CHAR_RE.test(value)
}
