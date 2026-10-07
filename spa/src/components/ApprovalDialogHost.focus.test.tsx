// spa/src/components/ApprovalDialogHost.focus.test.tsx — the dialog keeps the keyboard while it is open (lead-team plan
// v3 P9b-1 review). A decision made here switches to the requester's tab (U22, approval-goto.ts), and a notification
// click activates a tab too; a terminal takes focus in the NEXT animation frame (`useActivationFocus(…, { raf: true })`,
// TerminalView.tsx), after the next request's dialog already focused its panel. Focus that lands outside the panel is
// pulled back while a dialog is open — and only then: with the last request closed, the terminal keeps it.
//
// The real dialog, the real switch and the real activation hook; the terminal is a stand-in that focuses a textarea
// outside the dialog exactly as TerminalView does. Only the daemon call and `requestAnimationFrame` are faked.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { useRef } from 'react'
import { render, screen, fireEvent, act } from '@testing-library/react'
import { ApprovalDialogHost } from './ApprovalDialogHost'
import { useApprovalStore } from '../stores/useApprovalStore'
import { useHostStore } from '../stores/useHostStore'
import { useI18nStore } from '../stores/useI18nStore'
import { useUndoToast } from '../stores/useUndoToast'
import { useTabStore } from '../stores/useTabStore'
import { useWorkspaceStore } from '../stores/useWorkspaceStore'
import { useSessionStore } from '../stores/useSessionStore'
import { useShownHostsStore } from '../stores/useShownHostsStore'
import { usePaneFocusStore } from '../stores/usePaneFocusStore'
import { PaneIdentityContext, useActivationFocus } from '../hooks/useActivationFocus'
import { handleNotificationClick } from '../hooks/useNotificationDispatcher'
import { getPrimaryPane } from '../lib/pane-tree'
import { decideApproval } from '../lib/team/approval-api'
import { __resetClientDescriptorForTests } from '../lib/team/client-label'
import type { Approval } from '../lib/team/types'
import type { Tab } from '../types/tab'

vi.mock('../lib/team/approval-api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../lib/team/approval-api')>()),
  decideApproval: vi.fn(),
}))
const mockedDecide = vi.mocked(decideApproval)

const H = 'h1'
const request = (id: string, name: string, tmux: string, createdAt: number): Approval => ({
  id, kind: 'lead', host_id: 'd1',
  origin: { session_id: `S-${id}`, ref: '_40iueq', name, pid: 1, proc_start: 'p', cwd: '/w/purdex', tmux },
  payload: { reason: 'r', max_members: 3, roots: ['/w/purdex'] },
  state: 'open', created_at: createdAt, deadline_at: createdAt + 540_000, lease_until: createdAt + 30_000,
})
const A = request('req-a', 'requester-a', 'sess-a:@1.%2', 1_000)
const B = request('req-b', 'requester-b', 'sess-b:@3.%4', 2_000)

/** Every terminal focus the stand-ins ran, by tab id. */
let terminalFocus: string[]

/** TerminalView's focus rule, verbatim: focus the terminal's textarea in the next frame of an activation or request. */
function TerminalStandIn({ tabId }: { tabId: string }) {
  const active = useTabStore((s) => s.activeTabId === tabId)
  const ref = useRef<HTMLTextAreaElement>(null)
  useActivationFocus(active, true, () => {
    terminalFocus.push(tabId)
    ref.current?.focus()
  }, { raf: true })
  return <textarea data-testid={`terminal-${tabId}`} ref={ref} />
}

/** One stand-in per tmux tab, in its pane's identity (the request path reads it), outside the dialog. */
function Terminals() {
  const tabs = useTabStore((s) => s.tabs)
  const order = useTabStore((s) => s.tabOrder)
  return (
    <>
      {order.map((id) => {
        const pane = getPrimaryPane(tabs[id].layout)
        if (pane.content.kind !== 'tmux-session') return null
        return (
          <PaneIdentityContext.Provider key={id} value={{ tabId: id, paneId: pane.id }}>
            <TerminalStandIn tabId={id} />
          </PaneIdentityContext.Provider>
        )
      })}
    </>
  )
}

const tmuxTab = (id: string, sessionCode: string): Tab => ({
  id, pinned: false, locked: false, createdAt: 0,
  layout: { type: 'leaf', pane: { id: `p-${id}`, content: { kind: 'tmux-session', hostId: H, sessionCode, mode: 'terminal', cachedName: '', tmuxInstance: '' } } },
})
const blankTab: Tab = { id: 'tO', pinned: false, locked: false, createdAt: 0, layout: { type: 'leaf', pane: { id: 'p-tO', content: { kind: 'new-tab' } } } }

let frames: FrameRequestCallback[]
const runFrames = () => act(() => { frames.splice(0).forEach((cb) => cb(0)) })
const settle = () => act(async () => { await new Promise<void>((r) => setTimeout(r, 0)) })
const open = (...list: Approval[]) => act(() => { for (const a of list) useApprovalStore.getState().applyOpened(H, a) })
const dialog = () => screen.queryByTestId('approval-dialog')

