// spa/src/lib/conversations/terminal-adapter.ts — the terminal session's deck adapter (U4-2): today's behaviour, unchanged — the session's mod takes the
// message (POST /api/conversations/claude/{sid}/submit, the App-side queue waiting for idle), files are saved by the daemon's upload endpoint (named
// by the tmux session code), the draft is the deck's own memory.
import { agentUploadToPath } from '../host-api'
import type { DeckAdapter } from './deck-adapter'
import { hostSendPort } from './send'

/** No session code: no attachments (the daemon's upload names the session by it). */
export function terminalAdapter(hostId: string, sessionId: string, sessionCode?: string): DeckAdapter {
  return {
    kind: 'terminal',
    port: () => hostSendPort(hostId, sessionId),
    upload: sessionCode ? (file, o) => agentUploadToPath(hostId, file, sessionCode, o) : null,
  }
}
