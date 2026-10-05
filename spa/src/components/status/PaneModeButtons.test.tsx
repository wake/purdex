// spa/src/components/status/PaneModeButtons.test.tsx — the status bar's terminal / worker / chat buttons (shell cleanup
// spec §9.3). Every case runs in a split whose primary (first) pane is some other agent pane, so an action that went to
// the primary instead of the status target would show.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { render, screen, fireEvent, cleanup, act } from '@testing-library/react'
import { PaneModeButtons } from './PaneModeButtons'
import { HandoffDialogHost } from '../HandoffDialogHost'
import { useTabStore } from '../../stores/useTabStore'
import { useHostStore } from '../../stores/useHostStore'
import { useAgentStore } from '../../stores/useAgentStore'
import { useNexHostStore } from '../../stores/useNexHostStore'
import { useShownHostsStore } from '../../stores/useShownHostsStore'
import { useHandoffDialogStore } from '../../stores/useHandoffDialogStore'
import { registerTakeToTerminal, type TakeToTerminalEntry } from '../../lib/nex/take-to-terminal-registry'
import { compositeKey } from '../../lib/composite-key'
import { findPane } from '../../lib/pane-tree'
import type { ExecutionContent, Pane, PaneContent, PaneLayout, Tab, TmuxSessionContent } from '../../types/tab'

const H = 'h1'
const TAB = 't1'
const PRIMARY = 'primary'
const TARGET = 'target'

const tmux = (code: string, over: Partial<TmuxSessionContent> = {}): TmuxSessionContent => ({
  kind: 'tmux-session', hostId: H, sessionCode: code, mode: 'terminal', cachedName: code, tmuxInstance: 'inst-1', ...over,
})
const exec = (executionId: string, over: Partial<ExecutionContent> = {}): ExecutionContent => ({
  kind: 'execution', executionId, host: H, ...over,
})

/** A tab whose primary pane holds `primary` and whose second pane — the one the buttons are for — holds `target`. */
function seedTab(primary: PaneContent, target: PaneContent): Pane {
  const layout: PaneLayout = {
    type: 'split', id: 's1', direction: 'h',
    children: [
      { type: 'leaf', pane: { id: PRIMARY, content: primary } },
      { type: 'leaf', pane: { id: TARGET, content: target } },
    ],
    sizes: [50, 50],
  }
  const tab: Tab = { id: TAB, pinned: false, locked: false, createdAt: 0, layout }
  useTabStore.setState({ tabs: { [TAB]: tab }, tabOrder: [TAB], activeTabId: TAB, visitHistory: [] })
  return { id: TARGET, content: target }
}

const contentOf = (paneId: string) => findPane(useTabStore.getState().tabs[TAB].layout, paneId)?.content

function seedReady(ready = true) {
  useNexHostStore.setState({
    byHost: {
      [H]: {
        info: null,
        capabilities: { delegate: { resume_session_id: true }, sandbox_profiles: ready ? ['default', 'handoff'] : ['default'] } as never,
        phase: 'ready', error: null, fetchedAt: 0, generation: 1, fingerprint: 'f',
      },
    },
  })
}
const agents = (byCode: Record<string, string>) => useAgentStore.setState({
  agentTypes: Object.fromEntries(Object.entries(byCode).map(([code, type]) => [compositeKey(H, code), type])),
})

const button = (name: 'Terminal' | 'Worker room' | 'Chat') => screen.getByRole('button', { name })
const pressed = () => ['Terminal', 'Worker room', 'Chat'].filter((n) => button(n as never).getAttribute('aria-pressed') === 'true')

const unregister: Array<() => void> = []
function registerTake(paneId: string, entry: Partial<TakeToTerminalEntry> = {}): TakeToTerminalEntry {
  const full: TakeToTerminalEntry = { canTake: true, busy: false, takeToTerminal: vi.fn(), ...entry }
  unregister.push(registerTakeToTerminal(paneId, full))
  return full
}