/** Click 核准 on the current dialog; the daemon answers 200 with `a` approved. */
async function approve(a: Approval) {
  mockedDecide.mockResolvedValueOnce({ ...a, state: 'approved', decided_by: { kind: 'app', label: 'Purdex.app' }, decided_at: 5 })
  fireEvent.click(screen.getByTestId('approval-approve'))
  await settle()
}

beforeEach(() => {
  terminalFocus = []
  frames = []
  vi.spyOn(window, 'requestAnimationFrame').mockImplementation((cb) => {
    frames.push(cb)
    return frames.length
  })
  vi.spyOn(window, 'cancelAnimationFrame').mockImplementation((id) => {
    frames[id - 1] = () => {}
  })
  useI18nStore.getState().setLocale('zh-TW')
  useApprovalStore.getState().reset()
  useHostStore.setState({
    hosts: { [H]: { id: H, name: 'mlab', ip: '1.2.3.4', port: 7860, order: 0 } },
    hostOrder: [H],
    runtime: { [H]: { status: 'connected' } },
    activeHostId: H,
  })
  useShownHostsStore.setState({ ids: [H] })
  useSessionStore.setState({
    sessions: { [H]: [{ code: 'ca', name: 'sess-a', cwd: '/w', mode: 'terminal' }, { code: 'cb', name: 'sess-b', cwd: '/w', mode: 'terminal' }] },
    activeHostId: null, activeCode: null,
  })
  useWorkspaceStore.getState().reset()
  usePaneFocusStore.setState({ recent: {}, focusRequest: null })
  useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null, visitHistory: [] })
  useUndoToast.setState({ toast: null, notice: null })
  mockedDecide.mockReset()
  __resetClientDescriptorForTests()
  Object.defineProperty(window, 'electronAPI', { value: undefined, writable: true, configurable: true })
})
afterEach(() => {
  vi.restoreAllMocks()
  useHostStore.getState().reset()
})

describe('ApprovalDialogHost keeps the keyboard while open (U22 switch, notification click)', () => {
  it('A and B open, A approved here, A\'s tab already open: B\'s dialog keeps focus after the terminal\'s frame', async () => {
    useTabStore.setState({ tabs: { tA: tmuxTab('tA', 'ca'), tO: blankTab }, tabOrder: ['tA', 'tO'], activeTabId: 'tO' })
    render(<><Terminals /><ApprovalDialogHost /></>)
    open(A, B)
    expect(screen.getByTestId('approval-session').textContent).toBe('requester-a')

    await approve(A)
    expect(screen.getByTestId('approval-session').textContent).toBe('requester-b')
    expect(useTabStore.getState().activeTabId).toBe('tA')
    runFrames()

    expect(terminalFocus).toEqual(['tA']) // the terminal did try, behind the dialog
    expect(document.activeElement).toBe(screen.getByTestId('approval-panel'))
    // Focus moving INSIDE the panel is left alone (no tug of war with the guard).
    screen.getByTestId('approval-roots').focus()
    expect(document.activeElement).toBe(screen.getByTestId('approval-roots'))
  })

  it('A and B open, A approved here, no tab for A\'s session: the new tab\'s terminal does not take B\'s focus', async () => {
    useTabStore.setState({ tabs: { tO: blankTab }, tabOrder: ['tO'], activeTabId: 'tO' })
    render(<><Terminals /><ApprovalDialogHost /></>)
    open(A, B)

    await approve(A)
    const added = useTabStore.getState().activeTabId!
    expect(added).not.toBe('tO')
    expect(screen.getByTestId('approval-session').textContent).toBe('requester-b')
    runFrames()

    expect(terminalFocus).toEqual([added])
    expect(dialog()!.contains(document.activeElement)).toBe(true)
  })

  it('a notification click while a dialog is open: the activated terminal does not take its focus', () => {
    useTabStore.setState({ tabs: { tA: tmuxTab('tA', 'ca'), tO: blankTab }, tabOrder: ['tA', 'tO'], activeTabId: 'tO' })
    render(<><Terminals /><ApprovalDialogHost /></>)
    open(B)

    act(() => handleNotificationClick({ kind: 'open-session', hostId: H, sessionCode: 'ca' }))
    expect(useTabStore.getState().activeTabId).toBe('tA')
    runFrames()

    expect(terminalFocus).toEqual(['tA'])
    expect(document.activeElement).toBe(screen.getByTestId('approval-panel'))
  })

  it('only A open, A approved here: the dialog closes and the requester\'s terminal keeps the focus (no guard left behind)', async () => {
    useTabStore.setState({ tabs: { tA: tmuxTab('tA', 'ca'), tO: blankTab }, tabOrder: ['tA', 'tO'], activeTabId: 'tO' })
    render(<><Terminals /><ApprovalDialogHost /></>)
    open(A)
    const panel = screen.getByTestId('approval-panel')
    const panelFocus = vi.spyOn(panel, 'focus')

    await approve(A)
    expect(dialog()).toBeNull()
    runFrames()

    expect(terminalFocus).toEqual(['tA'])
    expect(document.activeElement).toBe(screen.getByTestId('terminal-tA'))
    expect(panelFocus).not.toHaveBeenCalled()
  })
})
