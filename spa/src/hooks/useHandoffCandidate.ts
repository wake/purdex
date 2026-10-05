// spa/src/hooks/useHandoffCandidate.ts — may this pane offer "Hand to nex"? (shell cleanup spec §9.4; was inline in
// PaneLayoutRenderer, P-C.3b). Shared by the pane context menu and the status bar's mode buttons. The gate itself is
// pure (`lib/nex/handoff-gate`); this hook feeds it the live agent type and the host's readiness, and asks the nex host
// store to fetch that readiness.
//
// The pane gate (host ownership H2d-4, §0.21) comes first: a pane whose host is hidden in this workbench has no host
// for the nex subscriptions — no `ensure` fetch, never a candidate. Non-session panes subscribe to constants.
import { useEffect } from 'react'
import { compositeKey } from '../lib/composite-key'
import { isHandoffCandidate } from '../lib/nex/handoff-gate'
import { usePaneHostShown } from '../lib/shown-hosts'
import { useAgentStore } from '../stores/useAgentStore'
import { useNexHostStore, selectHandoffReady } from '../stores/useNexHostStore'
import type { PaneContent, TmuxSessionContent } from '../types/tab'

const notReady = () => false
/** What a missing pane hands the pane gate: not host-bearing, so always shown (and never a candidate). */
const NO_HOST: PaneContent = { kind: 'dashboard' }

export function useHandoffCandidate(content: PaneContent | null | undefined): boolean {
  const hostShown = usePaneHostShown(content ?? NO_HOST)
  const tmux: TmuxSessionContent | null = content?.kind === 'tmux-session' ? content : null
  const hostId = hostShown ? tmux?.hostId ?? null : null
  const code = tmux?.sessionCode ?? ''
  const agentType = useAgentStore((s) => (hostId ? s.agentTypes[compositeKey(hostId, code)] : undefined))
  const handoffReady = useNexHostStore(hostId ? selectHandoffReady(hostId) : notReady)
  useEffect(() => {
    if (hostId) void useNexHostStore.getState().ensure(hostId)
  }, [hostId])
  return tmux !== null && hostId !== null && isHandoffCandidate(tmux, { agentType, handoffReady })
}
