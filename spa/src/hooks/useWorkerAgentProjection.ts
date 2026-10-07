// spa/src/hooks/useWorkerAgentProjection.ts — app-lifetime projection of every
// worker (execution) pane's state into `useAgentStore` (worker-pane theme spec
// §8.1–8.2), under `exec-<id>` keys, so the sidebar light, unread and the
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
import { isAwaitingApproval } from '../lib/nex/worker-summary'
import { hasOpenTurn, lastEndedOutcome, type ExecutionState } from '../lib/nex/event-reducer'
import type { ExecutionSummary } from '../lib/nex/types'
import type { AssistantMessage, StreamMessage } from '../lib/nex/message-types'
import type { Tab } from '../types/tab'

/**
 * Names the notification pipeline already understands (notification-content.ts, event-name.ts). `waiting` is a
 * worker awaiting approval: it is named for what it is, and `detailOf` marks it `notification_silent` so the
 * dispatcher never pushes it (permission channel PC2: no push notifications).
 */
const RAW_EVENT_NAME: Record<WorkerProjection['status'], string> = {
  running: 'UserPromptSubmit',
  waiting: 'PermissionRequest',
  idle: 'Stop',
  error: 'StopFailure',
  clear: 'SessionEnd',
}

const LAST_MESSAGE_MAX = 300

interface WorkerRef { hostId: string; executionId: string }

/**
 * Every execution pane in any tab, keyed by its agent-store composite key.
 * Controller ruling (worker-pane theme spec §L1): a worker's status is
 * decided by the daemon regardless of which pane shows it — identical to a
 * tmux agent, whose status the daemon reports no matter which pane runs it.
 * Only the *tab lookups* (`getActiveSessionInfo`, `findTabBySessionCode`,
 * `useTabDisplay`) are primary-pane, because a tab has one place to show a
 * badge or route a click. This projection is not a tab lookup — it feeds the
 * store every execution pane reads from — so it must scan every pane, not
 * just `getPrimaryPane`. A worker in a secondary split pane still gets a
 * status/unread entry here; it just has no tab to badge (`hasTab` is false in
 * the dispatcher), so it notifies only when `notifyWithoutTab` is on. Do not
 * narrow this back to the primary pane — that was tried and reverted
 * (`d108afd7`, then undone) because it silently dropped secondary-pane
 * workers' status and unread instead of just their notification routing.
 */
function collectWorkers(tabs: Record<string, Tab>): Map<string, WorkerRef> {
  const out = new Map<string, WorkerRef>()
  for (const tab of Object.values(tabs)) {
    scanPaneTree(tab.layout, (pane) => {
      const c = pane.content
      if (c.kind !== 'execution') return
      // Same resolution as ExecutionPaneWrapper (register-modules/index.tsx):
      // `resolveExecutionHostId` treats an empty-string host the same as a
      // missing one (falls back to the first host), so `??` here — which
      // only catches null/undefined — would leave a '' hint unresolved.
      const hostId = resolveExecutionHostId(c.host)
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
        // The summary decides (refetched on every permission event, Task 8), same as for a list row below.
        awaitingApproval: isAwaitingApproval(live.summary),
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
      awaitingApproval: isAwaitingApproval(row),
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
 * summary's `updated_at`; the clock only as a last resort. Waiting (awaiting
 * approval) → the pending request's `since` (Unix ms, the daemon's clock).
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
  if (status === 'waiting') {
    const since = src.summary.pending_permission?.since
    if (typeof since === 'number' && since > 0) return since
  }
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
  // PC2 forbids push notifications for 「等待核准」. The guard lives here, not in the notification module: the
  // dispatcher's `shouldNotify` drops a `notification_silent` event (the same flag opencode's idle uses). The tab
  // still marks unread like any `waiting` agent — that is not a push.
  if (status === 'waiting') return { notification_silent: true }
  return {}
}

const signatureOf = (agentType: string, p: WorkerProjection): string =>
  `${agentType}|${p.status}|${p.subagents.map((s) => s.id).join(',')}`

interface Dispatched {
  hostId: string
  code: string
  status: WorkerProjection['status']
  sig: string
  /** The row source's `turn_count` when the last dispatch came from a list row; null for a live source. */
  rowTurns: number | null
}

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
      // A list row knows only the current state and its refreshes are debounced, so a running -> idle between two
      // refreshes leaves the status signature unchanged and the second Stop would be swallowed. `turn_count` still
      // moves with every turn, so between two ROW-sourced dispatches a changed count is a new event. It is
      // deliberately not part of `sig`: a live <-> row source switch (pane eviction) must not look like a new Stop.
      const rowTurns = src.live ? null : (src.summary.turn_count ?? 0)
      const missedTurn = prev !== undefined && prev.rowTurns !== null && rowTurns !== null && rowTurns !== prev.rowTurns
      if (prev?.sig === sig && !missedTurn) continue
      const code = execAgentCode(w.executionId)
      // Terminated (not merely archived): an explicit event for the dispatcher before the clear, which would
      // otherwise take the key out silently. Only for a worker this engine saw alive — a first sight of an already
      // terminated one (reload) is history, not news. The dispatcher still suppresses it for the active, focused tab.
      if (projection.status === 'clear' && src.summary.state === 'terminated' && prev !== undefined && prev.status !== 'clear') {
        dispatch(w.hostId, code, {
          agent_type: agentType,
          status: 'idle',
          subagents: [],
          raw_event_name: 'WorkerTerminated',
          broadcast_ts: Math.max(Date.now(), src.summary.updated_at),
          detail: {},
        })
      }
      dispatched.set(key, { hostId: w.hostId, code, status: projection.status, sig, rowTurns })
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
