// spa/src/hooks/useHeldConversation.ts — hold one conversation in `useConversationStore` for as long as the caller is mounted, and read its
// entry (U3 plan D3, moved out of useConversationOfPane for U4-1b so a worker pane can hold its Claude Code session the same way).
// No session id → nothing is held.
import { useEffect } from 'react'
import { selectConversation, useConversationStore, type ConversationEntry } from '../stores/useConversationStore'

export function useHeldConversation(hostId: string, sessionId: string): ConversationEntry | undefined {
  useEffect(() => {
    if (!sessionId) return
    return useConversationStore.getState().acquire(hostId, sessionId)
  }, [hostId, sessionId])

  return useConversationStore(sessionId ? selectConversation(hostId, sessionId) : () => undefined)
}
