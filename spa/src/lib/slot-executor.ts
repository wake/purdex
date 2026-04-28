import { createSession } from './host-api'
import { executeCommand } from './execute-command'
import { useUndoToast } from '../stores/useUndoToast'
import { useI18nStore } from '../stores/useI18nStore'
import type { QuickCommand } from './quick-command-bindings'
import type { SlotContext } from '../components/CommandSlot'

/**
 * Workspace-narrowed slot context (#690 round-2 D1 / spec §3.3.1):
 * `workspaceId` is required (non-null string) — the round-4 destructive-command
 * guard relies on a real workspace to validate via `assertContextLive`. Host
 * contexts have no workspace to probe and must NOT reuse `runWorkspaceSlot`;
 * Phase 1c will introduce a sibling `runHostSlot` with `HostSlotContext` instead.
 *
 * `SlotContext.workspaceId` itself stays optional (HOST_ACTIONS shares the same
 * shape via CommandSlot props). The narrow happens at this entry point only.
 */
export interface WorkspaceSlotContext extends SlotContext {
  workspaceId: string
}

interface Deps {
  /**
   * Switches the active tab/pane to the freshly-created session.
   * Called after the session exists, regardless of whether send-keys
   * succeeded — see spec §3.3 (silent orphan sessions are the worst UX).
   */
  switchToSession: (hostId: string, sessionCode: string) => void

  /**
   * Resolves a hostId when ctx.hostId is null (spec v4 §3.2.2 — Option B).
   * Caller opens the HostPickerPopover inside this callback and resolves
   * with the user-selected hostId, or `null` if the user cancels.
   *
   * Required regardless of ctx.hostId — when ctx.hostId is non-null this
   * callback is never invoked (see happy-path test). Caller still passes
   * a noop / never-called function to satisfy the type contract.
   */
  resolveHostId: () => Promise<string | null>

  /**
   * Required (#690 / spec §3.3.1) — workspace liveness probe. Returns false if
   * the surrounding UI context (e.g. workspace) was destroyed while async work
   * was in flight. The executor calls this after `createSession` resolves and
   * BEFORE `executeCommand` runs so destructive commands aren't sent to a
   * session the user can no longer reach (codex round-4 of PR #686).
   *
   * Phase 1c HOST_ACTIONS will introduce a separate `runHostSlot` entry point
   * — host context has no workspace to verify and must NOT reuse this function
   * (was originally optional to share, but that path silently regresses the
   * round-4 guard for any future workspace-context caller that forgets to
   * wire it; e.g. Phase 1b' Plus hover popover).
   */
  assertContextLive: () => boolean
}

function genSessionName(cmd: QuickCommand): string {
  const ts = new Date().toISOString().slice(11, 19) // HH:MM:SS
  return `${cmd.name} ${ts}`.slice(0, 64)
}

/**
 * Workspace-slot executor:
 *  1. POST /api/sessions (cwd: ctx.cwd ?? sane default)
 *  2. POST /api/sessions/{code}/send-keys
 *  3. switchToSession()
 *
 * Failure UX (spec §3.3):
 *  - Step 1 fails → toast "Failed to start session: <reason>", abort.
 *  - Step 1 ok + Step 2 fails → STILL switch focus, toast with Retry action.
 *  - Step 1 ok + Step 2 ok + Step 3 fails (rare) → toast pointing user to
 *    the sessions list (session is alive elsewhere).
 *
 * Workspace-only entry (#690 / spec §3.3.1). Phase 1c HOST_ACTIONS will
 * introduce a sibling `runHostSlot` rather than reusing this — host callers
 * have no workspace context to validate via `assertContextLive`, and sharing
 * the executor with an optional probe was the round-4 regression risk #690
 * is closing.
 */
