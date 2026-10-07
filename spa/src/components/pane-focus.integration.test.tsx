// spa/src/components/pane-focus.integration.test.tsx — rule F end to end (shell cleanup spec §8.3, plan T5.4).
//
// Renders TabContent → PaneLayoutRenderer → the REAL registered renderers (`registerBuiltinModules`):
//   - tmux-session: SessionPaneContent → TerminalView (useTerminal, useTerminalWs and its reveal(), useActivationFocus);
//   - execution: the execution pane wrapper → ExecutionView → WorkerInput;
//   - editor: EditorPane → MonacoWrapper.
// Only what sits below the focus logic is mocked: xterm (one Terminal per pane, each with its own `focus` spy),
// `@monaco-editor/react` (one editor per instance, each with its own `focus` spy), the terminal socket
// (`connectTerminal`), the nex network hooks (subscription, lease, API calls) and the cwd / provenance probes.
// A worker's reply box is a real textarea; its focus is observed through a spy on `HTMLElement.prototype.focus`
// (which calls through), so that spy also catches a DOM focus from any other site.
//
// jsdom does not implement `inert`, so every assertion is on focus CALLS, never on `document.activeElement`.
import { describe, it, expect, beforeEach, afterEach, vi, type Mock, type MockInstance } from 'vitest'
import { render, act, fireEvent, screen, type RenderResult } from '@testing-library/react'
import { TabContent } from './TabContent'
import { usePaneFocusStore } from '../stores/usePaneFocusStore'
import { useTabStore } from '../stores/useTabStore'
import { useHostStore } from '../stores/useHostStore'
import { useShownHostsStore } from '../stores/useShownHostsStore'
import { useUISettingsStore } from '../stores/useUISettingsStore'
import { useNexHostStore } from '../stores/useNexHostStore'
import { useAgentStore } from '../stores/useAgentStore'
import { useExecutionStore } from '../stores/useExecutionStore'
import { useHostConfigStore } from '../stores/useHostConfigStore'
import { useEditorStore } from '../stores/useEditorStore'
import { bufferKey } from '../lib/editor-buffer-key'
import { handleNotificationClick } from '../hooks/useNotificationDispatcher'
import {
  clearAllBuiltinModuleRegistries,
  resetAndRegisterBuiltinModules,
  resetModuleEnabledStore,
} from '../lib/__tests__/test-bootstrap-harness'
import type { PaneContent, PaneLayout, Tab } from '../types/tab'

// --- mocked leaves -----------------------------------------------------------------------------------------------

interface TermEntry { el: HTMLElement | null; focus: Mock }
interface SocketEntry { url: string; onData: (data: ArrayBuffer) => void }
interface MonacoEntry { path: string; focus: Mock }

const leaves = vi.hoisted(() => ({
  terms: [] as TermEntry[],
  sockets: [] as SocketEntry[],
  editors: [] as MonacoEntry[],
}))

vi.mock('@xterm/xterm', () => ({
  Terminal: vi.fn(function () {
    const entry: TermEntry = { el: null, focus: vi.fn() }
    leaves.terms.push(entry)
    return {
      loadAddon: vi.fn(),
      open: vi.fn((el: HTMLElement) => { entry.el = el }),
      write: vi.fn(),
      onData: vi.fn(() => ({ dispose: vi.fn() })),
      onResize: vi.fn(() => ({ dispose: vi.fn() })),
      onTitleChange: vi.fn(() => ({ dispose: vi.fn() })),
      registerLinkProvider: vi.fn(() => ({ dispose: vi.fn() })),
      buffer: { active: { getLine: () => ({ translateToString: () => '' }) } },
      dispose: vi.fn(),
      focus: entry.focus,
      unicode: { activeVersion: '6' },
      options: {},
      cols: 80,
      rows: 24,
    }
  }),
}))
vi.mock('@xterm/addon-fit', () => ({ FitAddon: vi.fn(function () { return { fit: vi.fn(), dispose: vi.fn() } }) }))
vi.mock('@xterm/addon-webgl', () => ({
  WebglAddon: vi.fn(function () { return { dispose: vi.fn(), onContextLoss: vi.fn() } }),
}))
vi.mock('@xterm/addon-unicode11', () => ({ Unicode11Addon: vi.fn(function () { return { dispose: vi.fn() } }) }))