beforeEach(() => {
  cleanup()
  useNexHostStore.setState({ byHost: {}, ensure: vi.fn().mockResolvedValue(undefined) } as never)
  useAgentStore.setState({ agentTypes: {} })
  useShownHostsStore.setState({ ids: [H] })
  useHandoffDialogStore.setState({ target: null })
})

afterEach(() => {
  while (unregister.length) unregister.pop()!()
})

describe('PaneModeButtons — a tmux-session target', () => {
  // The primary pane is a Claude Code terminal too: a candidate in its own right.
  const primary = tmux('prim01')

  it('terminal is pressed; worker and chat are not', () => {
    seedReady()
    agents({ prim01: 'cc', targ01: 'cc' })
    render(<PaneModeButtons tabId={TAB} pane={seedTab(primary, tmux('targ01'))} />)
    expect(pressed()).toEqual(['Terminal'])
    expect(button('Worker room').getAttribute('aria-pressed')).toBe('false')
    expect(button('Chat').getAttribute('aria-pressed')).toBe('false')
  })

  it('worker on a candidate opens the Hand to Nex dialog for the target pane, with no mode (→ room)', () => {
    seedReady()
    agents({ prim01: 'cc', targ01: 'cc' })
    const target = tmux('targ01')
    render(<PaneModeButtons tabId={TAB} pane={seedTab(primary, target)} />)
    expect(button('Worker room')).toBeEnabled()
    fireEvent.click(button('Worker room'))
    const opened = useHandoffDialogStore.getState().target
    expect(opened).toEqual({ tabId: TAB, paneId: TARGET, content: target })
    expect(opened && 'mode' in opened).toBe(false)
  })

  it('chat on a candidate opens the same dialog with mode chat', () => {
    seedReady()
    agents({ prim01: 'cc', targ01: 'cc' })
    const target = tmux('targ01')
    render(<PaneModeButtons tabId={TAB} pane={seedTab(primary, target)} />)
    fireEvent.click(button('Chat'))
    expect(useHandoffDialogStore.getState().target).toEqual({ tabId: TAB, paneId: TARGET, content: target, mode: 'chat' })
  })

  it.each([
    ['not running Claude Code', () => { seedReady(); agents({ prim01: 'cc', targ01: 'codex' }) }, tmux('targ01'), 'This terminal is not running Claude Code'],
    ['no agent at all', () => { seedReady(); agents({ prim01: 'cc' }) }, tmux('targ01'), 'This terminal is not running Claude Code'],
    ['Nexen not ready', () => { seedReady(false); agents({ prim01: 'cc', targ01: 'cc' }) }, tmux('targ01'), 'Nexen is not ready on this host'],
    ['a terminated session', () => { seedReady(); agents({ prim01: 'cc', targ01: 'cc' }) }, tmux('targ01', { terminated: 'session-closed' }), "This terminal's session is gone"],
    ['a host hidden in this workbench', () => { seedReady(); agents({ prim01: 'cc', targ01: 'cc' }); useShownHostsStore.setState({ ids: [] }) }, tmux('targ01'), 'This host is turned off in this workbench'],
  ])('%s → worker and chat are disabled, titled with why', (_label, arrange, target, why) => {
    arrange()
    render(<PaneModeButtons tabId={TAB} pane={seedTab(primary, target)} />)
    for (const name of ['Worker room', 'Chat'] as const) {
      expect(button(name), name).toBeDisabled()
      expect(button(name).getAttribute('title'), name).toBe(why)
      fireEvent.click(button(name))
    }
    expect(useHandoffDialogStore.getState().target).toBeNull()
    // The pressed terminal button is not disabled: it is the current mode, not an unavailable one.
    expect(button('Terminal')).toBeEnabled()
  })

  it('the gate follows the stores live', () => {
    seedReady()
    render(<PaneModeButtons tabId={TAB} pane={seedTab(primary, tmux('targ01'))} />)
    expect(button('Worker room')).toBeDisabled()
    act(() => agents({ targ01: 'cc' }))
    expect(button('Worker room')).toBeEnabled()
  })

  it('clicking the pressed terminal button does nothing', () => {
    seedReady()
    agents({ prim01: 'cc', targ01: 'cc' })
    const target = tmux('targ01')
    render(<PaneModeButtons tabId={TAB} pane={seedTab(primary, target)} />)
    const before = useTabStore.getState().tabs
    fireEvent.click(button('Terminal'))
    expect(useHandoffDialogStore.getState().target).toBeNull()
    expect(useTabStore.getState().tabs).toBe(before)
  })
})

