// spa/src/components/status/PaneModeButtons.tsx — the status bar's view buttons (shell cleanup spec D.3, §9.3; U3-1a).
// They show the pane the status bar shows (its status target, rule D.4) and act on that pane, never on the tab's
// primary pane.
//
// | target now      | group                                        | separate control                   |
// |-----------------|----------------------------------------------|------------------------------------|
// | tmux-session    | 終端機 · 指揮台 · 聊天 (views of the session)   | 交給執行體 (Hand to Nex, the gate)   |
// | execution       | 指揮室 (room) · 聊天 (chat)                     | 拿回終端機 (Take to terminal)        |
//
// The three views of a session pane are only ways to look at it (device-local, `useSessionViewStore`); handing the
// session over to Nexen is a different act and has its own button, so a view button never starts a handoff. Nothing
// here reimplements a flow: Hand to Nex opens the app-level dialog (`useHandoffDialogStore`), room ↔ chat goes through
// the same guarded write as the pane's view menu (`setExecutionPaneMode`), and Take to terminal calls the mounted
// view's own handler (`take-to-terminal-registry`), with its running-turn confirm and busy guards.
import type { Icon } from '@phosphor-icons/react'
import { ArrowLeft, ArrowRight, ChatCircle, Robot, SquaresFour, TerminalWindow } from '@phosphor-icons/react'
import { useConversationViewGate } from '../../hooks/useConversationViewGate'
import { useHandoffGate } from '../../hooks/useHandoffCandidate'
import { useTakeToTerminal } from '../../lib/nex/take-to-terminal-registry'
import { setExecutionPaneMode, viewModeOf } from '../../lib/nex/view-mode'
import { resolveExecutionHostId } from '../../lib/nex/resolve-host'
import { useHandoffDialogStore } from '../../stores/useHandoffDialogStore'
import { useI18nStore } from '../../stores/useI18nStore'
import { selectSessionView, sessionBinding, useSessionViewStore, type SessionView } from '../../stores/useSessionViewStore'
import { keepFocus } from '../../lib/keep-focus'
import type { ExecutionViewMode, Pane } from '../../types/tab'

/** One button's state: pressed (the current mode; a click does nothing), an action, or disabled with why. */
type ButtonState =
  | { kind: 'pressed' }
  | { kind: 'action'; run: () => void }
  | { kind: 'disabled'; why: string }

const PRESSED: ButtonState = { kind: 'pressed' }

interface ButtonSpec { id: string; icon: Icon; label: string; state: ButtonState; toggle: boolean }

const BASE = 'flex items-center px-1 py-0.5 rounded border transition-colors max-[500px]:hidden'
const TONE: Record<ButtonState['kind'], string> = {
  pressed: 'border-accent-base/40 bg-accent-base/10 text-accent-base cursor-default',
  action: 'border-border-default text-text-secondary cursor-pointer hover:bg-surface-hover hover:text-text-primary',
  disabled: 'border-border-default text-text-secondary opacity-40 cursor-default',
}

function ModeButton({ spec, t }: { spec: ButtonSpec; t: (key: string) => string }) {
  const { icon: ModeIcon, state } = spec
  const name = t(spec.label)
  return (
    <button
      type="button"
      aria-label={name}
      // Only the group's own buttons are toggles; a separate control is an action and is never "pressed".
      aria-pressed={spec.toggle ? state.kind === 'pressed' : undefined}
      // The name stays the label; a disabled button's title says why, which reads as its description.
      title={state.kind === 'disabled' ? state.why : name}
      disabled={state.kind === 'disabled'}
      // On every state, for one rule: a mouse press leaves focus on the pane (shell polish spec §4). A disabled
      // button never sees the press, so it is harmless there.
      onMouseDown={keepFocus}
      onClick={state.kind === 'action' ? state.run : undefined}
      className={`${BASE} ${TONE[state.kind]}`}
    >
      <ModeIcon size={12} weight={state.kind === 'pressed' ? 'fill' : 'regular'} />
    </button>
  )
}

export function PaneModeButtons({ tabId, pane }: { tabId: string; pane: Pane }) {
  const t = useI18nStore((s) => s.t)
  const { content } = pane
  // All read unconditionally (hooks); each is inert for the other kind: the gates subscribe to constants for a
  // non-session pane, and a tmux pane has no registered worker view.
  const handoff = useHandoffGate(content)
  const viewGate = useConversationViewGate(content)
  const take = useTakeToTerminal(pane.id)
  const binding = content.kind === 'tmux-session' ? sessionBinding(content.hostId, content.sessionCode) : ''
  const view = useSessionViewStore(selectSessionView(tabId, pane.id, binding))

  let group: ButtonSpec[]
  let control: ButtonSpec
  if (content.kind === 'tmux-session') {
    const toView = (v: SessionView): ButtonState => {
      if (v === view) return PRESSED
      // The terminal is always reachable; the conversation views need the gate.
      if (v !== 'terminal' && !viewGate.ok) return { kind: 'disabled', why: t(`status.mode.${viewGate.reason}`) }
      return { kind: 'action', run: () => useSessionViewStore.getState().setView(tabId, pane.id, binding, v) }
    }
    group = [
      { id: 'terminal', icon: TerminalWindow, label: 'status.mode.terminal', state: toView('terminal'), toggle: true },
      { id: 'deck', icon: SquaresFour, label: 'status.mode.deck', state: toView('deck'), toggle: true },
      { id: 'chat', icon: ChatCircle, label: 'status.mode.chat', state: toView('chat'), toggle: true },
    ]
    const handoffState: ButtonState = !handoff.ok
      ? { kind: 'disabled', why: t(`status.mode.${handoff.reason}`) }
      // Opens with no mode: a handed-off pane opens in room, the default.
      : { kind: 'action', run: () => useHandoffDialogStore.getState().open({ tabId, paneId: pane.id, content }) }
    control = { id: 'handoff', icon: ArrowRight, label: 'status.mode.handoff', state: handoffState, toggle: false }
  } else if (content.kind === 'execution') {
    const current = viewModeOf(content)
    // The worker this bar shows, keyed as the pane keys its view (host + execution id, P6 review A2).
    const worker = { executionId: content.executionId, host: resolveExecutionHostId(content.host) }
    const switchTo = (mode: ExecutionViewMode): ButtonState => (mode === current
      ? PRESSED
      : { kind: 'action', run: () => setExecutionPaneMode(tabId, pane.id, worker, mode) })
    group = [
      { id: 'room', icon: Robot, label: 'status.mode.worker', state: switchTo('room'), toggle: true },
      { id: 'chat', icon: ChatCircle, label: 'status.mode.chat', state: switchTo('chat'), toggle: true },
    ]
    const takeState: ButtonState = !take || !take.canTake
      ? { kind: 'disabled', why: t('status.mode.cannot_take') }
      : take.busy
        ? { kind: 'disabled', why: t('status.mode.busy') }
        : { kind: 'action', run: take.takeToTerminal }
    control = { id: 'take', icon: ArrowLeft, label: 'status.mode.take_back', state: takeState, toggle: false }
  } else {
    return null
  }

  return (
    <>
      <span
        data-testid="status-mode-buttons"
        role="group"
        aria-label={t('status.mode.label')}
        className="flex items-center gap-1 max-[500px]:hidden"
      >
        {group.map((spec) => <ModeButton key={spec.id} spec={spec} t={t} />)}
      </span>
      <ModeButton spec={control} t={t} />
    </>
  )
}