vi.mock('../lib/ws', () => ({
  connectTerminal: vi.fn((url: string, onData: (data: ArrayBuffer) => void) => {
    leaves.sockets.push({ url, onData })
    return { send: vi.fn(), resize: vi.fn(), close: vi.fn() }
  }),
}))
vi.mock('../lib/rebuild/cwd-probe', () => ({ probeSessionCwd: vi.fn() }))
vi.mock('../lib/rebuild/provenance-probe', () => ({ probeSessionProvenance: vi.fn() }))

// Like the real editor, `onMount` fires once per instance, from an effect, with a fresh editor object.
vi.mock('@monaco-editor/react', async () => {
  const { useEffect } = await import('react')
  return {
    default: function MonacoEditorMock(props: { path: string; onMount?: (ed: unknown, monaco: unknown) => void }) {
      useEffect(() => {
        const focus = vi.fn()
        leaves.editors.push({ path: props.path, focus })
        props.onMount?.(
          { focus, addAction: vi.fn(), onDidChangeCursorPosition: vi.fn(), restoreViewState: vi.fn(), saveViewState: vi.fn(() => null) },
          { KeyMod: { CtrlCmd: 1 }, KeyCode: { KeyS: 2 } },
        )
        // eslint-disable-next-line react-hooks/exhaustive-deps -- once per instance, like the real editor
      }, [])
      return <div data-testid="monaco-editor" />
    },
    DiffEditor: () => null,
  }
})

vi.mock('../lib/nex/nex-api', async (orig) => ({
  ...(await orig<typeof import('../lib/nex/nex-api')>()),
  sendMessage: vi.fn(), interruptExecution: vi.fn(), terminateExecution: vi.fn(), releaseLease: vi.fn(),
  uploadWorkerFile: vi.fn(), attachControl: vi.fn(), renewLease: vi.fn(),
  fetchExecutionPrelude: vi.fn(() => new Promise(() => {})),
}))
vi.mock('../hooks/useExecutionSubscription', () => ({ useExecutionSubscription: vi.fn(() => ({ problem: null, paused: false })) }))
vi.mock('../hooks/useExecutionLease', () => ({
  useExecutionLease: vi.fn(() => ({ ensureLease: vi.fn(), release: vi.fn(), forget: vi.fn(), touch: vi.fn() })),
}))
vi.mock('../lib/nex/client-id', () => ({ getNexClientId: () => 't-me000000' }))

// --- fixtures ----------------------------------------------------------------------------------------------------

const H = 'h'
const EXEC = 'exc_1'
const EXEC2 = 'exc_2'
const FILE = '/notes/a.txt'

const tmux = (code: string): PaneContent =>
  ({ kind: 'tmux-session', hostId: H, sessionCode: code, mode: 'terminal', cachedName: '', tmuxInstance: 'i1' })
const leaf = (id: string, content: PaneContent): PaneLayout => ({ type: 'leaf', pane: { id, content } })
const split = (id: string, a: PaneLayout, b: PaneLayout): PaneLayout =>
  ({ type: 'split', id, direction: 'h', children: [a, b], sizes: [50, 50] })
const tab = (id: string, layout: PaneLayout): Tab => ({ id, pinned: false, locked: false, createdAt: 0, layout })

/** Tab A: terminals left (the primary pane) and right. */
const TA = tab('tA', split('sA', leaf('left', tmux('c1')), leaf('right', tmux('c2'))))
/** Tab W: a worker left (the primary pane), an editor right. */
const TW = tab('tW', split('sW', leaf('worker', { kind: 'execution', executionId: EXEC, host: H }),
  leaf('editor', { kind: 'editor', source: { type: 'inapp' }, filePath: FILE })))
/** Tab B: somewhere else to switch to. */
const TB = tab('tB', leaf('dash', { kind: 'dashboard' }))
/** Tab WW: two workers (#1840 A1). */
const TWW = tab('tWW', split('sWW', leaf('w1', { kind: 'execution', executionId: EXEC, host: H }),
  leaf('w2', { kind: 'execution', executionId: EXEC2, host: H })))
const ALL = [TA, TW, TB, TWW]

let view: RenderResult
let domFocus: MockInstance<HTMLElement['focus']>