describe('PaneModeButtons — an execution target in room', () => {
  // The primary pane is a worker as well, in room: a write aimed at it would be visible.
  const primary = exec('exc_primary')

  it('worker is pressed; terminal and chat are not', () => {
    render(<PaneModeButtons tabId={TAB} pane={seedTab(primary, exec('exc_target'))} />)
    expect(pressed()).toEqual(['Worker room'])
  })

  it('an absent mode, or one this build does not know, reads as room', () => {
    render(<PaneModeButtons tabId={TAB} pane={seedTab(primary, exec('exc_target', { mode: 'galaxy' as never }))} />)
    expect(pressed()).toEqual(['Worker room'])
  })

  it('chat switches the target pane to chat, and leaves the primary alone', () => {
    render(<PaneModeButtons tabId={TAB} pane={seedTab(primary, exec('exc_target'))} />)
    fireEvent.click(button('Chat'))
    expect(contentOf(TARGET)).toEqual(exec('exc_target', { mode: 'chat' }))
    expect(contentOf(PRIMARY)).toEqual(primary)
  })

  // P6 review A2: the switch is guarded by host + execution id (the execution store's key), not the id alone.
  it('the target pane now shows the same execution id on another host → chat does not write it', () => {
    render(<PaneModeButtons tabId={TAB} pane={seedTab(primary, exec('exc_target'))} />)
    const swapped = exec('exc_target', { host: 'h2' })
    useTabStore.getState().setPaneContent(TAB, TARGET, swapped)
    fireEvent.click(button('Chat'))
    expect(contentOf(TARGET)).toEqual(swapped)
  })

  it('a target with no host hint switches too (it resolves to the first host, as the pane does)', () => {
    const prev = useHostStore.getState().hostOrder
    useHostStore.setState({ hostOrder: [H] })
    try {
      render(<PaneModeButtons tabId={TAB} pane={seedTab(primary, exec('exc_target', { host: undefined }))} />)
      fireEvent.click(button('Chat'))
      expect(contentOf(TARGET)).toEqual({ kind: 'execution', executionId: 'exc_target', host: undefined, mode: 'chat' })
    } finally {
      useHostStore.setState({ hostOrder: prev })
    }
  })

  it('clicking the pressed worker button does nothing', () => {
    render(<PaneModeButtons tabId={TAB} pane={seedTab(primary, exec('exc_target'))} />)
    const before = useTabStore.getState().tabs
    fireEvent.click(button('Worker room'))
    expect(useTabStore.getState().tabs).toBe(before)
    expect(useHandoffDialogStore.getState().target).toBeNull()
  })

  it('terminal runs the target pane’s registered Take to terminal', () => {
    const forPrimary = registerTake(PRIMARY)
    const forTarget = registerTake(TARGET)
    render(<PaneModeButtons tabId={TAB} pane={seedTab(primary, exec('exc_target'))} />)
    expect(button('Terminal')).toBeEnabled()
    fireEvent.click(button('Terminal'))
    expect(forTarget.takeToTerminal).toHaveBeenCalledTimes(1)
    expect(forPrimary.takeToTerminal).not.toHaveBeenCalled()
  })

  it('no view registered for the target pane → terminal is disabled, even when the primary has one', () => {
    registerTake(PRIMARY)
    render(<PaneModeButtons tabId={TAB} pane={seedTab(primary, exec('exc_target'))} />)
    expect(button('Terminal')).toBeDisabled()
    expect(button('Terminal').getAttribute('title')).toBe("This worker can't be taken back to a terminal")
  })

  it('a view that does not offer Take to terminal → disabled, titled cannot_take', () => {
    const entry = registerTake(TARGET, { canTake: false })
    render(<PaneModeButtons tabId={TAB} pane={seedTab(primary, exec('exc_target'))} />)
    expect(button('Terminal')).toBeDisabled()
    expect(button('Terminal').getAttribute('title')).toBe("This worker can't be taken back to a terminal")
    fireEvent.click(button('Terminal'))
    expect(entry.takeToTerminal).not.toHaveBeenCalled()
  })

  it('a busy view → disabled, titled busy; enabled again once the view is idle (live)', () => {
    const busy = registerTake(TARGET, { busy: true })
    render(<PaneModeButtons tabId={TAB} pane={seedTab(primary, exec('exc_target'))} />)
    expect(button('Terminal')).toBeDisabled()
    expect(button('Terminal').getAttribute('title')).toBe('Busy — try again in a moment')
    fireEvent.click(button('Terminal'))
    expect(busy.takeToTerminal).not.toHaveBeenCalled()
    let idle: TakeToTerminalEntry | undefined
    act(() => { idle = registerTake(TARGET) })
    expect(button('Terminal')).toBeEnabled()
    fireEvent.click(button('Terminal'))
    expect(idle!.takeToTerminal).toHaveBeenCalledTimes(1)
  })
})

