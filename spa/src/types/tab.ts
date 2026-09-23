import { generateId } from '../lib/id'
import type { FileSource } from './fs'

// === Tab (tab bar unit) ===
export interface Tab {
  id: string
  pinned: boolean
  locked: boolean
  createdAt: number
  layout: PaneLayout
}

// === Pane Layout (tab-internal split tree) ===
export type SplitLayout = { type: 'split'; id: string; direction: 'h' | 'v'; children: PaneLayout[]; sizes: number[] }
export type PaneLayout =
  | { type: 'leaf'; pane: Pane }
  | SplitLayout

export type LayoutPattern = 'single' | 'split-h' | 'split-v'

// === Pane (content slot) ===
export interface Pane {
  id: string
  content: PaneContent
}

// === Pane Content (discriminated union) ===
export type TerminatedReason = 'session-closed' | 'tmux-restarted' | 'host-removed'

export interface UntitledDocumentState {
  name: string
  suggestedExtension: '.txt' | '.md'
  hasBeenRenamed: boolean
}

/**
 * Everything needed to recreate the tmux session a pane was bound to, once
 * that session is gone (spec §4.1). Accumulated in place by three ranked
 * writers — see `setPaneRebuild` in `useTabStore`.
 *
 * Optional on the pane: alpha convention is no persist migration, so panes
 * persisted before this feature simply have no record and behave as before.
 */
export interface PaneRebuildRecord {
  /** tmux session name actually in use (including any collision suffix). */
  sessionName: string
  /** Generation this record describes: the host's tmux_instance at write time. */
  tmuxInstance: string
  /** cwd the agent was launched in — the cwd its resume is scoped to. */
  cwd?: string
  cwdSource?: 'agent-session-start' | 'agent-backfill' | 'pane-probe' | 'user'
  agent?: {
    /** 'cc' | 'codex' | 'opencode' — open string, mirrors AGENT_NAMES. */
    type: string
    sessionId?: string
    /** tmux pane the owning SessionStart came from (spec §4.4). */
    tmuxPaneId?: string
    /**
     * The daemon frame that IS this agent run (agent-last-state spec, review
     * decision 2). An exit applies only when its frame id equals this one, so
     * a late exit of an older run with the same session id can never mark the
     * current run exited. Absent on records written before the daemon sent it:
     * exits never apply to those until their next SessionStart.
     */
    frameId?: string
    updatedAt: number
  }
  /**
   * The recorded agent has exited (agent-last-state spec §2). Absent means "the
   * agent was running when last seen" — which is also what an exit the SPA
   * never heard (it was disconnected) leaves behind. The agent identity is
   * kept, so "resume anyway" stays possible; `at` is the daemon's Unix ms,
   * for display only.
   */
  agentExited?: { at: number; reason: AgentExitReason }
  /**
   * User override for this pane only. Absent means "compose from the agent's
   * templates" (spec §4.2), which is the normal case: the record stores the
   * agent IDENTITY and `resolveResumeCommand` composes the command from it, so
   * a user who retypes a template in Settings changes what every pane resumes
   * with. Only a hand edit ever writes this, and it is cleared when the agent
   * identity it was written against changes (spec §4.3).
   *
   * It replaces the old auto-composed `resumeCommand`, which is gone rather
   * than renamed: repurposing it would have promoted every already-persisted
   * composed string into an override and pinned every existing record to the
   * old shape. Per the alpha convention no migration is written — a stale
   * `resumeCommand` key in persisted state is inert, because nothing reads it.
   */
  resumeCommandOverride?: string
  /**
   * The record's agent disagrees with what the daemon currently reports for
   * the session (spec §9.1): still shown, but unchecked by default and skipped
   * by "Rebuild all".
   */
  unverified?: boolean
  capturedAt: number
}

/** Why the recorded agent is gone: its own SessionEnd, or the daemon's pid sweep. */
export type AgentExitReason = 'session-end' | 'process-dead'