function show(active: Tab): void {
  if (view) view.rerender(<TabContent activeTab={active} allTabs={ALL} />)
  else view = render(<TabContent activeTab={active} allTabs={ALL} />)
}

/** Let effects, timers (reveal delay 0) and animation frames (the activation focus) run. */
async function settle(): Promise<void> {
  for (let i = 0; i < 2; i++) {
    await act(async () => {
      await new Promise((r) => setTimeout(r, 0))
      await new Promise((r) => requestAnimationFrame(() => r(undefined)))
    })
  }
}

/** The mounted terminals of tab A, in layout order (left is first in the document). */
function terms(): { left: TermEntry; right: TermEntry } {
  const live = leaves.terms.filter((t) => t.el?.isConnected)
  expect(live).toHaveLength(2)
  live.sort((a, b) => (a.el!.compareDocumentPosition(b.el!) & Node.DOCUMENT_POSITION_FOLLOWING ? -1 : 1))
  return { left: live[0], right: live[1] }
}

/** Every terminal's first data arrives; reveal() runs after the (zero) reveal delay. */
async function firstData(): Promise<void> {
  for (const s of leaves.sockets.splice(0)) act(() => s.onData(new ArrayBuffer(1)))
  await settle()
}

const workerBox = () => screen.getByTestId('execution-view').querySelector('textarea')!
const monaco = () => {
  const live = leaves.editors.at(-1)
  expect(live).toBeDefined()
  return live!
}
/** DOM focus() calls whose target is `el`. */
const domFocusOn = (el: Element) => domFocus.mock.contexts.filter((c) => c === el).length

function clearFocusCalls(): void {
  domFocus.mockClear()
  for (const t of leaves.terms) t.focus.mockClear()
  for (const e of leaves.editors) e.focus.mockClear()
}

beforeEach(() => {
  resetAndRegisterBuiltinModules()
  leaves.terms.length = 0
  leaves.sockets.length = 0
  leaves.editors.length = 0
  view = undefined as unknown as RenderResult

  useUISettingsStore.setState({ keepAliveCount: 3, terminalRevealDelay: 0, terminalRenderer: 'dom' })
  useTabStore.setState({
    tabs: Object.fromEntries(ALL.map((t) => [t.id, t])),
    tabOrder: ALL.map((t) => t.id),
    activeTabId: null,
    visitHistory: [],
  })
  usePaneFocusStore.setState({ recent: {}, focusRequest: null })
  useHostStore.setState({
    hosts: { [H]: { id: H, name: H, ip: '127.0.0.1', port: 7860, order: 0 } },
    hostOrder: [H],
    activeHostId: H,
    runtime: { [H]: { status: 'connected', attachReady: true } },
  } as never)
  useShownHostsStore.setState({ ids: [H] })
  useAgentStore.setState({ agentTypes: {} })
  useNexHostStore.setState({ byHost: {}, ensure: vi.fn().mockResolvedValue(undefined) } as never)
  useHostConfigStore.setState({ byHost: {}, ensureLoaded: async () => {} })

  useExecutionStore.setState({ executions: {} })
  for (const id of [EXEC, EXEC2]) {
    useExecutionStore.getState().setSummary(H, id, {
      id, state: 'idle', provider: 'claude', principal_id: 'p', cwd: '/Users/w/repo', mount_kind: 'dev', brief: 'b',
      labels: {}, created_at: 0, updated_at: 0, duration_ms: null, event_count: 0, observers: 1, archived: false,
      effective_profile: 'standard', turn_count: 0,
    } as never)
    useExecutionStore.getState().setHistoryLoaded(H, id, true)
  }

  useEditorStore.getState().clearAllBuffers()
  useEditorStore.getState().openBuffer(bufferKey({ type: 'inapp' }, FILE), 'hello', {
    language: 'plaintext', languageSource: 'manual', eol: 'lf', encoding: 'utf8',
  })

  domFocus = vi.spyOn(HTMLElement.prototype, 'focus')
})

afterEach(() => {
  view?.unmount()
  domFocus.mockRestore()
  clearAllBuiltinModuleRegistries()
  resetModuleEnabledStore()
})

// --- 1. keep-alive reactivation -----------------------------------------------------------------------------------

