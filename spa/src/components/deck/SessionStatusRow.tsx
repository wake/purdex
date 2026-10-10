// spa/src/components/deck/SessionStatusRow.tsx — the status row under a session view's input (U3-5), wired to what the App
// already has: the session's Claude Code statusLine snapshot (`useAgentStore.ccStatus`, the same one the Mac status bar reads,
// through the same `parseCcUsage`) for model, effort, context, 5h, 7d and cost, and the conversation (header status, newest
// item) for the state cell. No new data path; a missing value is 「—」.
import { useEffect, useMemo, useState } from 'react'
import { useAgentStore } from '../../stores/useAgentStore'
import { compositeKey } from '../../lib/composite-key'
import { parseCcExtras, parseCcUsage } from '../../lib/usage-display'
import type { DeckFooterContext } from './footer-context'
import { slotModel } from './slot-state'
import { StatusRow } from './StatusRow'

/** Re-renders the caller every `ms`. */
function useNow(ms: number): number {
  const [now, setNow] = useState(() => Date.now())
  useEffect(() => {
    const id = setInterval(() => setNow(Date.now()), ms)
    return () => clearInterval(id)
  }, [ms])
  return now
}

interface Props {
  /** The pane's tmux session code: the statusLine snapshot is keyed by host + code, not by Claude Code's session id. */
  sessionCode: string
  ctx: Pick<DeckFooterContext, 'hostId' | 'items' | 'status' | 'usage'>
}

export function SessionStatusRow({ sessionCode, ctx }: Props) {
  const entry = useAgentStore((s) => s.ccStatus[compositeKey(ctx.hostId, sessionCode)])
  const slot = useMemo(() => slotModel(ctx.status, ctx.items), [ctx.status, ctx.items])
  // The clock of a running step ticks every second; otherwise a slow tick keeps "stale" and reset times honest.
  const now = useNow(slot.state === 'running' ? 1000 : 30_000)
  const usage = parseCcUsage(entry?.raw)
  const extras = parseCcExtras(entry?.raw)
  return (
    <StatusRow
      state={slot.state}
      exitCode={slot.exitCode}
      elapsedMs={slot.runningSince !== undefined ? Math.max(0, now - slot.runningSince) : 0}
      model={extras.model ?? ctx.usage?.model ?? null}
      effort={extras.effort ?? ctx.usage?.effort ?? null}
      contextUsed={usage?.context ?? null}
      contextWindowTokens={extras.windowTokens}
      fiveHour={usage?.fiveHour ?? null}
      sevenDay={usage?.sevenDay ?? null}
      cost={extras.cost}
      now={now}
      usageAt={entry?.receivedAt}
    />
  )
}
