// spa/src/lib/team/compaction-api.ts — SPA client for `GET` / `PUT /api/team/compaction/{session_id}` (compaction-control spec §8).
// null in an override = follow the host default; PUT replaces the whole override.
import { send } from './approval-api'

export interface CompactionOverride { auto: boolean | null; threshold: number | null; idle: boolean | null }
export interface CompactionEffective { managed: boolean; reason?: 'self_relay' | 'member_auto'; auto: boolean; threshold: number; idle: boolean }
export interface CompactionView { role: string; override: CompactionOverride; effective: CompactionEffective }

const path = (sessionId: string) => `/api/team/compaction/${encodeURIComponent(sessionId)}`

export function getCompaction(hostId: string, sessionId: string, signal?: AbortSignal): Promise<CompactionView> {
  return send<CompactionView>(hostId, path(sessionId), { method: 'GET', ...(signal ? { signal } : {}) })
}

export function putCompaction(hostId: string, sessionId: string, override: CompactionOverride): Promise<CompactionView> {
  return send<CompactionView>(hostId, path(sessionId), {
    method: 'PUT', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(override),
  })
}
