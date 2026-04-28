// spa/src/lib/agent-ws/path-hint-dispatch.ts
// agent.path_hint dispatcher → usePathCacheStore.add().
// Defensive on every untrusted boundary: malformed JSON, wrong schema
// version, non-absolute dir, unresolvable workspace, and resolver throws
// all drop silently — never crash the WS pipeline.
import type { HostEvent } from '../host-events'
import type { PathHint } from '../../types/agent-events'
import {
  PATH_HINT_SCHEMA_VERSION,
  isValidPathHintKind,
} from '../../types/agent-events'
import { resolveWorkspaceIdForAgentSession } from './resolve-workspace-id-for-agent-session'
import { usePathCacheStore } from '../../stores/path-cache/usePathCacheStore'

export function handlePathHintEvent(hostId: string, event: HostEvent): void {
  try {
    const parsed = JSON.parse(event.value) as Partial<PathHint>
    if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) return
    if (parsed.schemaVersion !== PATH_HINT_SCHEMA_VERSION) return
    if (typeof parsed.dir !== 'string' || !parsed.dir.startsWith('/')) return
    if (!isValidPathHintKind(parsed.kind)) return
    if (typeof parsed.sessionCode !== 'string' || parsed.sessionCode === '') return

    const wsId = resolveWorkspaceIdForAgentSession(hostId, parsed.sessionCode)
    if (!wsId) return
    usePathCacheStore.getState().add(hostId, wsId, parsed.dir)
  } catch {
    // malformed payload OR resolver throw — drop silently
  }
}
