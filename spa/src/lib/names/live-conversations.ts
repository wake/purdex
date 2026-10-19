// spa/src/lib/names/live-conversations.ts — how many live conversations a host has, for the first-save notice (name pool
// v2). A conversation is a peer row with a ref: a plain tmux session has none (see `PeerRow.ref`).
import { usePeerStore } from '../../stores/usePeerStore'

/** `null` when the host's inventory never arrived or its last read failed, so the notice names no number it cannot know. */
export function liveConversationCount(hostId: string): number | null {
  const entry = usePeerStore.getState().byHost[hostId]
  if (!entry || entry.fetchedAt === 0 || entry.error !== null) return null
  return Object.values(entry.rows).filter((r) => r.ref !== '').length
}
