// spa/src/lib/conversations/terminal-adapter.ts — the terminal session's deck adapter (U4-2, K-2): a message is typed by the daemon
// (POST /api/sessions/{code}/submit), files are saved by the daemon's upload endpoint (named by the tmux session code), the draft is
// the deck's own memory. The send target (the session code and the tmux generation the pane believes in) is a GETTER, read at each
// submit, so a generation stamped after the queue was made is the one sent; no target sends nothing.
import { agentUploadToPath } from '../host-api'
import { useSessionStore } from '../../stores/useSessionStore'
import { useTabStore } from '../../stores/useTabStore'
import { findPane } from '../pane-tree'
import type { DeckAdapter } from './deck-adapter'
import { typedRecall, typedSendNow, typedSendPort, type SubmitTarget } from './send'

/** How a terminal message comes back when Claude Code queued it: whenever it reads it, as `user` or `queued`. Same as a worker's. */
const ECHO = { sources: ['user', 'queued'], afterMs: 10 * 60_000 } as const

/**
 * The live target of a pane showing tmux session `code` on `hostId`: the generation on the pane's content (`''` = unknown, a legacy record,
 * stamped later by `adoptTmuxInstance`), else the session list's `tmux_instance` for that code. Null when neither knows it.
 */
export function paneSendTarget(paneId: string, hostId: string, code: string): SubmitTarget | null {
  if (!code) return null
  let fromPane = ''
  const { tabs } = useTabStore.getState()
  for (const id of Object.keys(tabs)) {
    const p = findPane(tabs[id].layout, paneId)
    if (p) {
      if (p.content.kind === 'tmux-session') fromPane = p.content.tmuxInstance
      break
    }
  }
  const instance = fromPane || useSessionStore.getState().sessions[hostId]?.find((s) => s.code === code)?.tmux_instance || ''
  return instance ? { code, instance } : null
}

/** No session code: no attachments (the daemon's upload names the session by it). No `target`: a send says `no_instance` and goes nowhere. */
export function terminalAdapter(hostId: string, sessionId: string, sessionCode?: string, target: () => SubmitTarget | null = () => null): DeckAdapter {
  return {
    kind: 'terminal',
    // a live mod owner says `prompt`; a live conversation with none says `keys` (the daemon decides per request); the live capability is the only authority
    canSend: (capabilities) => capabilities?.send === 'prompt' || capabilities?.send === 'keys',
    recall: true,
    takeBack: typedRecall(hostId, target),
    sendNow: typedSendNow(hostId, target),
    // a message Claude Code queued while busy is written to the transcript when it reads it (source `queued`, no client_msg_id), minutes later
    port: () => ({ ...typedSendPort(hostId, sessionId, target), echo: ECHO }),
    upload: sessionCode ? (file, o) => agentUploadToPath(hostId, file, sessionCode, o) : null,
  }
}
