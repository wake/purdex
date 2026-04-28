import { createSession } from './host-api'
import { executeCommand } from './execute-command'
import { useUndoToast } from '../stores/useUndoToast'
import { useI18nStore } from '../stores/useI18nStore'
import type { QuickCommand } from './quick-command-bindings'
import type { SlotContext } from '../components/CommandSlot'

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
  ctx: SlotContext,
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
  if (!deps.assertContextLive()) {
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