export type PaneContent =
  | { kind: 'new-tab' }
  | { kind: 'tmux-session'; hostId: string; sessionCode: string; mode: 'terminal'; cachedName: string; tmuxInstance: string; terminated?: TerminatedReason; rebuild?: PaneRebuildRecord }
  | { kind: 'dashboard' }
  | { kind: 'hosts' }
  | { kind: 'history' }
  | { kind: 'settings'; scope: 'global' | { workspaceId: string } }
  | { kind: 'browser'; url: string }
  | { kind: 'memory-monitor' }
  | { kind: 'editor'; source: FileSource; filePath: string; untitled?: UntitledDocumentState; diff?: { against: 'saved' | string } }
  | { kind: 'editor-buffers' }
  | { kind: 'image-preview'; source: FileSource; filePath: string }
  | { kind: 'pdf-preview'; source: FileSource; filePath: string }
  // Execution detail page (M0 dispatch, Task P.12). Observe-only landing when a
  // deeplink cannot focus a live session tab; `host` is the optional daemon hint
  // carried by the deeplink. Never attaches a stdin write path.
  //
  // `from` (P-C.3b): set when the pane was swapped from a tmux session by
  // "Hand to nex" — the session CC exited in, so "Take back" can resume it
  // there. Pane-local provenance only: it takes no part in `contentMatches`
  // or the route, but `openSingletonTab` prefers a pane that carries it.
  | { kind: 'execution'; executionId: string; host?: string; from?: ExecutionFrom }

/** The tmux session an execution pane was handed off from (spec §4.4). */
export interface ExecutionFrom {
  sessionCode: string
  tmuxInstance: string
  cachedName: string
}

/** The `execution` arm of `PaneContent`. */
export type ExecutionContent = Extract<PaneContent, { kind: 'execution' }>

/** The `tmux-session` arm of `PaneContent`, named so writers can talk about it. */
export type TmuxSessionContent = Extract<PaneContent, { kind: 'tmux-session' }>

/**
 * One write against a pane's `PaneRebuildRecord`. The kinds are the writer
 * ranking of spec §4.1, and that ranking is the whole concurrency policy:
 *
 * - `agent-group` — a qualifying SessionStart. Replaces `agent`, `cwd`,
 *   `cwdSource` and `capturedAt` **as one unit**; a payload without `cwd`
 *   clears `cwd` rather than leaving the previous agent's directory attached
 *   to a new session id. `resumeCommandOverride` is NOT part of the unit: it
 *   is a user edit, and survives unless the identity it was written against
 *   changed (spec §4.3).
 * - `field` — a user edit. Touches only the field named.
 * - `probe-cwd` — the pane cwd probe. Fills `cwd` only when it is unset;
 *   never promoted to agent provenance.
 * - `unverified` — the reconnect projection disagrees with `agent.type`.
 * - `agent-backfill` — the daemon's answer to "who owns this pane" (spec §5.5).
 *   Ranks BELOW `agent-group`, which is a first-hand SessionStart: this one is
 *   inferred from a process tree, so it fills a gap, corrects a record already
 *   flagged `unverified` or `agentExited`, or confirms one — and otherwise does
 *   nothing.
 * - `agent-exit` — the daemon's `pdx_exit` envelope. Sets `agentExited` only on
 *   a record whose `agent.frameId` equals the exit's, and is the one
 *   session-scoped write a TERMINATED pane still takes (agent-last-state spec,
 *   review decision 4): termination can arrive before the exit broadcast.
 */
export type RebuildPatch =
  | { kind: 'agent-group'; record: Omit<PaneRebuildRecord, 'sessionName'> }
  | {
      kind: 'agent-backfill'
      record: {
        tmuxInstance: string
        agent: NonNullable<PaneRebuildRecord['agent']>
        cwd?: string
      }
    }
  | { kind: 'field'; field: 'cwd' | 'resumeCommandOverride' | 'sessionName'; value: string }
  | { kind: 'probe-cwd'; cwd: string }
  | { kind: 'unverified'; unverified: boolean }
  | { kind: 'agent-exit'; frameId: string; exited: NonNullable<PaneRebuildRecord['agentExited']> }

// === Workspace ===
export type IconWeight = 'bold' | 'regular' | 'thin' | 'light' | 'fill' | 'duotone'

export interface Workspace {
  id: string
  name: string
  icon?: string
  iconWeight?: IconWeight
  tabs: string[]
  activeTabId: string | null
  moduleConfig?: Record<string, Record<string, unknown>>
}
// === Factories ===

export function createTab(content: PaneContent, opts?: { pinned?: boolean }): Tab {
  return {
    id: generateId(),
    pinned: opts?.pinned ?? false,
    locked: false,
    createdAt: Date.now(),
    layout: { type: 'leaf', pane: { id: generateId(), content } },
  }
}

export function createWorkspace(name: string, icon?: string): Workspace {
  return {
    id: generateId(),
    name,
    icon,
    tabs: [],
    activeTabId: null,
    moduleConfig: {},
  }
}
