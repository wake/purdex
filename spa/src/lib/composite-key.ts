import { execAgentCode } from './nex/worker-agent-status'

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
 * `exec:` prefix, so `h1:exec:e1` → `h1` / `exec:e1`.
 *
 * A key without ':' yields hostId '' and sessionCode = the whole key.
 */
export function splitCompositeKey(ck: string): { hostId: string; sessionCode: string } {
  const execIdx = ck.lastIndexOf(':' + execAgentCode(''))
  if (execIdx >= 0) return { hostId: ck.slice(0, execIdx), sessionCode: ck.slice(execIdx + 1) }
  const colonIdx = ck.lastIndexOf(':')
  if (colonIdx < 0) return { hostId: '', sessionCode: ck }
  return { hostId: ck.slice(0, colonIdx), sessionCode: ck.slice(colonIdx + 1) }
}
