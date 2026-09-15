import type { WorkspaceSnapshot } from './types'

/**
 * A derived, host-scoped view of the captured snapshot (host-launcher spec
 * §4.3). Used only as the INPUT of a host page's "rebuild all sessions", so
 * that action can never create a session on another host. Never written back:
 * edits go through `setSessionMetaCwd` on the full snapshot.
 */
export function filterSnapshotByHost(snap: WorkspaceSnapshot, hostId: string): WorkspaceSnapshot {
  const own = Object.prototype.hasOwnProperty.call(snap.sessionMeta, hostId) ? snap.sessionMeta[hostId] : undefined
  return { ...snap, sessionMeta: own ? { [hostId]: own } : {} }
}

/** Which host page shows the client-scoped (whole-device) snapshot block. */
export function selectSnapshotClientHostId(s: {
  devHostId: string | null
  hosts: Record<string, unknown>
  hostOrder: string[]
}): string | null {
  if (s.devHostId !== null && s.hosts[s.devHostId]) return s.devHostId
  return s.hostOrder.find((id) => !!s.hosts[id]) ?? null
}