describe('pane focus — keep-alive reactivation (spec §8.3 path 1)', () => {
  it('two terminals: touch the right one, leave, come back → only the right terminal focuses', async () => {
    show(TA)
    await settle()
    await firstData()
    const { left, right } = terms()

    fireEvent.pointerDown(right.el!)
    expect(usePaneFocusStore.getState().recent.tA).toEqual(['right'])

    show(TB)
    await settle()
    expect(terms()).toEqual({ left, right }) // kept alive: the same instances, not remounted
    clearFocusCalls()

    show(TA)
    await settle()
    expect(right.focus).toHaveBeenCalledTimes(1)
    expect(left.focus).not.toHaveBeenCalled()
    expect(domFocus).not.toHaveBeenCalled()
  })

  // P5 review A1: the activation focus waits a frame; a click on the other pane inside that frame moves the target,
  // and the frame must not pull focus back to the pane that was the target at activation.
  it('two terminals, the right one recorded: a pointerdown on the left before the frame → the right does not focus', async () => {
    show(TA)
    await settle()
    await firstData()
    const { left, right } = terms()
    fireEvent.pointerDown(right.el!)

    show(TB)
    await settle()
    clearFocusCalls()

    show(TA) // activation: the right terminal's focus is scheduled for the next frame
    fireEvent.pointerDown(left.el!)
    expect(usePaneFocusStore.getState().recent.tA).toEqual(['left', 'right'])
    await settle()
    expect(right.focus).not.toHaveBeenCalled()
    expect(left.focus).not.toHaveBeenCalled()
  })

  it('two terminals, no record → only the primary (left) terminal focuses', async () => {
    show(TA)
    await settle()
    await firstData()
    const { left, right } = terms()
    expect(usePaneFocusStore.getState().recent.tA).toBeUndefined()

    show(TB)
    await settle()
    clearFocusCalls()

    show(TA)
    await settle()
    expect(left.focus).toHaveBeenCalledTimes(1)
    expect(right.focus).not.toHaveBeenCalled()
  })

  it('worker + editor: the editor recorded → only the editor focuses; the worker\'s reply box does not', async () => {
    show(TW)
    await settle()
    fireEvent.pointerDown(screen.getByTestId('monaco-editor'))
    expect(usePaneFocusStore.getState().recent.tW?.[0]).toBe('editor')

    show(TB)
    await settle()
    clearFocusCalls()

    show(TW)
    await settle()
    expect(monaco().focus).toHaveBeenCalled()
    expect(domFocusOn(workerBox())).toBe(0)
  })

  it('worker + editor, no record → only the primary (the worker\'s reply box) focuses', async () => {
    show(TW)
    await settle()
    show(TB)
    await settle()
    // The mount's own focus of the reply box recorded the worker; drop it to test the no-record branch.
    usePaneFocusStore.setState({ recent: {} })
    clearFocusCalls()

    show(TW)
    await settle()
    expect(domFocusOn(workerBox())).toBe(1)
    expect(monaco().focus).not.toHaveBeenCalled()
  })
})

// --- 2. first mount as the active tab ----------------------------------------------------------------------------

