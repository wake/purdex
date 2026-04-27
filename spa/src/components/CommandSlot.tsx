import { useMemo, type ReactNode } from 'react'
import { useQuickCommandStore } from '../stores/useQuickCommandStore'
import { useModuleEnabledStore } from '../stores/useModuleEnabledStore'
import { useI18nStore } from '../stores/useI18nStore'
import { getBindingTargets, type QuickCommand } from '../lib/quick-command-bindings'
import type { QuickCommandSlotId } from '../lib/quick-command-slots'

/**
 * Slot context (spec v4 §3.2.1 / §3.2.2 / §3.5).
 *
 * `hostId` is `string | null`:
 *   - WORKSPACE_ACTIONS: caller provides `inferWorkspaceHostId(ws, tabs)`
 *     which can return null when the workspace has no tmux-session tabs.
 *   - HOST_ACTIONS: caller always provides a concrete hostId (host detail
 *     page already knows which host it's rendering).
 *
 * When hostId is null, the executor MUST resolve it (e.g. by opening the
 * HostPickerPopover) before performing any host-side work — silently
 * falling back to `useHostStore.activeHostId` is forbidden.
 */
export interface SlotContext {
  hostId: string | null
  workspaceId?: string | null
  cwd?: string
}

export type SlotExecutor = (cmd: QuickCommand, ctx: SlotContext) => Promise<void>
/**
 * codex round-2 — `run` is the 3rd argument injected by `<CommandSlot>` so a
 * custom render can wire its own onClick to the executor pipeline. Without it
 * a custom render becomes an inert UI footgun (the chip is visible but
 * clicking does nothing). `run` is the same `executor(cmd, ctx)` Promise the
 * default chip kicks off; callers should still respect `busy` and avoid
 * double-firing.
 */
export type SlotRenderer = (cmd: QuickCommand, ctx: SlotContext, run: () => void) => ReactNode

interface Props {
  mountTo: QuickCommandSlotId
  ctx: SlotContext
  executor: SlotExecutor
  render?: SlotRenderer
  /**
   * Optional class for the outer container (codex round-1 B7 — used by the
   * context-menu caller which prefers `flex flex-col` over the toolbar default).
   */
  containerClassName?: string
  /**
   * codex round-1 C11 — picker resolver race guard: when the caller's host
   * picker is open, set `busy={true}` to disable every chip button. Prevents
   * a second click from creating a second pending Promise (and a second picker
   * instance fighting over the same resolver).
   */
  busy?: boolean
}

/**
 * Renders all commands bound to `mountTo` for the current `ctx.hostId`. The
 * Quick Commands module enable state is checked at the very top — disabling
 * the module makes every slot vanish app-wide without consumer changes.
 *
 * Render order is the capability order (`getBoundCommands` iterates the
 * stable `getCommands(hostId)` list), not `Object.keys(bindings)` — that's
 * the spec §4.4 stability guarantee against post-sync key-order divergence.
 */
export function CommandSlot({
  mountTo,
  ctx,
  executor,
  render,
  containerClassName,
  busy,
}: Props) {
  const enabled = useModuleEnabledStore((s) => s.isEnabled('quick-commands'))
  const bindings = useQuickCommandStore((s) => s.bindings)
  const t = useI18nStore((s) => s.t)
  // hostId null → no host override possible; show global-only command list.
  // Non-null → per-host capability list with overrides applied.
  const allCmds = useQuickCommandStore((s) =>
    ctx.hostId == null ? s.global : s.getCommands(ctx.hostId),
  )

  // Recompute boundCmds when bindings or capability list change.
  // codex round-1 P2 — use getBindingTargets (own-property guard) instead of
  // raw `bindings[c.id]?.includes`. A capability id that collides with an
  // inherited Object.prototype method (toString / valueOf / hasOwnProperty)
  // would otherwise resolve to that function and `.includes(mountTo)` would
  // throw, crashing every slot host.
  const boundCmds = useMemo(
    () =>
      allCmds.filter((c) => {
        const targets = getBindingTargets(bindings, c.id)
        return targets !== undefined && targets.includes(mountTo)
      }),
    [allCmds, bindings, mountTo],
  )

  if (!enabled) return null
  if (boundCmds.length === 0) return null

  return (
    <div
      className={containerClassName ?? 'flex flex-wrap items-center gap-1.5'}
      role="toolbar"
      // codex round-1 C16 — aria-label sourced from i18n, not hard-coded English
      aria-label={t('quick_commands.aria.toolbar')}
    >
      {boundCmds.map((cmd) => {
        // codex round-2 — `run` injected so custom render can wire its own
        // onClick. Without it custom render becomes inert (chip visible, click
        // does nothing) — the same footgun the original 2-arg signature shipped.
        const run = () => {
          if (busy) return
          void executor(cmd, ctx)
        }
        if (render) {
          return (
            <span key={cmd.id} className="inline-flex">
              {render(cmd, ctx, run)}
            </span>
          )
        }
        const ariaLabel = cmd.category ? `${cmd.name} (${cmd.category})` : cmd.name
        return (
          <button
            key={cmd.id}
            type="button"
            // codex round-1 C11 — busy guard prevents double-click from spawning
            // a second picker / executor pipeline while one is mid-flight.
            disabled={busy}
            onClick={run}
            aria-label={ariaLabel}
            title={cmd.command}
            className="inline-flex items-center gap-1 px-2 py-1 rounded text-xs text-text-secondary hover:text-text-primary hover:bg-surface-secondary cursor-pointer disabled:opacity-50 disabled:cursor-not-allowed"
          >
            <span className="truncate max-w-[12rem]">{cmd.name}</span>
            {cmd.category && (
              <span className="text-[10px] text-text-muted bg-surface-primary px-1.5 py-0.5 rounded">
                {cmd.category}
              </span>
            )}
          </button>
        )
      })}
    </div>
  )
}
