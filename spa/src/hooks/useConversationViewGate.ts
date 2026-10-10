// spa/src/hooks/useConversationViewGate.ts — may this pane show the deck / chat view? (U3 plan D3, U3-1a). The pane must
// be a live tmux session of Claude Code on a host that serves the conversation API. Same shape and the same reasons as
// the handoff gate (`useHandoffGate`) where they overlap, so the status bar words them once; the extra reason is
// `no_conversations`: the host's daemon does not list `conversations.v1`. Subscribes to constants for a non-session pane.
import { useEffect } from 'react'
import { compositeKey } from '../lib/composite-key'
import { runsClaudeCode } from '../lib/nex/handoff-gate'
import { usePaneHostShown } from '../lib/shown-hosts'
import { useAgentStore } from '../stores/useAgentStore'
import { useNexHostStore, selectConversationsV1 } from '../stores/useNexHostStore'
import type { PaneContent, TmuxSessionContent } from '../types/tab'

export type ViewBlockReason = 'not_session' | 'terminated' | 'host_hidden' | 'not_agent' | 'no_conversations'

export interface ConversationViewGate {
  ok: boolean
  reason: ViewBlockReason | null
}

const OPEN: ConversationViewGate = { ok: true, reason: null }
const closed = (reason: ViewBlockReason): ConversationViewGate => ({ ok: false, reason })

const NO_HOST: PaneContent = { kind: 'dashboard' }
const absent = () => false

export function useConversationViewGate(content: PaneContent | null | undefined): ConversationViewGate {
  const hostShown = usePaneHostShown(content ?? NO_HOST)
  const tmux: TmuxSessionContent | null = content?.kind === 'tmux-session' ? content : null
  const hostId = hostShown ? tmux?.hostId ?? null : null
  const agentType = useAgentStore((s) => (hostId && tmux ? s.agentTypes[compositeKey(hostId, tmux.sessionCode)] : undefined))
  const served = useNexHostStore(hostId ? selectConversationsV1(hostId) : absent)
  useEffect(() => {
    if (hostId) void useNexHostStore.getState().ensure(hostId)
  }, [hostId])
  if (!tmux) return closed('not_session')
  if (tmux.terminated) return closed('terminated')
  if (!hostId) return closed('host_hidden')
  if (!runsClaudeCode(tmux, agentType)) return closed('not_agent')
  if (!served) return closed('no_conversations')
  return OPEN
}
