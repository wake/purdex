// spa/src/components/deck/TurnLines.tsx — the live line at the end of the stream and the closing line of a finished turn (c9d9c7-54 pts 3, 5;
// lib/conversations/turn-line.ts has the data rules). Both are the system rows' grey, left-aligned, one size under the text; neither is
// interactive. The live line ticks once a second for its elapsed time and nothing else moves.
import { useNow } from '../../hooks/useNow'
import { useI18nStore } from '../../stores/useI18nStore'
import { formatClock } from '../../lib/conversations/deck-format'
import { doneLine, formatDuration, liveLine, type LineTurn } from '../../lib/conversations/turn-line'

const ROW = 'flex items-center gap-1.5 text-left text-xs text-text-system'

/** The turn's closing line: `✻ Done · 20s · 02:58`. Draws nothing for a turn that did not finish as `done`. */
export function DoneTurnLine({ turn }: { turn: LineTurn }) {
  const t = useI18nStore((s) => s.t)
  const done = doneLine(turn)
  if (!done) return null
  return (
    <div data-testid="turn-done" className={ROW}>
      <span aria-hidden="true">✻</span>
      <span>{[t('deck.done'), formatDuration(done.durationMs, t), formatClock(done.endedAt)].join(' · ')}</span>
    </div>
  )
}

/** The live line: `✻ Running · Bash · 2 subagents · 3m 03s`, `✻ Thinking… · 3m 03s` when no tool or subagent runs, or `✻ Waiting for you`. Draws nothing while the agent is idle. */
export function LiveTurnLine({ turns, status }: { turns: readonly LineTurn[]; status: string | undefined }) {
  const t = useI18nStore((s) => s.t)
  const live = liveLine(turns, status)
  const now = useNow(live?.kind === 'running')
  if (!live) return null
  if (live.kind === 'waiting') {
    return (
      <div data-testid="turn-live" data-kind="waiting" role="status" className="flex items-center gap-1.5 text-left text-xs text-status-warning-text">
        <span aria-hidden="true">✻</span>
        <span>{t('chat.status.waiting')}</span>
      </div>
    )
  }
  // No tool and no subagent running: the model is thinking or writing (the conversation cannot tell which, as in the terminal).
  const working = live.tool !== null || live.subagents > 0
  const parts = [t(working ? 'deck.live.running' : 'deck.live.thinking')]
  if (live.tool) parts.push(live.tool)
  if (live.subagents > 0) parts.push(t('deck.live.subagents', { n: live.subagents }))
  if (live.since !== null) parts.push(formatDuration(Math.max(0, now - live.since), t))
  return (
    <div data-testid="turn-live" data-kind="running" className={ROW}>
      <span aria-hidden="true" className="inline-block animate-pulse">✻</span>
      <span>{parts.join(' · ')}</span>
    </div>
  )
}