export async function runWorkspaceSlot(
  cmd: QuickCommand,
  ctx: WorkspaceSlotContext,
  deps: Deps,
): Promise<void> {
  const t = useI18nStore.getState().t
  const toast = useUndoToast.getState()

  // spec v4 §3.2.2 — null hostId resolution via caller-injected callback.
  // We do NOT silently fall back to activeHostId.
  let hostId: string
  if (ctx.hostId == null) {
    const picked = await deps.resolveHostId()
    if (picked == null) {
      // User cancelled the picker → no-op, no toast.
      return
    }
    hostId = picked
  } else {
    hostId = ctx.hostId
  }

  let sessionCode: string
  try {
    const session = await createSession(hostId, genSessionName(cmd), ctx.cwd ?? '~', 'terminal')
    sessionCode = session.code
  } catch (err) {
    // codex round-1 B4 — create-session failure has NO retry/undo action;
    // user has nothing meaningful to retry without re-clicking the chip.
    const reason = err instanceof Error ? err.message : String(err)
    toast.show(t('quick_commands.toast.create_failed', { reason }))
    return
  }

  // codex round-4 (PR #686) — workspace liveness gate. If the surrounding
  // context (e.g. workspace) was destroyed while createSession was in flight,
  // do NOT send the user command — destructive quick commands (rm / drop /
  // etc.) must not execute remotely once their context is gone. The session
  // is already created server-side; surface it as a switch failure (no retry)
  // so the user knows the operation didn't reach completion. Cleanup of the
  // server-side session is tracked separately (#689).
  //
  // #690 round-2 A2 — defense in depth. Type-level enforcement guarantees
  // `assertContextLive` is a function, but a cast-bypass (`as any` / `as
  // unknown as Deps` / Object.assign with payload-as-any) could still ship
  // a non-function or one that throws. Fail closed in either case so we
  // never reach `executeCommand` when the probe is unreliable.
  let live = false
  try {
    if (typeof deps.assertContextLive === 'function') {
      live = deps.assertContextLive() === true
    }
  } catch {
    live = false
  }
  if (!live) {
    toast.show(t('quick_commands.toast.switch_failed'))
    return
  }

  let sendKeysOk = true
  try {
    await executeCommand(hostId, sessionCode, cmd.command)
  } catch {
    sendKeysOk = false
  }

  // codex round-2 — three-stage failure precedence (replaces stitched-together
  // toast handlers that overwrote each other). switchToSession failure is more
  // serious than send-keys failure (an orphan session the user can't even see
  // beats one they can see but can't drive), so when both fail we surface
  // switch_failed and DROP the retry action — Retry would try to re-send to
  // a session the user can't navigate to anyway.
  const switchOk = trySwitch(deps, hostId, sessionCode)

  if (sendKeysOk && switchOk) return

  if (!switchOk) {
    // Switch failure (with or without preceding send-keys failure) → no action button.
    toast.show(t('quick_commands.toast.switch_failed'))
    return
  }

  // sendKeysOk = false, switchOk = true → user can see the orphan; offer Retry.
  toast.show(
    t('quick_commands.toast.send_keys_failed'),
    () => {
      void executeCommand(hostId, sessionCode, cmd.command).catch(() => undefined)
    },
    t('quick_commands.toast.retry'),
  )
}

function trySwitch(deps: Deps, hostId: string, sessionCode: string): boolean {
  try {
    deps.switchToSession(hostId, sessionCode)
    return true
  } catch {
    return false
  }
}

/**
 * Host-narrowed slot context (Phase 1c — spec §3.2 HOST_ACTIONS row + §3.3.1).
 *
 * `hostId` is required non-null string — host detail page (the only Phase 1c
 * caller) owns the hostId via prop. spec §3.3.1 explicitly defers
 * HostSlotContext shape to 1c. We pick the minimal shape today; widen only
 * when a real caller demands it.
 */
export interface HostSlotContext extends SlotContext {
  hostId: string
}

