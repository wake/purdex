// spa/src/components/status/PaneModeButtons.tsx — the status bar's terminal / worker / chat buttons (shell cleanup spec
// D.3, §9.3). They show the mode of the pane the status bar shows (its status target, rule D.4) and switch it; every
// action goes to that pane, never to the tab's primary pane.
//
// | target now      | terminal                         | worker                     | chat                       |
// |-----------------|----------------------------------|----------------------------|----------------------------|
// | tmux-session    | pressed                          | gate → Hand to Nex (room)  | gate → Hand to Nex (chat)  |
// | execution, room | the view's own Take to terminal  | pressed                    | → chat                     |
// | execution, chat | the view's own Take to terminal  | → room                     | pressed                    |
//
// Nothing here reimplements a flow: Hand to Nex opens the app-level dialog (`useHandoffDialogStore`), room ↔ chat goes
// through the same guarded write as the pane's view menu (`setExecutionPaneMode`), and Take to terminal calls the
// mounted view's own handler (`take-to-terminal-registry`), with its running-turn confirm and busy guards.
import type { Icon } from '@phosphor-icons/react'
import { ChatCircle, Robot, TerminalWindow } from '@phosphor-icons/react'
import { useHandoffGate } from '../../hooks/useHandoffCandidate'
import { useTakeToTerminal } from '../../lib/nex/take-to-terminal-registry'
import { setExecutionPaneMode, viewModeOf } from '../../lib/nex/view-mode'
import { resolveExecutionHostId } from '../../lib/nex/resolve-host'
import { useHandoffDialogStore } from '../../stores/useHandoffDialogStore'
import { useI18nStore } from '../../stores/useI18nStore'
import type { ExecutionViewMode, Pane } from '../../types/tab'

type Mode = 'terminal' | ExecutionViewMode

/** One button's state: pressed (the current mode; a click does nothing), an action, or disabled with why. */
type ButtonState =
  | { kind: 'pressed' }
  | { kind: 'action'; run: () => void }
  | { kind: 'disabled'; why: string }

const PRESSED: ButtonState = { kind: 'pressed' }

const BUTTONS: ReadonlyArray<{ mode: Mode; icon: Icon; label: string }> = [
  { mode: 'terminal', icon: TerminalWindow, label: 'status.mode.terminal' },
  { mode: 'room', icon: Robot, label: 'status.mode.worker' },
  { mode: 'chat', icon: ChatCircle, label: 'status.mode.chat' },
]

const BASE = 'flex items-center px-1 py-0.5 rounded border transition-colors'
const TONE: Record<ButtonState['kind'], string> = {
  pressed: 'border-accent-base/40 bg-accent-base/10 text-accent-base cursor-default',
  action: 'border-border-default text-text-secondary cursor-pointer hover:bg-surface-hover hover:text-text-primary',
  disabled: 'border-border-default text-text-secondary opacity-40 cursor-default',
}

export function PaneModeButtons({ tabId, pane }: { tabId: string; pane: Pane }) {
  const t = useI18nStore((s) => s.t)
  const { content } = pane
  // Both read unconditionally (hooks); each is inert for the other kind: the gate subscribes to constants for a
  // non-session pane, and a tmux pane has no registered worker view.
  const gate = useHandoffGate(content)
  const take = useTakeToTerminal(pane.id)

  let states: Record<Mode, ButtonState>
  if (content.kind === 'tmux-session') {
    const handOff = (mode?: ExecutionViewMode): ButtonState => {
      if (!gate.ok) return { kind: 'disabled', why: t(`status.mode.${gate.reason}`) }
      return {
        kind: 'action',
        run: () => useHandoffDialogStore.getState().open({ tabId, paneId: pane.id, content, ...(mode ? { mode } : {}) }),
      }
    }
    // Worker opens the dialog with no mode: a handed-off pane opens in room, the default.
    states = { terminal: PRESSED, room: handOff(), chat: handOff('chat') }
  } else if (content.kind === 'execution') {
    const current = viewModeOf(content)
    // The worker this bar shows, keyed as the pane keys its view (host + execution id, P6 review A2).
    const worker = { executionId: content.executionId, host: resolveExecutionHostId(content.host) }
    const switchTo = (mode: ExecutionViewMode): ButtonState => (mode === current
      ? PRESSED
      : { kind: 'action', run: () => setExecutionPaneMode(tabId, pane.id, worker, mode) })
    const terminal: ButtonState = !take || !take.canTake
      ? { kind: 'disabled', why: t('status.mode.cannot_take') }
      : take.busy
        ? { kind: 'disabled', why: t('status.mode.busy') }
        : { kind: 'action', run: take.takeToTerminal }
    states = { terminal, room: switchTo('room'), chat: switchTo('chat') }
  } else {
    return null
  }

  return (
    <span
      data-testid="status-mode-buttons"
      role="group"
      aria-label={t('status.mode.label')}
      className="flex items-center gap-1 max-[500px]:hidden"
    >
      {BUTTONS.map(({ mode, icon: ModeIcon, label }) => {
        const state = states[mode]
        const name = t(label)
        return (
          <button
            key={mode}
            type="button"
            aria-label={name}
            aria-pressed={state.kind === 'pressed'}
            // The name stays the label; a disabled button's title says why, which reads as its description.
            title={state.kind === 'disabled' ? state.why : name}
            disabled={state.kind === 'disabled'}
            onClick={state.kind === 'action' ? state.run : undefined}
            className={`${BASE} ${TONE[state.kind]}`}
          >
            <ModeIcon size={12} weight={state.kind === 'pressed' ? 'fill' : 'regular'} />
          </button>
        )
      })}
    </span>
  )
}
