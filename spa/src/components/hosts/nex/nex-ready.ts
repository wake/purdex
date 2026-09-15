// spa/src/components/hosts/nex/nex-ready.ts — the one "is the Nexen engine
// serving?" rule shared by the status badge and the executions table.
import type { NexInfo } from '../../../lib/host-api'

// A daemon older than P-B.3 reports only `configured`/`mounted` in
// `/api/info.nex` (spec §4.4.2); a mounted module there is serving.
export function isNexReady(info: NexInfo | null | undefined): boolean {
  if (!info) return false
  if (info.ready === undefined) return info.mounted
  return info.ready
}