describe('PaneModeButtons — an execution target in chat', () => {
  const primary = exec('exc_primary', { mode: 'chat' })

  it('chat is pressed; terminal and worker are not', () => {
    render(<PaneModeButtons tabId={TAB} pane={seedTab(primary, exec('exc_target', { mode: 'chat' }))} />)
    expect(pressed()).toEqual(['Chat'])
  })

  it('worker switches the target pane back to room, and leaves the primary alone', () => {
    render(<PaneModeButtons tabId={TAB} pane={seedTab(primary, exec('exc_target', { mode: 'chat' }))} />)
    fireEvent.click(button('Worker room'))
    expect(contentOf(TARGET)).toEqual(exec('exc_target', { mode: 'room' }))
    expect(contentOf(PRIMARY)).toEqual(primary)
  })

  it('clicking the pressed chat button does nothing', () => {
    render(<PaneModeButtons tabId={TAB} pane={seedTab(primary, exec('exc_target', { mode: 'chat' }))} />)
    const before = useTabStore.getState().tabs
    fireEvent.click(button('Chat'))
    expect(useTabStore.getState().tabs).toBe(before)
  })

  it('terminal behaves as in room: it runs the registered Take to terminal', () => {
    const entry = registerTake(TARGET)
    render(<PaneModeButtons tabId={TAB} pane={seedTab(primary, exec('exc_target', { mode: 'chat' }))} />)
    fireEvent.click(button('Terminal'))
    expect(entry.takeToTerminal).toHaveBeenCalledTimes(1)
  })
})

