// spa/src/lib/agent-ws/path-hint-dispatch.ts
// agent.path_hint dispatcher → usePathCacheStore.add().
// Defensive on every untrusted boundary: oversized payload, malformed JSON,
// schema-version drift, non-absolute / control-char fields, envelope/payload
// session mismatch (R2-D3), and store throws all drop silently — never
// crash the WS pipeline.
import type { HostEvent } from '../host-events'
import type { PathHint } from '../../types/agent-events'
import {
  PATH_HINT_SCHEMA_VERSION,
  isValidPathHintKind,
} from '../../types/agent-events'
import { usePathCacheStore } from '../../stores/path-cache/usePathCacheStore'

// Mirror daemon's MaxRawEventBytes; rejects amplification attempts that
// slipped past daemon (e.g. version skew).
const MAX_PAYLOAD_BYTES = 64 * 1024

export function handlePathHintEvent(hostId: string, event: HostEvent): void {
  try {
    if (typeof event.value !== 'string' || event.value.length > MAX_PAYLOAD_BYTES) return

    const parsed = JSON.parse(event.value) as Partial<PathHint>
    if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) return
    if (parsed.schemaVersion !== PATH_HINT_SCHEMA_VERSION) return
    if (typeof parsed.sessionCode !== 'string' || parsed.sessionCode === '') return
    if (parsed.sessionCode !== event.session) return  // envelope/payload session mismatch (R2-D3)
    if (typeof parsed.cwd !== 'string' || !parsed.cwd.startsWith('/')) return
    if (typeof parsed.dir !== 'string' || !parsed.dir.startsWith('/')) return
    if (!isValidPathHintKind(parsed.kind)) return

    // Store does its own normalize / cap / control-char rejection.
    usePathCacheStore.getState().add(hostId, parsed.cwd, parsed.sessionCode, parsed.dir)
  } catch {
    // malformed payload OR store throw — drop silently
  }
}
