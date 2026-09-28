// spa/src/hooks/useWorkerAgentProjection.ts — app-lifetime projection of every
// worker (execution) pane's state into `useAgentStore` (worker-pane theme spec
// §8.1–8.2), under `exec:<id>` keys, so the sidebar light, unread and the
// notification dispatcher treat a worker tab like a terminal agent tab.
//
// Source per pane: the live `useExecutionStore` entry (when it has a summary
// and its SSE is open, or the list row is not strictly newer than it), else
// the host's execution list row (a frozen live entry is still used when no
// row exists). Every host with a worker tab
// holds one list subscription so a row exists for an evicted pane. A dispatch
// happens only when the projection (status, subagent ids, agent type) changes;
// closing the last pane of a worker dispatches `clear`.
import { useEffect } from 'react'
import { useAgentStore, type NormalizedEvent } from '../stores/useAgentStore'
import { useTabStore } from '../stores/useTabStore'
import { useExecutionStore, executionKey } from '../stores/useExecutionStore'
import { useExecutionListStore } from '../stores/useExecutionListStore'
import { useNexHostStore } from '../stores/useNexHostStore'
import { useHostStore } from '../stores/useHostStore'
import { compositeKey } from '../lib/composite-key'
import { scanPaneTree } from '../lib/pane-tree'
import { resolveExecutionHostId } from '../lib/nex/resolve-host'
import { runningTasks } from '../lib/nex/tasks'
import { isResultError } from '../lib/nex/cost-summary'
import { execAgentCode, projectWorkerStatus, providerAgentType, type WorkerProjection, type WorkerStatusInput } from '../lib/nex/worker-agent-status'
import { hasOpenTurn, lastEndedOutcome, type ExecutionState } from '../lib/nex/event-reducer'
import type { ExecutionSummary } from '../lib/nex/types'
import type { AssistantMessage, StreamMessage } from '../lib/nex/message-types'
import type { Tab } from '../types/tab'

/** Names the notification pipeline already understands (notification-content.ts, event-name.ts). */
const RAW_EVENT_NAME: Record<WorkerProjection['status'], string> = {
  running: 'UserPromptSubmit',
  idle: 'Stop',
  error: 'StopFailure',
  clear: 'SessionEnd',
}

const LAST_MESSAGE_MAX = 300

interface WorkerRef { hostId: string; executionId: string }

/** Every execution pane in any tab, keyed by its agent-store composite key. */
function collectWorkers(tabs: Record<string, Tab>): Map<string, WorkerRef> {
  const out = new Map<string, WorkerRef>()
  for (const tab of Object.values(tabs)) {
    scanPaneTree(tab.layout, (pane) => {
      const c = pane.content
      if (c.kind !== 'execution') return
      // Same resolution as ExecutionPaneWrapper (register-modules/index.tsx).
      const hostId = c.host ?? resolveExecutionHostId(undefined)
      if (!hostId) return
      out.set(compositeKey(hostId, execAgentCode(c.executionId)), { hostId, executionId: c.executionId })
    })
  }
  return out
}

interface Source {
  input: WorkerStatusInput
  provider: string
  /** Live state when it is the source; null for a list-row source. */
  live: ExecutionState | null
  summary: ExecutionSummary
}

/**
 * How fresh the live snapshot is, on the daemon's clock: the newer of the
 * summary's `updated_at` (refreshed by every summary refetch — event patches
 * never touch it) and the `created_at` of the last applied durable event
 * (`lastEventAt`; the summary lags it between an event and its refetch).
 * Both are the same clock a list row's `updated_at` is on.
 */
function liveFreshness(live: ExecutionState, summary: ExecutionSummary): number {
  return Math.max(summary.updated_at, live.lastEventAt)
}

/**
 * Chosen by freshness, not by SSE state alone. An `open` stream is delivering,
 * so the live entry wins. Every other status leaves the entry frozen — an
 * evicted pane (`paused`, useExecutionSubscription's slot cap), a dead stream
 * (`closed`), a resumed one still dialing in (`connecting`), a stream retrying
 * (`reconnecting`) — and then the list row wins only when it is strictly newer
 * than the live snapshot. That serves both failure modes: a brief network
 * blip leaves the row older, so the live state holds (no false Stop then
 * UserPromptSubmit); a stream stuck retrying or evicted falls behind a row
 * that keeps advancing, so the row takes over. With no row the frozen live
 * entry is still the best we have.
 */
