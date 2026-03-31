export function compositeKey(hostId: string, sessionCode: string): string {
  return `${hostId}:${sessionCode}`
}