describe('pane focus — first mount as the active tab (spec §8.3 path 2)', () => {
  // keepAliveCount 0: leaving a heavy tab unmounts it, so coming back mounts it fresh, as the active tab.
  beforeEach(() => { useUISettingsStore.setState({ keepAliveCount: 0 }) })

  it('two terminals, the right one recorded → only the right focuses, through the activation hook and reveal()', async () => {
    show(TA)
    await settle()
    fireEvent.pointerDown(terms().right.el!)
    show(TB)
    await settle()
    expect(leaves.terms.filter((t) => t.el?.isConnected)).toHaveLength(0) // tab A is no longer alive
    leaves.terms.length = 0
    leaves.sockets.length = 0

    show(TA)
    await settle()
    const { left, right } = terms()
    // The activation hook (mount with isActive true).
    expect(right.focus).toHaveBeenCalledTimes(1)
    expect(left.focus).not.toHaveBeenCalled()

    // reveal(): the first data reaches both terminals; only the target focuses.
    clearFocusCalls()
    await firstData()
    expect(right.focus).toHaveBeenCalledTimes(1)
    expect(left.focus).not.toHaveBeenCalled()
    expect(domFocus).not.toHaveBeenCalled()
  })

  it('two terminals, no record → only the primary focuses, through the activation hook and reveal()', async () => {
    show(TA)
    await settle()
    const { left, right } = terms()
    expect(left.focus).toHaveBeenCalledTimes(1)
    expect(right.focus).not.toHaveBeenCalled()

    clearFocusCalls()
    await firstData()
    expect(left.focus).toHaveBeenCalledTimes(1)
    expect(right.focus).not.toHaveBeenCalled()
  })

  it('worker + editor, the editor recorded → only the editor focuses (on mount and at activation)', async () => {
    show(TW)
    await settle()
    fireEvent.pointerDown(screen.getByTestId('monaco-editor'))
    show(TB)
    await settle()
    expect(screen.queryByTestId('execution-view')).toBeNull() // tab W is no longer alive
    clearFocusCalls()
    leaves.editors.length = 0

    show(TW)
    await settle()
    expect(monaco().focus).toHaveBeenCalled()
    expect(domFocusOn(workerBox())).toBe(0)
  })

  it('worker + editor, no record → only the worker\'s reply box focuses', async () => {
    show(TW)
    await settle()
    expect(domFocusOn(workerBox())).toBe(1)
    expect(monaco().focus).not.toHaveBeenCalled()
  })

  // P5 review follow-up: the activation finds the reply box disabled while the history loads; its focus waits
  // for the box to be usable, and is dropped if the user moved to another pane meanwhile.
  it('worker + editor, no record, the history still loading → the reply box focuses once, when it has loaded', async () => {
    useExecutionStore.getState().setHistoryLoaded(H, EXEC, false)
    show(TW)
    await settle()
    expect(workerBox()).toHaveAttribute('aria-disabled', 'true')
    expect(domFocusOn(workerBox())).toBe(0)

    act(() => { useExecutionStore.getState().setHistoryLoaded(H, EXEC, true) })
    await settle()
    expect(domFocusOn(workerBox())).toBe(1)
    expect(monaco().focus).not.toHaveBeenCalled()
  })

  it('worker + editor, the history still loading, a pointerdown on the editor before it loads → no focus from any site', async () => {
    useExecutionStore.getState().setHistoryLoaded(H, EXEC, false)
    show(TW)
    await settle()
    fireEvent.pointerDown(screen.getByTestId('monaco-editor'))
    expect(usePaneFocusStore.getState().recent.tW?.[0]).toBe('editor')
    clearFocusCalls()

    act(() => { useExecutionStore.getState().setHistoryLoaded(H, EXEC, true) })
    await settle()
    expect(domFocusOn(workerBox())).toBe(0)
    expect(monaco().focus).not.toHaveBeenCalled()
  })
})

// --- 3. click inside a visible tab -------------------------------------------------------------------------------

describe('pane focus — click inside a visible tab (spec §8.3 path 3)', () => {
  it('two terminals: a pointerdown on either pane moves the record but calls no focus() from any site', async () => {
    show(TA)
    await settle()
    // Reveal first: a terminal whose first data arrives after the click would (rightly) focus the clicked target.
    await firstData()
    const { left, right } = terms()
    clearFocusCalls()

    fireEvent.pointerDown(right.el!)
    await settle()
    expect(usePaneFocusStore.getState().recent.tA).toEqual(['right'])
    fireEvent.pointerDown(left.el!)
    await settle()
    expect(usePaneFocusStore.getState().recent.tA).toEqual(['left', 'right'])

    expect(left.focus).not.toHaveBeenCalled()
    expect(right.focus).not.toHaveBeenCalled()
    expect(domFocus).not.toHaveBeenCalled()
  })

  it('worker + editor: a pointerdown on either pane calls no focus() from any site', async () => {
    show(TW)
    await settle()
    clearFocusCalls()

    fireEvent.pointerDown(screen.getByTestId('monaco-editor'))
    await settle()
    expect(usePaneFocusStore.getState().recent.tW?.[0]).toBe('editor')
    fireEvent.pointerDown(screen.getByTestId('execution-view'))
    await settle()
    expect(usePaneFocusStore.getState().recent.tW?.[0]).toBe('worker')

    expect(monaco().focus).not.toHaveBeenCalled()
    expect(domFocus).not.toHaveBeenCalled()
  })
})

