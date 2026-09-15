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
 * A key without ':' yields hostId '' and sessionCode = the whole key.
 */
export function splitCompositeKey(ck: string): { hostId: string; sessionCode: string } {
  const colonIdx = ck.lastIndexOf(':')
  if (colonIdx < 0) return { hostId: '', sessionCode: ck }
  return { hostId: ck.slice(0, colonIdx), sessionCode: ck.slice(colonIdx + 1) }
}
