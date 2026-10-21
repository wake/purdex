// spa/src/stores/useAgentStore.ts
import { create } from 'zustand'
import { isAgentVisibleInActiveTab } from '../lib/active-session'
import { compositeKey } from '../lib/composite-key'
import { daemonNsToMsOrNull, parseExit, parseProvenance } from '../lib/rebuild/provenance'
import { useTabStore } from './useTabStore'
import { scanPaneTree } from '../lib/pane-tree'
import { covers, cursorOf, type ReadCursor } from '../lib/read-sync/cursor'
import { reportRead } from '../lib/read-sync/reporter'

export type AgentStatus = 'running' | 'waiting' | 'idle' | 'error'

/**
 * OSC 0/2 payloads are technically free-form — agents / shells occasionally
 * embed ANSI CSI (colours) or C0 control chars that xterm passes through raw.
 * Strip them so the text is safe to render in React and in native `title=""`
 * tooltips.
 */
export function sanitizeOscTitle(raw: string): string {
  // eslint-disable-next-line no-control-regex
  return raw.replace(/\x1b\[[\d;]*[A-Za-z]/g, '').replace(/[\x00-\x1f\x7f]/g, '').trim()
}

/**
 * Return a new record containing every entry of `rec` except those whose key
 * matches `shouldOmit`. Shared by `removeHost` (prefix match) and
 * `clearHostAgentStatus` (set membership).
 */
function omitKeys<T>(
  rec: Record<string, T>,
  shouldOmit: (key: string) => boolean,
): Record<string, T> {
  const out: Record<string, T> = {}
  for (const [k, v] of Object.entries(rec)) {
    if (!shouldOmit(k)) out[k] = v
  }
  return out
}

/**
 * Subagent reference — mirrors Go `internal/agent.SubagentRef`. Covers native
 * SubagentStart refs (source_pid=0 / source_start_time="" / is_proxy omitted)
 * and cross-agent proxy refs (source_pid/source_start_time populated +
 * is_proxy:true). Phase 2 PR-2a wires the type end-to-end; PR-2b lights up
 * proxy detection and the dot visual (type color + outline).
 */
export interface SubagentRef {
  id: string
  type: string
  started_at: number
  source_pid: number
  source_start_time: string
  is_proxy?: boolean
  delegating?: boolean
  delegating_tool_use_ids?: string[]
}

/** Normalized event from backend (replaces AgentHookEvent). */
export interface NormalizedEvent {
  agent_type: string
  status: string             // running | waiting | idle | error | clear
  model?: string
  subagents?: SubagentRef[] | null
  raw_event_name: string
  broadcast_ts: number
  detail?: Record<string, unknown>
  /** The session's background-work symbol (U1-3, spec N6); '' / absent = none. Unknown strings are ignored. */
  background?: '' | 'workflow' | 'monitor' | 'schedule'
  /** What decided `status` on a daemon `hook` frame: a live mod stream or the hook. */
  source?: 'mod' | 'hook'
  /** Daemon boot id (suffixed `-n` after a counter rotation) and the contiguous per-process `hook` counter. */
  epoch?: string
  seq?: number
  /** A subscribe-time replay frame / `agent.snapshot` entry: `seq` is the slot's high-water mark, not a new frame. */
  snapshot?: boolean
  /** A Stop / StopFailure of a proxy subagent (a codex run by the pane's main agent as a tool): ends a tool, not the
   *  main agent's turn, so it raises no desktop notification and no unread. Only present when true (daemon). */
  from_proxy?: boolean
}

/** Whether `e` is a proxy subagent's Stop / StopFailure — the only events `from_proxy` mutes. */
export function isProxyStop(e: Pick<NormalizedEvent, 'from_proxy' | 'raw_event_name'>): boolean {
  if (e.from_proxy !== true) return false
  const n = e.raw_event_name
  return n === 'PdxStop' || n === 'Stop' || n === 'PdxStopFailure' || n === 'StopFailure'
}

/** `queued` is not an event field: the tab derives it from the pool's jobs (a pane's agent session has a job queued or running). */
export type BackgroundKind = 'queued' | 'workflow' | 'monitor' | 'schedule'

/** The background symbol an event carries, or undefined (absent, '' or a kind this client does not know). */
export function backgroundOf(e?: NormalizedEvent): BackgroundKind | undefined {
  const b = e?.background
  return b === 'workflow' || b === 'monitor' || b === 'schedule' ? b : undefined
}

/**
 * Write the rebuild record from a qualifying `SessionStart`'s provenance
 * envelope (spec §4.2 step 3). The whole agent group is replaced as one unit,
 * so a payload without `cwd` clears `cwd` rather than leaving the previous
 * agent's directory beside a new session id.
 *
 * What lands is the agent IDENTITY, never a command: `resolveResumeCommand`
 * composes from the identity at the moment the command is needed, so a
 * template the user edits later reaches every pane already recorded.
 *
 * Stamped with the event's `broadcast_ts`, not this client's clock: every
 * attached client writes this same record into the same synced section, and
 * the bytes must match (sync-conflict-fixes spec D1).
 */
function writeProvenanceRecord(
  hostId: string,
  sessionCode: string,
  detail: Record<string, unknown> | undefined,
  broadcastTs: number,
): boolean {
  const prov = parseProvenance(detail)
  if (!prov) return false
  // A missing / out-of-window broadcast_ts falls back to this client's clock:
  // still a stamp, but not a daemon time, so the store must not order on it.
  const daemonMs = daemonNsToMsOrNull(broadcastTs)
  const now = daemonMs ?? Date.now()
  useTabStore.getState().setPaneRebuild(hostId, sessionCode, prov.tmuxInstance, {
    kind: 'agent-group',
    ordered: daemonMs !== null,
    record: {
      tmuxInstance: prov.tmuxInstance,
      cwd: prov.cwd || undefined,
      cwdSource: prov.cwd ? 'agent-session-start' : undefined,
      agent: {
        type: prov.agentType,
        sessionId: prov.sessionId || undefined,
        tmuxPaneId: prov.tmuxPaneId || undefined,
        frameId: prov.frameId || undefined,
        updatedAt: now,
      },
      capturedAt: now,
    },
  })
  return true
}

/**
 * Mark the recorded agent exited from a `pdx_exit` envelope (agent-last-state
 * spec §2). The store applies it only to a record whose `agent.frameId` and
 * `agent.sessionId` are the exit's — so this may be called for every event without a pane check here.
 */
function writeExitRecord(
  hostId: string,
  sessionCode: string,
  detail: Record<string, unknown> | undefined,
): void {
  const exit = parseExit(detail)
  if (!exit) return
  useTabStore.getState().setPaneRebuild(hostId, sessionCode, exit.tmuxInstance, {
    kind: 'agent-exit',
    frameId: exit.frameId,
    sessionId: exit.sessionId,
    exited: { at: exit.at, reason: exit.reason },
  })
}

/**
 * The reconnect projection replay (`internal/module/agent/module.go:564`)
 * delivers the session's *current* agent type but no provenance. When it
 * disagrees with the record, the SPA was away while the session changed
 * agents: the record still holds the old agent and nothing else marks it
 * wrong, so flag it unverified (spec §9.1).
 *
 * Only the replay does this. A hot-path event's outer `agent_type` is the
 * session-projection winner, which can legitimately name another tmux pane of
 * the same session — not evidence that the record is stale.
 */
function flagUnverifiedAgent(hostId: string, sessionCode: string, agentType: string): void {
  if (!agentType) return
  const instances = new Set<string>()
  for (const tab of Object.values(useTabStore.getState().tabs)) {
    scanPaneTree(tab.layout, (pane) => {
      const c = pane.content
      if (c.kind !== 'tmux-session') return
      if (c.hostId !== hostId || c.sessionCode !== sessionCode) return
      const recorded = c.rebuild?.agent?.type
      if (!recorded || recorded === agentType || c.rebuild?.unverified === true) return
      instances.add(c.tmuxInstance)
    })
  }
  for (const tmuxInstance of instances) {
    useTabStore.getState().setPaneRebuild(hostId, sessionCode, tmuxInstance, {
      kind: 'unverified', unverified: true,
    })
  }
}

/** Latest CC statusLine snapshot per session (ephemeral, from statusLine wrapper). */
export interface CcStatusEntry {
  receivedAt: number
  /** True only when receivedAt came from a valid daemon `at`; false = it is just the local arrival time (old daemon / no at). */
  atTrusted?: boolean
  raw: Record<string, unknown>
}

interface AgentState {
  // Backend-derived state
  statuses: Record<string, AgentStatus>
  agentTypes: Record<string, string>
  models: Record<string, string>
  subagents: Record<string, SubagentRef[]>
  lastEvents: Record<string, NormalizedEvent>  // for notification dispatcher
  oscTitles: Record<string, string>  // latest OSC 0/2 title per session (ephemeral)
  ccStatus: Record<string, CcStatusEntry>  // latest CC statusLine snapshot (ephemeral)

  // UI state
  unread: Record<string, boolean>
  /**
   * Per unread key, the hook cursor of the event that last set it (app-badge spec §7.2, §7.3). Absent: that event had
   * none (an event from before the feature, or a frame not from the emit slot), so any read of the code covers it.
   * Only keys in `unread` have one; every path that drops an unread drops its cursor with it.
   */
  unreadCursor: Record<string, ReadCursor>

  // Actions
  handleNormalizedEvent: (hostId: string, sessionCode: string, event: NormalizedEvent) => void
  /**
   * A daemon `hook` frame (live, legacy or replay). Same side effects as `handleNormalizedEvent`, but unread follows
   * the transition rule (U1-3): only a status that really changed from a known previous one is news. The worker
   * projection keeps `handleNormalizedEvent`'s rule, which deliberately counts same-status events.
   */
  applyHookEvent: (hostId: string, sessionCode: string, event: NormalizedEvent) => void
  /**
   * The host's complete agent list from an `agent.snapshot` frame: each entry is applied with the transition rule,
   * then every code of this host the snapshot does not list is cleared (execution panes `exec-…` and codes with no
   * agent state are left alone) — all in one `set`.
   */
  applyAgentSnapshot: (hostId: string, entries: { session: string; event: NormalizedEvent }[]) => void
  /**
   * Replace a key's running-subagent refs and nothing else — no status, no `lastEvents` entry, no unread. For a
   * worker, whose refs are decoration read from its pane's live stream while its status comes from the host list
   * (useWorkerAgentProjection): a change of refs alone must never look like an event to the notification dispatcher.
   */
  setSubagents: (hostId: string, sessionCode: string, subagents: SubagentRef[]) => void
  clearSession: (hostId: string, sessionCode: string) => void
  /**
   * Mark a key read. Returns what the unread -> read transition captured: whether it WAS unread and the cursor of the
   * event that set it, taken now and never read later (read sync, spec §7.2). The caller decides whether this is a user
   * read worth telling the daemon (`markReadByUser`); a read received from the daemon goes through `applyReads` instead.
   */
  markRead: (hostId: string, sessionCode: string) => ReadTransition
  /**
   * Reads received from the daemon (`session.reads` / `session.read`): clear each key's unread when the read covers it
   * (spec §7.2: same epoch and the unread's seq <= T.seq; an unread with no cursor is covered by any read). Never
   * reports anything: a received read is not forwarded again.
   */
  applyReads: (hostId: string, reads: { code: string; through: ReadCursor }[]) => void
  removeHost: (hostId: string) => void
  setOscTitle: (hostId: string, sessionCode: string, title: string) => void
  setCcStatus: (hostId: string, sessionCode: string, raw: Record<string, unknown>, receivedAt?: number) => void
  clearHostAgentStatus: (hostId: string) => void
}

/** What `markRead` captured at the unread -> read transition. */
export interface ReadTransition {
  wasUnread: boolean
  cursor: ReadCursor | undefined
}

type UnreadPolicy = 'worker' | 'transition'

/** An actionable event of an agent that is on screen: never marked unread, but seen (spec §7.1, "seen live"). */
interface SeenLive {
  sessionCode: string
  cursor: ReadCursor | undefined
}

/** `rec` without `key` (the same object when it has none). */
function dropKey<T>(rec: Record<string, T>, key: string): Record<string, T> {
  if (!(key in rec)) return rec
  const { [key]: _, ...rest } = rec
  return rest
}

/** `s` without anything keyed `key` (what a `clear` event and clearSession do). */
function withoutKey(s: AgentState, key: string): Partial<AgentState> {
  const drop = <T,>(rec: Record<string, T>): Record<string, T> => {
    if (!(key in rec)) return rec
    const { [key]: _, ...rest } = rec
    return rest
  }
  return {
    statuses: drop(s.statuses),
    agentTypes: drop(s.agentTypes),
    models: drop(s.models),
    subagents: drop(s.subagents),
    lastEvents: drop(s.lastEvents),
    oscTitles: drop(s.oscTitles),
    ccStatus: drop(s.ccStatus),
    unread: drop(s.unread),
    unreadCursor: drop(s.unreadCursor),
  }
}

/**
 * The side effects of an event that live outside this store (rebuild record, exit record, unverified flag). Done
 * once per event, before its state patch. The exit record is read BEFORE the clear return: a root's SessionEnd (or
 * the sweep clearing it) is exactly the event that empties the session.
 */
function eventSideEffects(hostId: string, sessionCode: string, event: NormalizedEvent): void {
  writeExitRecord(hostId, sessionCode, event.detail)
  if (event.status === 'clear') return
  // Rebuild record (spec §4.2). Reads ONLY `detail.pdx_provenance` — the outer `agent_type` is the
  // session-projection winner and must never reach the record (spec §4.3.1).
  const wroteProvenance = writeProvenanceRecord(hostId, sessionCode, event.detail, event.broadcast_ts)
  if (!wroteProvenance && event.raw_event_name === 'replay') {
    flagUnverifiedAgent(hostId, sessionCode, event.agent_type)
  }
}

/**
 * The state patch of one event on `s`. `policy` picks how unread is decided:
 * - `worker`: the original rule (every actionable event, a replay only when the status changed);
 * - `transition`: only a status that changed from a KNOWN previous one (U1-3 ruling 1).
 */
function eventPatch(
  s: AgentState, hostId: string, sessionCode: string, event: NormalizedEvent, policy: UnreadPolicy, seen: SeenLive[],
): Partial<AgentState> {
  const key = compositeKey(hostId, sessionCode)
  if (event.status === 'clear') return withoutKey(s, key)

  const patch: Partial<AgentState> = { lastEvents: { ...s.lastEvents, [key]: event } }
  if (event.agent_type) patch.agentTypes = { ...s.agentTypes, [key]: event.agent_type }
  // model persists across events
  if (event.model) patch.models = { ...s.models, [key]: event.model }
  // subagents: only an event that carries the field changes them
  if ('subagents' in event) {
    const subagents = event.subagents ?? []
    if (subagents.length > 0) patch.subagents = { ...s.subagents, [key]: subagents }
    else if (key in s.subagents) {
      const { [key]: _, ...rest } = s.subagents
      patch.subagents = rest
    }
  }

  // status: skip events with no status (e.g. SubagentStart/Stop)
  const status = event.status as AgentStatus | ''
  if (!status) return patch
  const prevStatus = s.statuses[key]
  patch.statuses = { ...s.statuses, [key]: status }

  // running is unambiguous user activity — clear any leftover unread regardless of focus.
  if (status === 'running') {
    if (key in s.unread) {
      const { [key]: _, ...rest } = s.unread
      patch.unread = rest
    }
    if (key in s.unreadCursor) patch.unreadCursor = dropKey(s.unreadCursor, key)
    return patch
  }

  if (policy === 'worker') {
    // Stopgap: a reconnect replay re-sends every session's current status, and SPA statuses outlive a daemon
    // restart — an unchanged (or first-seen) status is not news.
    if (event.raw_event_name === 'replay' && (prevStatus === undefined || prevStatus === status)) return patch
  } else if (prevStatus === undefined || prevStatus === status) {
    return patch
  }

  // Mark unread unless some pane of the active tab shows the agent (#1853 — the notification dispatcher's rule).
  // Notification raises status=idle but shouldn't surface as actionable: cc emits PdxNotification post-W2,
  // codex/opencode pre-migration still emit "Notification". Recognise both literals during the transition.
  const rawName = event.raw_event_name
  const isNotification = rawName === 'Notification' || rawName === 'PdxNotification'
  const notificationSilent = event.detail?.notification_silent === true
  const isActionable = status === 'waiting' || status === 'error' ||
    (status === 'idle' && !isNotification && !notificationSilent)
  if (isActionable && !isProxyStop(event)) {
    if (!isAgentVisibleInActiveTab(hostId, sessionCode)) {
      patch.unread = { ...s.unread, [key]: true }
      // The cursor of the event that LAST set the unread: a later event replaces it, one with none leaves none.
      const cursor = cursorOf(event)
      if (cursor) patch.unreadCursor = { ...s.unreadCursor, [key]: cursor }
      else if (key in s.unreadCursor) patch.unreadCursor = dropKey(s.unreadCursor, key)
    } else {
      // On screen, so it never becomes unread here — but the person saw it: tell the daemon, or a device elsewhere
      // keeps an unread of something already seen (spec §7.1). Reported after the state is written. Only while the
      // window is actually visible: the active tab of a minimized or covered window is not a person looking, and the
      // report clears OTHER devices' unread.
      if (isWindowVisible()) seen.push({ sessionCode, cursor: cursorOf(event) })
    }
  }
  return patch
}

/** Is this window showing its page to a person (not minimized, not covered, not on another Space)? True where there is no document. */
function isWindowVisible(): boolean {
  return typeof document === 'undefined' || document.visibilityState !== 'hidden'
}

/** Report each seen-live event (after the state write: the reporter may read the store). */
function reportSeen(hostId: string, seen: SeenLive[]): void {
  for (const e of seen) reportRead(hostId, e.sessionCode, e.cursor)
}

export const useAgentStore = create<AgentState>()(
  (set, get) => ({
    statuses: {},
    agentTypes: {},
    models: {},
    subagents: {},
    lastEvents: {},
    oscTitles: {},
    ccStatus: {},
    unread: {},
    unreadCursor: {},

    clearSession: (hostId, sessionCode) => {
      const key = compositeKey(hostId, sessionCode)
      set((s) => withoutKey(s, key))
    },

    handleNormalizedEvent: (hostId, sessionCode, event) => {
      eventSideEffects(hostId, sessionCode, event)
      const seen: SeenLive[] = []
      set((s) => eventPatch(s, hostId, sessionCode, event, 'worker', seen))
      reportSeen(hostId, seen)
    },

    applyHookEvent: (hostId, sessionCode, event) => {
      eventSideEffects(hostId, sessionCode, event)
      const seen: SeenLive[] = []
      set((s) => eventPatch(s, hostId, sessionCode, event, 'transition', seen))
      reportSeen(hostId, seen)
    },

    applyAgentSnapshot: (hostId, entries) => {
      for (const e of entries) eventSideEffects(hostId, e.session, e.event)
      const seen: SeenLive[] = []
      set((s) => {
        let draft: AgentState = s
        const listed = new Set<string>()
        for (const e of entries) {
          listed.add(compositeKey(hostId, e.session))
          draft = { ...draft, ...eventPatch(draft, hostId, e.session, e.event, 'transition', seen) }
        }
        // A code of this host the snapshot does not list has no agent any more: clear it, but only a code that
        // holds agent state, and never an execution pane (owned by the worker projection, not daemon hook frames).
        const prefix = `${hostId}:`
        const held = new Set<string>([...Object.keys(draft.statuses), ...Object.keys(draft.lastEvents)])
        for (const key of held) {
          if (!key.startsWith(prefix) || listed.has(key)) continue
          if (key.slice(prefix.length).startsWith('exec-')) continue
          draft = { ...draft, ...withoutKey(draft, key) }
        }
        return draft
      })
      reportSeen(hostId, seen)
    },

    setSubagents: (hostId, sessionCode, subagents) => set((s) => {
      const key = compositeKey(hostId, sessionCode)
      if (subagents.length > 0) return { subagents: { ...s.subagents, [key]: subagents } }
      if (!(key in s.subagents)) return s
      const { [key]: _, ...rest } = s.subagents
      return { subagents: rest }
    }),

    markRead: (hostId, sessionCode) => {
      const key = compositeKey(hostId, sessionCode)
      // Captured NOW, at the transition: a later event may set the unread again with a higher cursor, and that one
      // was not read by this.
      const { unread, unreadCursor } = get()
      const wasUnread = unread[key] === true
      const transition: ReadTransition = { wasUnread, cursor: wasUnread ? unreadCursor[key] : undefined }
      set((s) => ({ unread: dropKey(s.unread, key), unreadCursor: dropKey(s.unreadCursor, key) }))
      return transition
    },

    applyReads: (hostId, reads) => set((s) => {
      let unread = s.unread
      let unreadCursor = s.unreadCursor
      for (const { code, through } of reads) {
        const key = compositeKey(hostId, code)
        if (unread[key] !== true || !covers(unreadCursor[key], through)) continue
        unread = dropKey(unread, key)
        unreadCursor = dropKey(unreadCursor, key)
      }
      return unread === s.unread ? s : { unread, unreadCursor }
    }),

    removeHost: (hostId) => set((s) => {
      const prefix = `${hostId}:`
      const byPrefix = (k: string) => k.startsWith(prefix)
      return {
        statuses: omitKeys(s.statuses, byPrefix),
        agentTypes: omitKeys(s.agentTypes, byPrefix),
        models: omitKeys(s.models, byPrefix),
        subagents: omitKeys(s.subagents, byPrefix),
        lastEvents: omitKeys(s.lastEvents, byPrefix),
        oscTitles: omitKeys(s.oscTitles, byPrefix),
        ccStatus: omitKeys(s.ccStatus, byPrefix),
        unread: omitKeys(s.unread, byPrefix),
        unreadCursor: omitKeys(s.unreadCursor, byPrefix),
      }
    }),

    setOscTitle: (hostId, sessionCode, title) => set((s) => {
      const key = compositeKey(hostId, sessionCode)
      const cleaned = sanitizeOscTitle(title)
      if (!cleaned) {
         
        const { [key]: _, ...rest } = s.oscTitles
        return { oscTitles: rest }
      }
      if (s.oscTitles[key] === cleaned) return s
      return { oscTitles: { ...s.oscTitles, [key]: cleaned } }
    }),

    setCcStatus: (hostId, sessionCode, raw, receivedAt) => {
      const key = compositeKey(hostId, sessionCode)
      // The daemon's own receive time when it sent one (a snapshot replayed after a restart is old), else now.
      // Only a finite millisecond value (>= 1e12; a smaller one looks like seconds) counts; one from the future (a small skew, or
      // an untrustworthy far-future value) is pinned to now so it can never make the line look fresher than it is.
      const now = Date.now()
      const atTrusted = typeof receivedAt === 'number' && Number.isFinite(receivedAt) && receivedAt >= 1e12
      const at = atTrusted ? Math.min(receivedAt, now) : now
      const entry: CcStatusEntry = { receivedAt: at, atTrusted, raw }
      set((s) => ({ ccStatus: { ...s.ccStatus, [key]: entry } }))
    },

    /**
     * Wipe CC statusLine state for a host. Terminal OSC 0/2 title state is
     * preserved because CC statusLine no longer owns title display.
     *
     * Called on the `agent.status.cleared` WS event, broadcast by the daemon
     * when the CC statusLine wrapper is uninstalled.
     *
     * Does NOT touch statuses / agentTypes / models / subagents / unread /
     * lastEvents — those track agent hook events, which are orthogonal to
     * statusLine install state. Uninstalling the wrapper does not imply the
     * CC session has ended; the hook stream may continue.
     */
    clearHostAgentStatus: (hostId) => set((s) => {
      const prefix = `${hostId}:`
      const ccKeysToRemove = Object.keys(s.ccStatus).filter((k) => k.startsWith(prefix))
      if (ccKeysToRemove.length === 0) return s
      return {
        ccStatus: omitKeys(s.ccStatus, (k) => k.startsWith(prefix)),
      }
    }),
  }),
)