// --- the worker's post-send refocus ------------------------------------------------------------------------------

describe('pane focus — a worker send coming back (spec §8.3)', () => {
  const sending = (v: boolean) => act(() => { useExecutionStore.getState().setPendingSend(H, EXEC, v); useExecutionStore.getState().setSendLocked(H, EXEC, v) })

  it('the worker is not the target (the user moved to the editor) → its reply box does not take focus', async () => {
    show(TW)
    await settle()
    fireEvent.pointerDown(screen.getByTestId('monaco-editor'))
    sending(true)
    expect(workerBox()).toHaveAttribute('aria-disabled', 'true')
    clearFocusCalls()

    sending(false)
    await settle()
    expect(domFocusOn(workerBox())).toBe(0)
    expect(monaco().focus).not.toHaveBeenCalled()
  })

  it('control: the worker is the target → its reply box takes focus back', async () => {
    show(TW)
    await settle()
    fireEvent.pointerDown(screen.getByTestId('execution-view'))
    sending(true)
    clearFocusCalls()

    sending(false)
    await settle()
    expect(domFocusOn(workerBox())).toBe(1)
    expect(monaco().focus).not.toHaveBeenCalled()
  })
})

// --- 4. an explicit request: a notification click (#1840 review A1) ----------------------------------------------

describe('pane focus — a notification click focuses the notified pane (#1840 review A1)', () => {
  /** Like App: the active tab is read from the store, so a click's writes and the tab's activation are one commit. */
  function Shell() {
    const tabs = useTabStore((s) => s.tabs)
    const activeTabId = useTabStore((s) => s.activeTabId)
    return <TabContent activeTab={activeTabId ? tabs[activeTabId] : null} allTabs={ALL} />
  }
  const mountShell = (activeTabId: string) => {
    useTabStore.setState({ activeTabId })
    view = render(<Shell />)
  }
  const click = (sessionCode: string) => act(() => { handleNotificationClick({ kind: 'open-session', hostId: H, sessionCode }) })

  it('two terminals, the tab on screen, the left one in use: a notification for the right → only the right focuses, once', async () => {
    mountShell('tA')
    await settle()
    await firstData()
    const { left, right } = terms()
    fireEvent.pointerDown(left.el!)
    clearFocusCalls()

    click('c2')
    await settle()
    expect(right.focus).toHaveBeenCalledTimes(1)
    expect(left.focus).not.toHaveBeenCalled()

    // Used once: the user goes back to the left; later renders never pull focus to the right again.
    fireEvent.pointerDown(left.el!)
    clearFocusCalls()
    view.rerender(<Shell />)
    await settle()
    expect(right.focus).not.toHaveBeenCalled()
  })

  it('two terminals, another tab on screen: the click shows the tab and only the right terminal focuses, exactly once', async () => {
    mountShell('tA')
    await settle()
    await firstData()
    const { left, right } = terms()
    fireEvent.pointerDown(left.el!)
    act(() => { useTabStore.getState().setActiveTab('tB') })
    await settle()
    clearFocusCalls()

    click('c2')
    expect(useTabStore.getState().activeTabId).toBe('tA')
    await settle()
    expect(right.focus).toHaveBeenCalledTimes(1)
    expect(left.focus).not.toHaveBeenCalled()
  })

  it('two workers, the tab on screen, typing in the first: a notification for the second → its reply box has the focus', async () => {
    mountShell('tWW')
    await settle()
    const [box1, box2] = screen.getAllByTestId('execution-view').map((v) => v.querySelector('textarea')!)
    fireEvent.pointerDown(box1)
    act(() => box1.focus())
    clearFocusCalls()

    click(`exec-${EXEC2}`)
    await settle()
    expect(document.activeElement).toBe(box2)
    expect(domFocusOn(box2)).toBe(1)
    expect(domFocusOn(box1)).toBe(0)

    // Used once: back in the first box, later renders leave the focus there.
    fireEvent.pointerDown(box1)
    act(() => box1.focus())
    clearFocusCalls()
    view.rerender(<Shell />)
    await settle()
    expect(document.activeElement).toBe(box1)
    expect(domFocusOn(box2)).toBe(0)
  })
})
