// spa/src/hooks/useWorkerAgentProjection.ts — app-lifetime projection of every
// worker (execution) pane's state into `useAgentStore` (worker-pane theme spec
// §8.1–8.2), under `exec-<id>` keys, so the sidebar light, unread and the
// notification dispatcher treat a worker tab like a terminal agent tab.
//
// One source of truth for STATUS: the host's execution list row
// (`useExecutionListStore`) — like a tmux agent, whose status the daemon pushes
// whatever its panes do. Every host with a worker tab holds one list
// subscription, and its one site-wide stream refetches the list on every frame,
// so a worker whose pane is not mounted (its tab switched away, or evicted)
// still moves and notifies. Everything status-class — state, archived, the
// pending request (等待核准), turn_count, the dedupe stamps, and so every
// transition dispatched here — is read from the row. The pane's live entry
// (`useExecutionStore`) serves the conversation and the request card inside the
// pane; here it is only decoration (the running-subagent refs, a Stop /
// StopFailure notification's detail) while its stream is delivering, and never
// decides a status. The one fallback to it: a list cut at its page cap that has
// no row for the worker. A status dispatch happens only when the status
// signature (status, agent type, the pending request while waiting) changes or a
// counted turn was missed; a decoration-only change writes the refs alone.
// Closing the last pane of a worker, or its row leaving a list that answered in
// full (archived), dispatches `clear`.
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
 * worker awaiting approval. Permission channel PC2 as amended 2026-10-07 (user): that is an event of the same level as
 * a terminal agent's ask, so it carries the ask's name — a terminal Claude Code agent's `PdxPermissionRequest`
 * normalizes to `PermissionRequest` — and the dispatcher applies the same rules and the same setting
 * (`agents[agentType].events.PermissionRequest`, `agentType` from `providerAgentType`: `cc` for a Claude worker).
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
 * badge. This projection is not a tab lookup — it feeds the store every
 * execution pane reads from — so it must scan every pane, not just
 * `getPrimaryPane`. A worker in a secondary split pane still gets a
 * status/unread entry here; the notification dispatcher walks every pane too
 * (#1840: `findTabAndPaneBySessionCode` / `isAgentVisibleInActiveTab`), so it
 * notifies, is quiet while its tab is on screen and routes its click like a
 * primary-pane worker. Do not narrow this back to the primary pane — that
 * was tried and reverted (`d108afd7`, then undone) because it silently dropped secondary-pane
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
  /** The list row — or, under the truncated fallback only, the live summary. Status, stamps and request come from it. */
  summary: ExecutionSummary
  /** The live entry when IT is the status source (the truncated fallback); null for a list row. */
  live: ExecutionState | null
  /**
   * Decoration only, never status: the live entry while its stream is delivering (`sse === 'open'`) — or the status
   * source itself under the fallback. A stream in any other state (the pane unmounted: `idle`; evicted: `paused`;
   * dialing: `connecting`; retrying: `reconnecting`; dead: `closed`) left the entry frozen, its tasks and messages
   * possibly a turn behind the row, so it decorates nothing. Null = no decoration.
   */
  decor: ExecutionState | null
}

/** No row for the worker in a host list that answered in full: the worker is not live there any more. */
const GONE = 'gone'

function runningSubagentsOf(live: ExecutionState | null): WorkerStatusInput['runningSubagents'] {
  if (!live) return []
  return runningTasks(live.tasks)
    .filter((t) => t.kind === 'subagent')
    .map((t) => ({ task_id: t.task_id, subagent_type: t.subagent_type, started_at: t.started_at }))
}

