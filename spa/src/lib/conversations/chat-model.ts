// spa/src/lib/conversations/chat-model.ts — what the chat (聊天) draws from the conversation's turns (U3 spec §5): bubbles for
// the user and the agent, ONE row per chain of work (turn-row.ts), peer messages folded to one line (consecutive ones
// merged), a file chip at the end of a turn that changed files. Thinking and notes the chat does not show are left out.
import type { AgentTextItem, StepItem, SystemItem, UserItem } from './types'
import type { PanelTurn } from './panel-resolve'
import { turnRows, type TurnRun } from './turn-row'

export type ChatEntry =
  | { kind: 'user'; key: string; turnIndex: number; item: UserItem }
  | { kind: 'peer'; key: string; turnIndex: number; items: UserItem[] }
  | { kind: 'agent'; key: string; turnIndex: number; item: AgentTextItem }
  | { kind: 'work'; key: string; turnIndex: number; turnId: string; run: TurnRun }
  | { kind: 'system'; key: string; turnIndex: number; item: SystemItem }
  | { kind: 'files'; key: string; turnIndex: number; turnId: string; files: number; added: number; removed: number; firstStepId: string }

/** System items the chat shows as a line; the rest (handoff, model change, notices) are the deck's business. */
const CHAT_SYSTEM = new Set(['interrupted', 'compacted'])

/** The files a run of steps changed: distinct paths, and the lines added / removed over every edit that took effect. */
export function fileSummary(steps: StepItem[]): { files: number; added: number; removed: number } | null {
  const paths = new Set<string>()
  let added = 0
  let removed = 0
  for (const s of steps) {
    if (s.kind !== 'edit' || !s.diff || s.status === 'failed' || s.status === 'denied') continue
    paths.add(s.diff.path)
    added += s.diff.added
    removed += s.diff.removed
  }
  return paths.size === 0 ? null : { files: paths.size, added, removed }
}

export function buildChat(turns: PanelTurn[]): ChatEntry[] {
  const out: ChatEntry[] = []
  for (const turn of turns) {
    const rows = turnRows(turn)
    for (const seg of rows.segments) {
      if (seg.kind === 'run') {
        out.push({ kind: 'work', key: `w:${seg.run.stepIds[0]}`, turnIndex: turn.index, turnId: turn.id, run: seg.run })
        continue
      }
      const it = seg.item
      if (it.type === 'user') {
        const user = it as UserItem
        if (user.source === 'command_output') continue
        const last = out[out.length - 1]
        if (user.source === 'peer') {
          // iOS 0.6.44: only peer messages that follow each other directly, inside one turn, are one line; any other row
          // between them (a reply, a work row, a user message) or a turn boundary splits them.
          if (last?.kind === 'peer' && last.turnIndex === turn.index) last.items.push(user)
          else out.push({ kind: 'peer', key: `p:${user.id}`, turnIndex: turn.index, items: [user] })
        } else out.push({ kind: 'user', key: `u:${user.id}`, turnIndex: turn.index, item: user })
      } else if (it.type === 'agent_text') out.push({ kind: 'agent', key: `a:${it.id}`, turnIndex: turn.index, item: it as AgentTextItem })
      else if (it.type === 'system' && CHAT_SYSTEM.has((it as SystemItem).kind)) out.push({ kind: 'system', key: `s:${it.id}`, turnIndex: turn.index, item: it as SystemItem })
    }
    const sum = fileSummary(rows.runs.flatMap((r) => r.steps))
    if (sum) {
      const first = rows.runs.find((r) => fileSummary(r.steps))!
      out.push({ kind: 'files', key: `f:${turn.id}`, turnIndex: turn.index, turnId: turn.id, ...sum, firstStepId: first.stepIds[0] })
    }
  }
  return out
}
