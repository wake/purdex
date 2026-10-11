// spa/src/components/deck/SessionStatusRow.tsx — the status row under a session view's input, wired to what the App already has:
// the session's Claude Code statusLine snapshot (`useAgentStore.ccStatus`, the one the Mac status bar reads) for model, effort,
// cost and (when it carries them) cwd / worktree / branch; the pane's rebuild record for the cwd otherwise. No new data path.
import { useAgentStore } from '../../stores/useAgentStore'
import { useSessionStore } from '../../stores/useSessionStore'
import { compositeKey } from '../../lib/composite-key'
import { useHostTint } from '../../lib/host-tint'
import { parseCcExtras } from '../../lib/usage-display'
import type { DeckFooterContext } from './footer-context'
import { StatusRow } from './StatusRow'
import { readWhere } from './session-line'

interface Props {
  /** The pane's tmux session code: the statusLine snapshot is keyed by host + code, not by Claude Code's session id. */
  sessionCode: string
  /** The pane's recorded cwd (rebuild record): used when the snapshot has none. */
  cwd?: string
  ctx: Pick<DeckFooterContext, 'hostId' | 'usage'>
}

export function SessionStatusRow({ sessionCode, cwd, ctx }: Props) {
  const entry = useAgentStore((s) => s.ccStatus[compositeKey(ctx.hostId, sessionCode)])
  const agentType = useAgentStore((s) => s.agentTypes[compositeKey(ctx.hostId, sessionCode)])
  // the same colour set the tab badge reads: an agent session's terminal colours, a plain terminal's console colours
  const tint = useHostTint(ctx.hostId, agentType ? 'terminal' : 'console')
  // the daemon reads the session cwd's git branch (no git process); used when the statusLine snapshot has no worktree.branch
  const listBranch = useSessionStore((s) => s.sessions[ctx.hostId]?.find((x) => x.code === sessionCode)?.branch)
  const extras = parseCcExtras(entry?.raw)
  const where = readWhere(entry?.raw)
  return (
    <StatusRow
      cwd={where.cwd ?? cwd}
      worktree={where.worktree}
      branch={where.branch ?? listBranch ?? null}
      model={extras.model ?? ctx.usage?.model ?? null}
      effort={extras.effort ?? ctx.usage?.effort ?? null}
      cost={extras.cost}
      tint={tint}
    />
  )
}
