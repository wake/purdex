// spa/src/components/status/PaneModeButtons.test.tsx — the status bar's view buttons (shell cleanup spec §9.3; U3-1a).
// A session pane has 終端機 · 指揮台 · 聊天 (views, device-local) and a separate Hand-to-worker control; an execution pane
// has 指揮室 · 聊天 and a separate Take-back control. Every case runs in a split whose primary (first) pane is some other
// agent pane, so an action that went to the primary instead of the status target would show.
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
import { useSessionViewStore, selectSessionView } from '../../stores/useSessionViewStore'
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

/** The host's info: Nexen ready (or not) for the handoff, and the daemon serving (or not) the conversation API. */
function seedHost({ nex = true, conversations = true }: { nex?: boolean; conversations?: boolean } = {}) {
  useNexHostStore.setState({
    byHost: {
      [H]: {
        info: null,
        capabilities: { delegate: { resume_session_id: true }, sandbox_profiles: nex ? ['default', 'handoff'] : ['default'] } as never,
        daemonCapabilities: conversations ? ['conversations.v1'] : [],
        phase: 'ready', error: null, fetchedAt: 0, generation: 1, fingerprint: 'f',
      },
    },
  })
}
const agents = (byCode: Record<string, string>) => useAgentStore.setState({
  agentTypes: Object.fromEntries(Object.entries(byCode).map(([code, type]) => [compositeKey(H, code), type])),
})

type Name = 'Terminal' | 'Deck' | 'Worker room' | 'Chat' | 'Hand to worker' | 'Take back to terminal'
const button = (name: Name) => screen.getByRole('button', { name })
const GROUP = ['Terminal', 'Deck', 'Worker room', 'Chat'] as const
const pressed = () => GROUP.filter((n) => {
  const b = screen.queryByRole('button', { name: n })
  return b?.getAttribute('aria-pressed') === 'true'
})
const viewOfTarget = (code: string) => selectSessionView(TAB, TARGET, code)(useSessionViewStore.getState())

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
  useSessionViewStore.setState({ byPane: {} })
})

afterEach(() => {
  while (unregister.length) unregister.pop()!()
})

