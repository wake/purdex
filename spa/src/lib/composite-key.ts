// The `useAgentStore` key namespace for a worker (execution) tab (worker-pane
// theme spec §8.1) — never collides with a tmux session code. Defined here,
// not in `nex/worker-agent-status`, so this core module has no dependency on
// the `nex/` feature; `nex/worker-agent-status.ts` imports it back from here.
export const EXEC_PREFIX = 'exec:'
const EXEC_MARKER = ':' + EXEC_PREFIX

export function compositeKey(hostId: string, sessionCode: string): string {
  return `${hostId}:${sessionCode}`
}

/**
 * Inverse of compositeKey.
 *
 * Why lastIndexOf: sessionCode is a fixed 6-char base36 token and never
 * contains ':', while hostId may (e.g. "mlab:abc123"). Splitting on the
 * first colon would truncate such hostIds to "mlab".
 *
 * The exception is a worker (execution) agent key `exec:<id>` (worker-pane
 * theme spec §8.1), which carries a colon of its own: it is split before its
 * `exec:` prefix, so `h1:exec:e1` → `h1` / `exec:e1`. That split only
 * applies when the `:exec:` marker sits immediately before the final
 * segment — i.e. nothing after it contains another ':' — so a hostId that
 * happens to embed `:exec:` mid-string (e.g. `a:exec:b:ses001`, a plain tmux
 * key) still falls back to the last-colon split instead of being misread as
 * a worker key.
 *
 * A key without ':' yields hostId '' and sessionCode = the whole key.
 */
export function splitCompositeKey(ck: string): { hostId: string; sessionCode: string } {
  const execIdx = ck.lastIndexOf(EXEC_MARKER)
  if (execIdx >= 0 && !ck.slice(execIdx + EXEC_MARKER.length).includes(':')) {
    return { hostId: ck.slice(0, execIdx), sessionCode: ck.slice(execIdx + 1) }
  }
  const colonIdx = ck.lastIndexOf(':')
  if (colonIdx < 0) return { hostId: '', sessionCode: ck }
  return { hostId: ck.slice(0, colonIdx), sessionCode: ck.slice(colonIdx + 1) }
}