interface HostDeps {
  switchToSession: (hostId: string, sessionCode: string) => void
  /**
   * Required — Finding 1 of plan-review job task-moijtc8w-w8rf8w.
   *
   * Host liveness probe. Must return false if the host record (the row
   * matching `hostId` in `useHostStore.hosts`) was deleted while async work
   * was in flight — `useHostStore.getDaemonBase` falls back to
   * `activeHostId ?? hostOrder[0]` when the host is gone, so without this
   * guard a destructive command can ship to the wrong host.
   *
   * Called twice: after createSession resolves (before executeCommand), AND
   * inside the retry action (because the toast can sit before the user
   * clicks retry, and the host can be deleted in that window).
   */
  assertHostLive: (hostId: string) => boolean
}

/**
 * Host-slot executor — same failure-precedence rules as runWorkspaceSlot but
 * with a host-side (not workspace-side) liveness probe. See spec §3.3 for
 * shared failure UX, plan §1c for HOST_ACTIONS-specific carve-outs.
 */
export async function runHostSlot(
  cmd: QuickCommand,
  ctx: HostSlotContext,
  deps: HostDeps,
): Promise<void> {
  const t = useI18nStore.getState().t
  const toast = useUndoToast.getState()

  // R2 fix (codex adversarial review) — pre-createSession host liveness
  // gate. Without this, a stale chip click after Settings-side host
  // deletion (before the chip re-renders disabled) would still call
  // createSession; useHostStore.getDaemonBase falls back to
  // activeHostId ?? hostOrder[0], so the orphan session would be created
  // on the WRONG host. The post-create probe stops executeCommand but
  // can't undo the orphan session. Pre-check + post-check + retry-check
  // is defense in depth.
  if (!isHostLive(deps, ctx.hostId)) {
    toast.show(t('quick_commands.toast.switch_failed'))
    return
  }

  let sessionCode: string
  try {
    const session = await createSession(ctx.hostId, genSessionName(cmd), ctx.cwd ?? '~', 'terminal')
    // Finding 6 — blank session.code is treated as a create failure: the
    // server returned 200 but produced no usable handle. Letting it through
    // would surface a confusing 404 from executeCommand later.
    const code = session.code?.trim()
    if (!code) {
      toast.show(t('quick_commands.toast.create_failed', { reason: 'empty session code' }))
      return
    }
    sessionCode = code
  } catch (err) {
    const reason = err instanceof Error ? err.message : String(err)
    toast.show(t('quick_commands.toast.create_failed', { reason }))
    return
  }

  // Finding 1 — host liveness gate. Defense in depth (mirrors PR #686
  // round-4 / #690 round-2 A2 for workspace): typeof + try/catch + strict
  // bool. Even with type-level required, a cast-bypass or buggy probe must
  // fail closed so executeCommand never ships to a fallback host.
  if (!isHostLive(deps, ctx.hostId)) {
    toast.show(t('quick_commands.toast.switch_failed'))
    return
  }

  let sendKeysOk = true
  try {
    await executeCommand(ctx.hostId, sessionCode, cmd.command)
  } catch {
    sendKeysOk = false
  }

  const switchOk = tryHostSwitch(deps, ctx.hostId, sessionCode)

  if (sendKeysOk && switchOk) return

  if (!switchOk) {
    toast.show(t('quick_commands.toast.switch_failed'))
    return
  }

  toast.show(
    t('quick_commands.toast.send_keys_failed'),
    () => {
      // Finding 1 — retry must re-check host liveness. Toast can sit
      // for several seconds; user may have deleted the host in that window.
      if (!isHostLive(deps, ctx.hostId)) return
      void executeCommand(ctx.hostId, sessionCode, cmd.command).catch(() => undefined)
    },
    t('quick_commands.toast.retry'),
  )
}

function isHostLive(deps: HostDeps, hostId: string): boolean {
  try {
    if (typeof deps.assertHostLive !== 'function') return false
    return deps.assertHostLive(hostId) === true
  } catch {
    return false
  }
}

function tryHostSwitch(deps: HostDeps, hostId: string, sessionCode: string): boolean {
  try {
    deps.switchToSession(hostId, sessionCode)
    return true
  } catch {
    return false
  }
}