describe('PaneModeButtons — a tmux-session target', () => {
  // The primary pane is a Claude Code terminal too: a candidate in its own right.
  const primary = tmux('prim01')
  const ready = () => { seedHost(); agents({ prim01: 'cc', targ01: 'cc' }) }

  it('the terminal is pressed; deck and chat are not; there is no worker-room button', () => {
    ready()
    render(<PaneModeButtons tabId={TAB} pane={seedTab(primary, tmux('targ01'))} />)
    expect(pressed()).toEqual(['Terminal'])
    expect(screen.queryByRole('button', { name: 'Worker room' })).toBeNull()
  })

  it('deck and chat switch the view of the target pane only, and the pressed one follows', () => {
    ready()
    render(<PaneModeButtons tabId={TAB} pane={seedTab(primary, tmux('targ01'))} />)
    fireEvent.click(button('Deck'))
    expect(viewOfTarget('targ01')).toBe('deck')
    expect(pressed()).toEqual(['Deck'])
    fireEvent.click(button('Chat'))
    expect(viewOfTarget('targ01')).toBe('chat')
    expect(pressed()).toEqual(['Chat'])
    fireEvent.click(button('Terminal'))
    expect(viewOfTarget('targ01')).toBe('terminal')
    expect(pressed()).toEqual(['Terminal'])
    expect(selectSessionView(TAB, PRIMARY, 'prim01')(useSessionViewStore.getState())).toBe('terminal')
  })

  // The view is a way of looking, not a handoff: it must not touch the dialog or the pane's content.
  it('a view button never opens the handoff dialog or changes the tab', () => {
    ready()
    render(<PaneModeButtons tabId={TAB} pane={seedTab(primary, tmux('targ01'))} />)
    const before = useTabStore.getState().tabs
    fireEvent.click(button('Deck'))
    fireEvent.click(button('Chat'))
    expect(useHandoffDialogStore.getState().target).toBeNull()
    expect(useTabStore.getState().tabs).toBe(before)
  })

  it('the handoff is its own button: it opens the dialog for the target pane with no mode (→ room)', () => {
    ready()
    const target = tmux('targ01')
    render(<PaneModeButtons tabId={TAB} pane={seedTab(primary, target)} />)
    expect(button('Hand to worker')).toBeEnabled()
    expect(button('Hand to worker').getAttribute('aria-pressed')).toBeNull()
    fireEvent.click(button('Hand to worker'))
    const opened = useHandoffDialogStore.getState().target
    expect(opened).toEqual({ tabId: TAB, paneId: TARGET, content: target })
    expect(opened && 'mode' in opened).toBe(false)
    expect(viewOfTarget('targ01')).toBe('terminal')
  })

  it.each([
    ['not running Claude Code', () => { seedHost(); agents({ prim01: 'cc', targ01: 'codex' }) }, tmux('targ01'),
      'This terminal is not running Claude Code', 'This terminal is not running Claude Code'],
    ['no agent at all', () => { seedHost(); agents({ prim01: 'cc' }) }, tmux('targ01'),
      'This terminal is not running Claude Code', 'This terminal is not running Claude Code'],
    ['a host without the conversation API', () => { seedHost({ conversations: false }); agents({ prim01: 'cc', targ01: 'cc' }) }, tmux('targ01'),
      'This host does not offer the conversation view', null],
    ['Nexen not ready', () => { seedHost({ nex: false }); agents({ prim01: 'cc', targ01: 'cc' }) }, tmux('targ01'),
      null, 'Nexen is not ready on this host'],
    ['a terminated session', () => { seedHost(); agents({ prim01: 'cc', targ01: 'cc' }) }, tmux('targ01', { terminated: 'session-closed' }),
      "This terminal's session is gone", "This terminal's session is gone"],
    ['a host hidden in this workbench', () => { seedHost(); agents({ prim01: 'cc', targ01: 'cc' }); useShownHostsStore.setState({ ids: [] }) }, tmux('targ01'),
      'This host is turned off in this workbench', 'This host is turned off in this workbench'],
  ])('%s → each blocked button is disabled, titled with its own reason', (_label, arrange, target, viewWhy, handoffWhy) => {
    arrange()
    render(<PaneModeButtons tabId={TAB} pane={seedTab(primary, target)} />)
    for (const name of ['Deck', 'Chat'] as const) {
      if (viewWhy) {
        expect(button(name), name).toBeDisabled()
        expect(button(name).getAttribute('title'), name).toBe(viewWhy)
      } else {
        expect(button(name), name).toBeEnabled()
      }
      fireEvent.click(button(name))
    }
    if (handoffWhy) {
      expect(button('Hand to worker')).toBeDisabled()
      expect(button('Hand to worker').getAttribute('title')).toBe(handoffWhy)
    } else {
      expect(button('Hand to worker')).toBeEnabled()
    }
    fireEvent.click(button('Hand to worker'))
    expect(useHandoffDialogStore.getState().target === null).toBe(handoffWhy !== null)
    if (viewWhy) expect(viewOfTarget('targ01')).toBe('terminal')
    // The pressed terminal button is not disabled: it is the current mode, not an unavailable one.
    expect(button('Terminal')).toBeEnabled()
  })

  it('the gates follow the stores live', () => {
    seedHost()
    render(<PaneModeButtons tabId={TAB} pane={seedTab(primary, tmux('targ01'))} />)
    expect(button('Deck')).toBeDisabled()
    expect(button('Hand to worker')).toBeDisabled()
    act(() => agents({ targ01: 'cc' }))
    expect(button('Deck')).toBeEnabled()
    expect(button('Hand to worker')).toBeEnabled()
  })

  it('clicking the pressed terminal button does nothing', () => {
    ready()
    render(<PaneModeButtons tabId={TAB} pane={seedTab(primary, tmux('targ01'))} />)
    const before = useTabStore.getState().tabs
    fireEvent.click(button('Terminal'))
    expect(useHandoffDialogStore.getState().target).toBeNull()
    expect(useTabStore.getState().tabs).toBe(before)
    expect(useSessionViewStore.getState().byPane).toEqual({})
  })

  // The pressed view stays reachable when its gate closes (the agent exited): it is a record of what the pane shows,
  // and the terminal button takes the user back; nothing switches by itself (D11).
  it('a deck chosen earlier stays pressed after the agent goes away, and the terminal button still works', () => {
    ready()
    render(<PaneModeButtons tabId={TAB} pane={seedTab(primary, tmux('targ01'))} />)
    fireEvent.click(button('Deck'))
    act(() => agents({ prim01: 'cc' }))
    expect(pressed()).toEqual(['Deck'])
    expect(viewOfTarget('targ01')).toBe('deck')
    fireEvent.click(button('Terminal'))
    expect(viewOfTarget('targ01')).toBe('terminal')
  })

  it('a pane rebound to another session starts at the terminal again', () => {
    ready()
    useSessionViewStore.getState().setView(TAB, TARGET, 'old001', 'chat')
    render(<PaneModeButtons tabId={TAB} pane={seedTab(primary, tmux('targ01'))} />)
    expect(pressed()).toEqual(['Terminal'])
  })
})