function deriveSource({ hostId, executionId }: WorkerRef): Source | typeof GONE | null {
  const live = useExecutionStore.getState().executions[executionKey(hostId, executionId)]
  const list = useExecutionListStore.getState().byHost[hostId]
  const row = list?.items.find((r) => r.id === executionId)
  if (row) {
    const decor = live?.sse === 'open' ? live : null
    return {
      input: {
        // A list row knows only the current state; `turn_count` (Dispatched.rowTurns) covers a turn missed between
        // two of its debounced refreshes.
        state: row.state,
        turnLive: row.state === 'running',
        lastOutcome: row.state === 'failed' ? 'failed' : null,
        hasTurn: (row.turn_count ?? 0) > 0,
        archived: row.archived,
        // A list row carries only a count (running_tasks), not the refs: those are the live stream's, when it delivers.
        runningSubagents: runningSubagentsOf(decor),
        awaitingApproval: isAwaitingApproval(row),
      },
      provider: row.provider,
      summary: row,
      live: null,
      decor,
    }
  }
  // The ONLY fallback to the live entry: the host's list hit its page cap (D9, `truncated`) and has no row for this
  // worker, so the list cannot say anything about it. The live entry then decides as it did before the list became
  // the one source — per turn, not the execution-wide `turnLive` alone (the first turn's end clears `turnLive` while a
  // queued send is still pending).
  if (list?.truncated && live?.summary) {
    return {
      input: {
        state: live.summary.state,
        turnLive: live.turnLive || hasOpenTurn(live),
        lastOutcome: lastEndedOutcome(live),
        hasTurn: live.turnStarts.length > 0,
        archived: live.summary.archived,
        runningSubagents: runningSubagentsOf(live),
        awaitingApproval: isAwaitingApproval(live.summary),
      },
      provider: live.summary.provider,
      summary: live.summary,
      live,
      decor: live,
    }
  }
  // The list asks for unarchived executions only (execution-list-effects.ts): a row missing from a list that answered
  // in full is a worker archived (or gone) since. Not before the list answered, nor from a list cut at its page cap.
  if (list?.phase === 'ready' && !list.truncated) return GONE
  return null
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

/** The failing turn's reason: its failing result's subtype (decoration, from a delivering live stream), else the lifecycle / terminal reason. */
function failureReason(src: Source): string {
  if (src.decor) {
    const msgs = lastTurnMessages(src.decor)
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
 * event. Read from the row only: running / idle / error → the row's
 * `updated_at` (Nexen advances it on every state change); waiting (awaiting
 * approval) → the pending request's `since` (Unix ms, the daemon's clock) —
 * Nexen creating a request does not touch `updated_at` (`pending_permission`
 * is a query-time subquery). The clock only as a last resort. Under the
 * truncated fallback (the live entry is the source) running / idle / error
 * keep the live turn's start / end, as before the row was the one source.
 *
 * The request's `since` is used even for the first projection after a
 * reload. It is not what dedupes the request,
 * though: two requests can share a millisecond, so the dispatcher dedupes a
 * waiting event by its `detail.request_id` (`shouldDispatchRequest`) — quiet
 * for a request it already saw, once for one it has not (also one that came
 * while the App was closed), and only a baseline for a key it never saw — and
 * keeps the newest stamp for the key's other events.
 *
 * `firstInSession`: this engine has not dispatched the key yet. A list-row
 * source (any status but waiting, above) then stamps 0 instead of
 * `updated_at`: Nexen advances `updated_at` on lease acquire / renew / release
 * and on last_turn_reason writes with no status change — so after a reload the
 * first list-row projection would carry a stamp above the one the dispatcher
 * stored and replay an old Stop. 0 is a baseline the dispatcher records (for
 * an unseen key) but never shows, and it can never exceed a stored stamp.
 * Later list-row dispatches in the session keep `updated_at`: change detection
 * only lets them through on a real status / signature change (or a missed
 * turn), and that `updated_at` is newer than anything stored. A
 * decoration-only change is never a dispatch, so it never carries a stamp.
 */
function stateStamp(status: WorkerProjection['status'], src: Source, firstInSession: boolean): number {
  if (status === 'waiting') {
    const since = src.summary.pending_permission?.since
    if (typeof since === 'number' && since > 0) return since
  }
  if (!src.live && firstInSession) return 0
  const meta = src.live?.turnMeta.at(-1)
  const t = status === 'running' ? meta?.startAt : meta?.endAt
  if (typeof t === 'number' && t > 0) return t
  return src.summary.updated_at > 0 ? src.summary.updated_at : Date.now()
}

function detailOf(status: WorkerProjection['status'], src: Source): Record<string, unknown> {
  // Decoration: the turn's last reply, only from a live stream that is delivering (a frozen entry may hold an older
  // turn's text). None is just a Stop without the snippet.
  if (status === 'idle') {
    const text = src.decor ? lastAssistantText(src.decor) : undefined
    return text !== undefined ? { last_assistant_message: text } : {}
  }
  if (status === 'error') return { error: failureReason(src) }
  // 「等待核准」 notifies like a terminal agent's ask (PC2 as amended 2026-10-07), so it carries the ask's detail shape:
  // `tool_name`, which the PermissionRequest notification body names (notification-content.ts). And `request_id`: the
  // dispatcher dedupes such an event by the request it is about, not by its stamp (`since`), which two requests can
  // share (useNotificationDispatcher.ts, `shouldDispatchRequest`).
  if (status === 'waiting') {
    const pending = src.summary.pending_permission
    const detail: Record<string, unknown> = {}
    if (typeof pending?.tool_name === 'string' && pending.tool_name !== '') detail.tool_name = pending.tool_name
    if (typeof pending?.request_id === 'string' && pending.request_id !== '') detail.request_id = pending.request_id
    return detail
  }
  return {}
}

/** The pending request a waiting projection is about; '' for any other status. */
function requestIdOf(status: WorkerProjection['status'], src: Source): string {
  return status === 'waiting' ? (src.summary.pending_permission?.request_id ?? '') : ''
}

/**
 * What makes a projection a new dispatch: status-class data only. While waiting it includes the pending request's id:
 * a new request is a new event even when the worker never left `waiting` (back-to-back requests), and the same request
 * seen again through a refetch is not. The running-subagent refs are decoration and deliberately NOT part of it: a
 * change of refs alone is written with `setSubagents` and dispatches nothing, so it can never carry a new stamp (a
 * replayed Stop) or re-mark the tab unread — e.g. the refs dropping when the pane unmounts.
 */
const signatureOf = (agentType: string, status: WorkerProjection['status'], requestId: string): string =>
  `${agentType}|${status}|${requestId}`

/** The decoration a tab shows next to the light: the running-subagent ids. */
const subagentsSigOf = (p: WorkerProjection): string => p.subagents.map((s) => s.id).join(',')

interface Dispatched {
  hostId: string
  code: string
  status: WorkerProjection['status']
  sig: string
  /** The row's `turn_count` as last seen; null while the live entry is the source (the truncated fallback). */
  rowTurns: number | null
  /** The subagent refs last written for the key (`subagentsSigOf`). */
  subs: string
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
      const prev = dispatched.get(key)
      if (src === GONE) {
        // Its row left a list that answered in full: archived (or gone) since, so not live — like an archived summary
        // (`isLiveRow`). Only for a worker this engine saw: one never listed yet (a new worker the list has not caught
        // up with) keeps no light until its row appears.
        if (prev && prev.status !== 'clear') {
          dispatched.set(key, { ...prev, status: 'clear', sig: GONE, rowTurns: null, subs: '' })
          clearKey(prev)
        }
        continue
      }
      if (!src) continue
      const projection = projectWorkerStatus(src.input)
      const agentType = src.provider ? providerAgentType(src.provider) : ''
      const sig = signatureOf(agentType, projection.status, requestIdOf(projection.status, src))
      const subs = subagentsSigOf(projection)
      // A list row knows only the current state and its refreshes are debounced, so a running -> idle between two
      // refreshes leaves the status signature unchanged and the second Stop would be swallowed. `turn_count` still
      // moves with every turn, so between two ROW-sourced projections a changed count is a new event. It is
      // deliberately not part of `sig`: a switch between the row and the truncated fallback must not look like a Stop.
      const rowTurns = src.live ? null : (src.summary.turn_count ?? 0)
      const missedTurn = prev !== undefined && prev.rowTurns !== null && rowTurns !== null && rowTurns !== prev.rowTurns
      const code = execAgentCode(w.executionId)
      if (prev?.sig === sig && !missedTurn) {
        // Same status: at most the decoration moved (refs from the live stream) — written alone, never dispatched.
        if (prev.subs !== subs && projection.status !== 'clear') useAgentStore.getState().setSubagents(w.hostId, code, projection.subagents)
        if (prev.subs !== subs || prev.rowTurns !== rowTurns) dispatched.set(key, { ...prev, subs, rowTurns })
        continue
      }
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
      dispatched.set(key, { hostId: w.hostId, code, status: projection.status, sig, rowTurns, subs })
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
