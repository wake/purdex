// spa/src/components/deck/TurnLines.tsx — the live line at the end of the stream and the closing line of a finished turn (c9d9c7-54 pts 3, 5;
// lib/conversations/turn-line.ts has the data rules). Both are the system rows' grey, left-aligned, one size under the text; neither is
// interactive. The live line ticks once a second for its elapsed time and nothing else moves.
import { useNow } from '../../hooks/useNow'
import { useI18nStore } from '../../stores/useI18nStore'
import { formatClock } from '../../lib/conversations/deck-format'
import { backgroundText, doneLine, formatDuration, formatTokens, liveLine, type LineTurn } from '../../lib/conversations/turn-line'
import type { HeaderBackground } from '../../lib/conversations/types'

const ROW = 'flex items-center gap-1.5 text-left deck-t-down text-text-system'

/** The turn's closing line: `✻ Done · 20s · ↓ 3.1k · 02:58 · 1 background command still running` (the tokens when the turn reports them; the background work for the last turn).  Draws nothing for a turn that did not finish as `done`. */
export function DoneTurnLine({ turn, background }: { turn: LineTurn; /** The header's background work: given for the conversation's last turn only (it is what runs now). */ background?: HeaderBackground }) {
  const t = useI18nStore((s) => s.t)
  const done = doneLine(turn)
  if (!done) return null
  const still = backgroundText(background, t)
  return (
    <div data-testid="turn-done" className={ROW}>
      <span aria-hidden="true">✻</span>
      <span>{[t('deck.done'), formatDuration(done.durationMs, t), ...(done.outputTokens !== null ? [`↓ ${formatTokens(done.outputTokens)}`] : []), formatClock(done.endedAt), ...(still ? [t('deck.bg.still', { what: still })] : [])].join(' · ')}</span>
    </div>
  )
}

/** The live line: `✻ Running · Bash · 2 subagents · ↓ 9.2k · 3m 03s`, `✻ Thinking… · 3m 03s` when no tool or subagent runs, or `✻ Waiting for you`. Draws nothing while the agent is idle. */
export function LiveTurnLine({ turns, status, background }: { turns: readonly LineTurn[]; status: string | undefined; /** The header's background work: said on the running line, and on the closing line once the turn is over. */ background?: HeaderBackground }) {
  const t = useI18nStore((s) => s.t)
  const live = liveLine(turns, status)
  const now = useNow(live?.kind === 'running')
  if (!live) return null
  if (live.kind === 'waiting') {
    return (
      <div data-testid="turn-live" data-kind="waiting" role="status" className="flex items-center gap-1.5 text-left deck-t-down text-status-warning-text">
        <span aria-hidden="true">✻</span>
        <span>{t('chat.status.waiting')}</span>
      </div>
    )
  }
  // No tool and no subagent running: the model is thinking or writing (the conversation cannot tell which, as in the terminal).
  const parts = [t(live.busy ? 'deck.live.running' : 'deck.live.thinking')]
  if (live.tool) parts.push(live.tool)
  if (live.subagents > 0) parts.push(t('deck.live.subagents', { n: live.subagents }))
  if (live.outputTokens !== null) parts.push(`↓ ${formatTokens(live.outputTokens)}`)
  const bg = backgroundText(background, t)
  if (bg) parts.push(bg)
  if (live.since !== null) parts.push(formatDuration(Math.max(0, now - live.since), t))
  return (
    <div data-testid="turn-live" data-kind="running" className={ROW}>
      <span aria-hidden="true" className="inline-block animate-pulse">✻</span>
      <span>{parts.join(' · ')}</span>
    </div>
  )
}
