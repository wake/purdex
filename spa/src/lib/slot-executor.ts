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
 * The executor is shared by Phase 1b workspace entry and Phase 1c host entry
 * (the latter calls it with workspaceId omitted; the cwd-resolution defaults
 * differ per slot caller, see spec §3.2 table).
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

  try {
    await executeCommand(hostId, sessionCode, cmd.command)
  } catch {
    // Step 2 failed — STILL switch (so user sees the orphan), WITH Retry action.
    safelySwitch(hostId, sessionCode, deps, t)
    toast.show(
      t('quick_commands.toast.send_keys_failed'),
      // retry: re-run send-keys; failures dropped (user can keep clicking).
      () => {
        void executeCommand(hostId, sessionCode, cmd.command).catch(() => undefined)
      },
      // codex round-1 B4 — only this branch carries an action label
      t('quick_commands.toast.retry'),
    )
    return
  }

  safelySwitch(hostId, sessionCode, deps, t)
}

function safelySwitch(
  hostId: string,
  sessionCode: string,
  deps: Deps,
  t: ReturnType<typeof useI18nStore.getState>['t'],
): void {
  try {
    deps.switchToSession(hostId, sessionCode)
  } catch {
    // codex round-1 B4 — switch failure has NO retry action either; the session
    // is already alive elsewhere, the toast is purely informational.
    useUndoToast.getState().show(t('quick_commands.toast.switch_failed'))
  }
}