// Shell polish spec §4 (rule F): a mouse press on a mode button leaves focus on the pane. jsdom does not focus on
// mousedown, so `fireEvent.mouseDown(...) === false` proves the wiring. Pressed and action buttons are probed; a disabled
// one never sees the press (React drops mouse events on a disabled button, and a browser does not focus one).
describe('PaneModeButtons — a mouse press keeps focus where it was', () => {
  const ALL = ['Terminal', 'Worker room', 'Chat'] as const

  it('tmux target: every button prevents the mousedown default and stays in the tab order; a click still opens the dialog', () => {
    seedReady()
    agents({ prim01: 'cc', targ01: 'cc' })
    const target = tmux('targ01')
    render(<PaneModeButtons tabId={TAB} pane={seedTab(tmux('prim01'), target)} />)
    for (const name of ALL) {
      expect(fireEvent.mouseDown(button(name)), name).toBe(false)
      expect(button(name).tabIndex, name).toBeGreaterThanOrEqual(0)
    }
    fireEvent.click(button('Worker room'))
    expect(useHandoffDialogStore.getState().target).toEqual({ tabId: TAB, paneId: TARGET, content: target })
  })

  it('execution target: every button prevents the mousedown default; a press then a click still runs each action', () => {
    const take = registerTake(TARGET)
    render(<PaneModeButtons tabId={TAB} pane={seedTab(exec('exc_primary'), exec('exc_target'))} />)
    for (const name of ALL) {
      expect(fireEvent.mouseDown(button(name)), name).toBe(false)
      expect(button(name).tabIndex, name).toBeGreaterThanOrEqual(0)
    }
    fireEvent.mouseDown(button('Terminal'))
    fireEvent.click(button('Terminal'))
    expect(take.takeToTerminal).toHaveBeenCalledTimes(1)
    fireEvent.mouseDown(button('Chat'))
    fireEvent.click(button('Chat'))
    expect(contentOf(TARGET)).toEqual(exec('exc_target', { mode: 'chat' }))
  })

  // Shell polish spec §4: the press leaves focus on the pane, so the Hand to Nex confirm (the app-level
  // `HandoffDialogHost`, opened through the store) takes it itself — onto its panel, not a button, so Enter does not
  // reach the pane's input — and gives it back to the pane when it closes. A textarea stands in for the pane.
  it.each([
    ['Cancel', () => fireEvent.click(screen.getByTestId('handoff-cancel'))],
    ['Escape', () => fireEvent.keyDown(document.activeElement!, { key: 'Escape' })],
  ] as const)('worker on a candidate: the dialog panel takes focus from the pane; %s gives it back', (_name, close) => {
    seedReady()
    agents({ prim01: 'cc', targ01: 'cc' })
    render(
      <>
        <textarea data-testid="pane" />
        <PaneModeButtons tabId={TAB} pane={seedTab(tmux('prim01'), tmux('targ01'))} />
        <HandoffDialogHost />
      </>,
    )
    const pane = screen.getByTestId('pane')
    pane.focus()
    expect(fireEvent.mouseDown(button('Worker room'))).toBe(false)
    fireEvent.click(button('Worker room'))
    expect(document.activeElement).toBe(screen.getByTestId('handoff-panel'))
    close()
    expect(screen.queryByTestId('handoff-dialog')).toBeNull()
    expect(document.activeElement).toBe(pane)
  })
})

describe('PaneModeButtons — layout and other kinds', () => {
  it('the group drops below 500 px, like the split buttons it replaces', () => {
    render(<PaneModeButtons tabId={TAB} pane={seedTab(exec('exc_primary'), exec('exc_target'))} />)
    expect(screen.getByTestId('status-mode-buttons').className).toContain('max-[500px]:hidden')
    expect(screen.getByRole('group', { name: 'Pane mode' })).toBe(screen.getByTestId('status-mode-buttons'))
  })

  it('every button keeps its label as its accessible name; an enabled one is titled with it', () => {
    registerTake(TARGET)
    render(<PaneModeButtons tabId={TAB} pane={seedTab(exec('exc_primary'), exec('exc_target'))} />)
    for (const name of ['Terminal', 'Worker room', 'Chat'] as const) {
      expect(button(name).getAttribute('title'), name).toBe(name)
    }
  })

  it.each([
    ['an editor', { kind: 'editor', source: { type: 'inapp' }, filePath: '/a.md' } as PaneContent],
    ['a dashboard', { kind: 'dashboard' } as PaneContent],
  ])('renders nothing for %s', (_label, content) => {
    const { container } = render(<PaneModeButtons tabId={TAB} pane={seedTab(exec('exc_primary'), content)} />)
    expect(container).toBeEmptyDOMElement()
  })
})