function liveWins(live: ExecutionState, summary: ExecutionSummary, row: ExecutionSummary | undefined): boolean {
  if (live.sse === 'open' || !row) return true
  return !(row.updated_at > liveFreshness(live, summary))
}

function deriveSource({ hostId, executionId }: WorkerRef): Source | null {
  const live = useExecutionStore.getState().executions[executionKey(hostId, executionId)]
  const row = useExecutionListStore.getState().byHost[hostId]?.items.find((r) => r.id === executionId)
  if (live?.summary && liveWins(live, live.summary, row)) {
    const subs = runningTasks(live.tasks).filter((t) => t.kind === 'subagent')
    return {
      input: {
        state: live.summary.state,
        // Per turn, not the execution-wide `turnLive` alone: the first
        // turn's end clears `turnLive` while a queued send is still pending.
        turnLive: live.turnLive || hasOpenTurn(live),
        lastOutcome: lastEndedOutcome(live),
        hasTurn: live.turnStarts.length > 0,
        archived: live.summary.archived,
        runningSubagents: subs.map((t) => ({ task_id: t.task_id, subagent_type: t.subagent_type, started_at: t.started_at })),
      },
      provider: live.summary.provider,
      live,
      summary: live.summary,
    }
  }
  if (!row) return null
  return {
    input: {
      state: row.state,
      turnLive: row.state === 'running',
      lastOutcome: row.state === 'failed' ? 'failed' : null,
      hasTurn: (row.turn_count ?? 0) > 0,
      archived: row.archived,
      // A list row carries only a count (running_tasks), not the refs.
      runningSubagents: [],
    },
    provider: row.provider,
    live: null,
    summary: row,
  }
}

/** Messages of the last turn (from its boundary; all messages when no boundary is known). */
function lastTurnMessages(live: ExecutionState): StreamMessage[] {
  const start = live.turnStarts.at(-1) ?? 0
  return live.messages.slice(Math.min(Math.max(start, 0), live.messages.length))
}

/** The last top-level assistant text of the turn, first 300 chars. */
function lastAssistantText(live: ExecutionState): string | undefined {
  const msgs = lastTurnMessages(live)
  for (let i = msgs.length - 1; i >= 0; i--) {
    const m = msgs[i]
    if (m.type !== 'assistant') continue
    const a = m as AssistantMessage
    if (a.parent_tool_use_id != null) continue
    const text = (a.message?.content ?? [])
      .filter((b) => b.type === 'text' && typeof b.text === 'string')
      .map((b) => b.text as string)
      .join('\n')
      .trim()
    if (text) return text.slice(0, LAST_MESSAGE_MAX)
  }
  return undefined
}

/** The failing turn's reason: its failing result's subtype, else the lifecycle / terminal reason. */
function failureReason(src: Source): string {
  if (src.live) {
    const msgs = lastTurnMessages(src.live)
    for (let i = msgs.length - 1; i >= 0; i--) {
      const m = msgs[i] as Record<string, unknown>
      if (m.type !== 'result' || m.parent_tool_use_id != null || !isResultError(m)) continue
      if (typeof m.subtype === 'string' && m.subtype !== '' && m.subtype !== 'success') return m.subtype
      break
    }
  }
  const s = src.summary
  return s.last_turn_reason || s.terminal_reason || s.reject_reason || s.state
}

/**
 * A timestamp tied to the state, not the clock: the dispatcher's persistent
 * dedup (`shouldDispatch`) compares it with the last one it saw for the key,
 * so re-projecting the same state after a reload must not look like a new
 * event. Running → the turn's start; idle / error → the turn's end; else the
 * summary's `updated_at`; the clock only as a last resort.
 *
 * `firstInSession`: this engine has not dispatched the key yet. A list-row
 * source then stamps 0 instead of `updated_at`. The live and list-row sources
 * stamp the same state differently (turn endAt vs `updated_at`), and Nexen
 * advances `updated_at` on lease acquire / renew / release and on
 * last_turn_reason writes with no status change — so after a reload the first
 * list-row projection would carry a stamp above the one the dispatcher stored
 * and replay an old Stop. 0 is a baseline the dispatcher records (for an
 * unseen key) but never shows, and it can never exceed a stored stamp. Later
 * list-row dispatches in the session keep `updated_at`: change detection only
 * lets them through on a real status / signature change, and that
 * `updated_at` is newer than anything stored. The live source always keeps
 * its turn-derived stamps.
 */