describe('PaneModeButtons — an execution target in room', () => {
  // The primary pane is a worker as well, in room: a write aimed at it would be visible.
  const primary = exec('exc_primary')

  it('room is pressed; chat is not; there is no terminal or deck button in the group', () => {
    render(<PaneModeButtons tabId={TAB} pane={seedTab(primary, exec('exc_target'))} />)
    expect(pressed()).toEqual(['Worker room'])
    expect(screen.queryByRole('button', { name: 'Terminal' })).toBeNull()
    expect(screen.queryByRole('button', { name: 'Deck' })).toBeNull()
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

  it('clicking the pressed room button does nothing', () => {
    render(<PaneModeButtons tabId={TAB} pane={seedTab(primary, exec('exc_target'))} />)
    const before = useTabStore.getState().tabs
    fireEvent.click(button('Worker room'))
    expect(useTabStore.getState().tabs).toBe(before)
    expect(useHandoffDialogStore.getState().target).toBeNull()
  })

  it('take back runs the target pane’s registered Take to terminal', () => {
    const forPrimary = registerTake(PRIMARY)
    const forTarget = registerTake(TARGET)
    render(<PaneModeButtons tabId={TAB} pane={seedTab(primary, exec('exc_target'))} />)
    expect(button('Take back to terminal')).toBeEnabled()
    expect(button('Take back to terminal').getAttribute('aria-pressed')).toBeNull()
    fireEvent.click(button('Take back to terminal'))
    expect(forTarget.takeToTerminal).toHaveBeenCalledTimes(1)
    expect(forPrimary.takeToTerminal).not.toHaveBeenCalled()
  })

  it('no view registered for the target pane → take back is disabled, even when the primary has one', () => {
    registerTake(PRIMARY)
    render(<PaneModeButtons tabId={TAB} pane={seedTab(primary, exec('exc_target'))} />)
    expect(button('Take back to terminal')).toBeDisabled()
    expect(button('Take back to terminal').getAttribute('title')).toBe("This worker can't be taken back to a terminal")
  })

  it('a view that does not offer Take to terminal → disabled, titled cannot_take', () => {
    const entry = registerTake(TARGET, { canTake: false })
    render(<PaneModeButtons tabId={TAB} pane={seedTab(primary, exec('exc_target'))} />)
    expect(button('Take back to terminal')).toBeDisabled()
    expect(button('Take back to terminal').getAttribute('title')).toBe("This worker can't be taken back to a terminal")
    fireEvent.click(button('Take back to terminal'))
    expect(entry.takeToTerminal).not.toHaveBeenCalled()
  })

  it('a busy view → disabled, titled busy; enabled again once the view is idle (live)', () => {
    const busy = registerTake(TARGET, { busy: true })
    render(<PaneModeButtons tabId={TAB} pane={seedTab(primary, exec('exc_target'))} />)
    expect(button('Take back to terminal')).toBeDisabled()
    expect(button('Take back to terminal').getAttribute('title')).toBe('Busy — try again in a moment')
    fireEvent.click(button('Take back to terminal'))
    expect(busy.takeToTerminal).not.toHaveBeenCalled()
    let idle: TakeToTerminalEntry | undefined
    act(() => { idle = registerTake(TARGET) })
    expect(button('Take back to terminal')).toBeEnabled()
    fireEvent.click(button('Take back to terminal'))
    expect(idle!.takeToTerminal).toHaveBeenCalledTimes(1)
  })
})

describe('PaneModeButtons — an execution target in chat', () => {
  const primary = exec('exc_primary', { mode: 'chat' })

  it('chat is pressed; room is not', () => {
    render(<PaneModeButtons tabId={TAB} pane={seedTab(primary, exec('exc_target', { mode: 'chat' }))} />)
    expect(pressed()).toEqual(['Chat'])
  })

  it('room switches the target pane back to room, and leaves the primary alone', () => {
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

  it('take back behaves as in room: it runs the registered Take to terminal', () => {
    const entry = registerTake(TARGET)
    render(<PaneModeButtons tabId={TAB} pane={seedTab(primary, exec('exc_target', { mode: 'chat' }))} />)
    fireEvent.click(button('Take back to terminal'))
    expect(entry.takeToTerminal).toHaveBeenCalledTimes(1)
  })
})

// Shell polish spec §4 (rule F): a mouse press on a mode button leaves focus on the pane. jsdom does not focus on
// mousedown, so `fireEvent.mouseDown(...) === false` proves the wiring. Pressed and action buttons are probed; a disabled
// one never sees the press (React drops mouse events on a disabled button, and a browser does not focus one).
describe('PaneModeButtons — a mouse press keeps focus where it was', () => {
  it('tmux target: every button prevents the mousedown default and stays in the tab order; a click still works', () => {
    seedHost()
    agents({ prim01: 'cc', targ01: 'cc' })
    const target = tmux('targ01')
    render(<PaneModeButtons tabId={TAB} pane={seedTab(tmux('prim01'), target)} />)
    for (const name of ['Terminal', 'Deck', 'Chat', 'Hand to worker'] as const) {
      expect(fireEvent.mouseDown(button(name)), name).toBe(false)
      expect(button(name).tabIndex, name).toBeGreaterThanOrEqual(0)
    }
    fireEvent.click(button('Hand to worker'))
    expect(useHandoffDialogStore.getState().target).toEqual({ tabId: TAB, paneId: TARGET, content: target })
  })

  it('execution target: every button prevents the mousedown default; a press then a click still runs each action', () => {
    const take = registerTake(TARGET)
    render(<PaneModeButtons tabId={TAB} pane={seedTab(exec('exc_primary'), exec('exc_target'))} />)
    for (const name of ['Worker room', 'Chat', 'Take back to terminal'] as const) {
      expect(fireEvent.mouseDown(button(name)), name).toBe(false)
      expect(button(name).tabIndex, name).toBeGreaterThanOrEqual(0)
    }
    fireEvent.mouseDown(button('Take back to terminal'))
    fireEvent.click(button('Take back to terminal'))
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
  ] as const)('hand to worker on a candidate: the dialog panel takes focus from the pane; %s gives it back', (_name, close) => {
    seedHost()
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
    expect(fireEvent.mouseDown(button('Hand to worker'))).toBe(false)
    fireEvent.click(button('Hand to worker'))
    expect(document.activeElement).toBe(screen.getByTestId('handoff-panel'))
    close()
    expect(screen.queryByTestId('handoff-dialog')).toBeNull()
    expect(document.activeElement).toBe(pane)
  })
})

describe('PaneModeButtons — layout and other kinds', () => {
  it('the group and the separate control drop below 500 px, like the split buttons they replace', () => {
    render(<PaneModeButtons tabId={TAB} pane={seedTab(exec('exc_primary'), exec('exc_target'))} />)
    expect(screen.getByTestId('status-mode-buttons').className).toContain('max-[500px]:hidden')
    expect(screen.getByRole('group', { name: 'Pane mode' })).toBe(screen.getByTestId('status-mode-buttons'))
    expect(button('Take back to terminal').className).toContain('max-[500px]:hidden')
  })

  it('the separate control is outside the group', () => {
    registerTake(TARGET)
    render(<PaneModeButtons tabId={TAB} pane={seedTab(exec('exc_primary'), exec('exc_target'))} />)
    expect(screen.getByTestId('status-mode-buttons').contains(button('Take back to terminal'))).toBe(false)
  })

  it('every button keeps its label as its accessible name; an enabled one is titled with it', () => {
    registerTake(TARGET)
    render(<PaneModeButtons tabId={TAB} pane={seedTab(exec('exc_primary'), exec('exc_target'))} />)
    for (const name of ['Worker room', 'Chat', 'Take back to terminal'] as const) {
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