function stateStamp(status: WorkerProjection['status'], src: Source, firstInSession: boolean): number {
  if (!src.live && firstInSession) return 0
  const meta = src.live?.turnMeta.at(-1)
  const t = status === 'running' ? meta?.startAt : meta?.endAt
  if (typeof t === 'number' && t > 0) return t
  return src.summary.updated_at > 0 ? src.summary.updated_at : Date.now()
}

function detailOf(status: WorkerProjection['status'], src: Source): Record<string, unknown> {
  if (status === 'idle' && src.live) {
    const text = lastAssistantText(src.live)
    return text !== undefined ? { last_assistant_message: text } : {}
  }
  if (status === 'error') return { error: failureReason(src) }
  return {}
}

const signatureOf = (agentType: string, p: WorkerProjection): string =>
  `${agentType}|${p.status}|${p.subagents.map((s) => s.id).join(',')}`

interface Dispatched { hostId: string; code: string; status: WorkerProjection['status']; sig: string }

/** Start the projection; returns its teardown. Exported for tests — the app mounts it through the hook. */
export function startWorkerAgentProjection(): () => void {
  const dispatched = new Map<string, Dispatched>()
  const listSubs = new Map<string, () => void>()
  let running = false
  let dirty = false
  let stopped = false

  const dispatch = (hostId: string, code: string, event: NormalizedEvent) =>
    useAgentStore.getState().handleNormalizedEvent(hostId, code, event)

  const syncOnce = () => {
    const workers = collectWorkers(useTabStore.getState().tabs)

    const hosts = new Set<string>()
    for (const w of workers.values()) hosts.add(w.hostId)
    for (const hostId of hosts) {
      if (listSubs.has(hostId)) continue
      void useNexHostStore.getState().ensure(hostId)
      listSubs.set(hostId, useExecutionListStore.getState().subscribe(hostId))
    }
    for (const [hostId, release] of listSubs) {
      if (hosts.has(hostId)) continue
      listSubs.delete(hostId)
      release()
    }

    for (const [key, w] of workers) {
      const src = deriveSource(w)
      if (!src) continue
      const projection = projectWorkerStatus(src.input)
      const agentType = src.provider ? providerAgentType(src.provider) : ''
      const sig = signatureOf(agentType, projection)
      const prev = dispatched.get(key)
      if (prev?.sig === sig) continue
      const code = execAgentCode(w.executionId)
      dispatched.set(key, { hostId: w.hostId, code, status: projection.status, sig })
      dispatch(w.hostId, code, {
        agent_type: agentType,
        status: projection.status,
        subagents: projection.subagents,
        raw_event_name: RAW_EVENT_NAME[projection.status],
        broadcast_ts: stateStamp(projection.status, src, prev === undefined),
        detail: detailOf(projection.status, src),
      })
    }

    for (const [key, d] of dispatched) {
      if (workers.has(key)) continue
      dispatched.delete(key)
      if (d.status !== 'clear') clearKey(d)
    }
  }

  // `Date.now()` here is intentionally outside the stamp logic: a `clear`
  // never reaches `lastEvents` (the agent store drops the key), so the
  // dispatcher's dedup never compares this stamp.
  const clearKey = (d: Dispatched) =>
    dispatch(d.hostId, d.code, { agent_type: '', status: 'clear', raw_event_name: RAW_EVENT_NAME.clear, broadcast_ts: Date.now(), detail: {} })

  // A dispatch or a subscribe may synchronously touch a watched store; the
  // nested notification only marks the pass dirty and the outer loop reruns.
  const sync = () => {
    if (stopped) return
    if (running) { dirty = true; return }
    running = true
    try {
      do { dirty = false; syncOnce() } while (dirty && !stopped)
    } finally {
      running = false
    }
  }

  const unsubs = [
    useTabStore.subscribe((s, p) => { if (s.tabs !== p.tabs) sync() }),
    useExecutionStore.subscribe((s, p) => { if (s.executions !== p.executions) sync() }),
    useExecutionListStore.subscribe((s, p) => { if (s.byHost !== p.byHost) sync() }),
    useHostStore.subscribe((s, p) => { if (s.hostOrder !== p.hostOrder) sync() }),
  ]
  sync()

  return () => {
    stopped = true
    for (const u of unsubs) u()
    for (const release of listSubs.values()) release()
    listSubs.clear()
    for (const d of dispatched.values()) if (d.status !== 'clear') clearKey(d)
    dispatched.clear()
  }
}

/** Mount once at app level, next to the other app-lifetime hooks. */
export function useWorkerAgentProjection(): void {
  useEffect(() => startWorkerAgentProjection(), [])
}
