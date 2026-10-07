// spa/src/components/ApprovalDialogHost.minimize.test.tsx — 縮小 (lead-team spec U22 (b), plan v3 P9b-2). Minimized, the
// dialog is hidden (still mounted, so the grant edits survive) and a corner pill shows `待核准 N · m:ss`. It is not
// modal then: no Escape swallow, no Tab trap, no focus guard, and the keyboard goes back to where it was before the
// dialog took it. A new request updates N and flashes the pill but never expands it; only a click on the pill does.
// The last close ends the minimize, so the next request opens the dialog. Both kinds, `lead` and `self_relay` (U22 (c)).
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, act } from '@testing-library/react'
import { ApprovalDialogHost } from './ApprovalDialogHost'
import { useApprovalStore } from '../stores/useApprovalStore'
import { useHostStore } from '../stores/useHostStore'
import { useI18nStore } from '../stores/useI18nStore'
import { useUndoToast } from '../stores/useUndoToast'
import { decideApproval } from '../lib/team/approval-api'
import { __resetClientDescriptorForTests } from '../lib/team/client-label'
import type { Approval, ApprovalKind } from '../lib/team/types'

vi.mock('../lib/team/approval-api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../lib/team/approval-api')>()),
  decideApproval: vi.fn(),
}))
const mockedDecide = vi.mocked(decideApproval)

const NOW = 1_000_000
const request = (kind: ApprovalKind, id: string, created: number, deadline: number): Approval => ({
  id, kind, host_id: 'd1',
  origin: { session_id: `S-${id}`, ref: '_40iueq', name: `requester-${id}`, pid: 1, proc_start: 'p', cwd: '/w/purdex', tmux: `s-${id}:@1.%2` },
  payload: kind === 'self_relay'
    ? { op_id: `op-${id}`, used_percentage: 72, window: 1_000_000 }
    : { reason: 'r', max_members: 3, roots: ['/w/purdex'] },
  state: 'open', created_at: created, deadline_at: deadline, lease_until: created + 30_000,
})
const KINDS: ApprovalKind[] = ['lead', 'self_relay']

const dialog = () => screen.queryByTestId('approval-dialog')
const pill = () => screen.queryByTestId('approval-pill')
const terminal = () => screen.getByTestId('terminal')
const open = (hostId: string, a: Approval) => act(() => { useApprovalStore.getState().applyOpened(hostId, a) })
const closeElsewhere = (hostId: string, a: Approval) => act(() => {
  useApprovalStore.getState().applyClosed(hostId, { ...a, state: 'denied', decided_by: { kind: 'app', label: 'Purdex.app @ air26' } })
})
const minimize = () => fireEvent.click(screen.getByTestId('approval-minimize'))
/** The app's panes stand in as one textarea outside the dialog, focused before the request arrives. */
const renderWithTerminal = () => {
  const r = render(<><textarea data-testid="terminal" /><ApprovalDialogHost /></>)
  terminal().focus()
  return r
}

beforeEach(() => {
  vi.useFakeTimers({ now: NOW, toFake: ['Date', 'setInterval', 'clearInterval'] })
  useI18nStore.getState().setLocale('zh-TW')
  useApprovalStore.getState().reset()
  useHostStore.setState({
    hosts: {
      h1: { id: 'h1', name: 'mlab', ip: '1.2.3.4', port: 7860, order: 0 },
      h2: { id: 'h2', name: 'air26', ip: '1.2.3.5', port: 7860, order: 1 },
    },
    hostOrder: ['h1', 'h2'],
    runtime: { h1: { status: 'connected' }, h2: { status: 'connected' } },
    activeHostId: 'h1',
  })
  useUndoToast.setState({ toast: null, notice: null })
  mockedDecide.mockReset()
  __resetClientDescriptorForTests()
  Object.defineProperty(window, 'electronAPI', { value: undefined, writable: true, configurable: true })
})
afterEach(() => {
  vi.useRealTimers()
  useHostStore.getState().reset()
})

describe('ApprovalDialogHost — 縮小 (U22 b)', () => {
  for (const kind of KINDS) {
    it(`縮小 hides the dialog and shows the pill with N across hosts and the nearest m:ss (${kind} current)`, () => {
      renderWithTerminal()
      open('h1', request(kind, 'a', NOW - 5_000, NOW + 500_000))
      open('h2', request('lead', 'b', NOW - 1_000, NOW + 65_000))
      expect(dialog()!.dataset.kind).toBe(kind)
      expect(dialog()!.hidden).toBe(false)
      expect(pill()).toBeNull()
      const button = screen.getByTestId('approval-minimize')
      expect(button.getAttribute('type')).toBe('button')
      expect(button.textContent).toBe('縮小')

      minimize()
      expect(useApprovalStore.getState().minimized).toBe(true)
      expect(dialog()!.hidden).toBe(true) // mounted, hidden
      expect(pill()!.textContent).toBe('待核准 2 · 1:05')
    })
  }

  it('縮小 is never disabled: it works while a decision is queued', () => {
    renderWithTerminal()
    open('h1', request('lead', 'a', NOW, NOW + 500_000))
    act(() => { useHostStore.getState().setRuntime('h1', { status: 'reconnecting' }) })
    fireEvent.click(screen.getByTestId('approval-deny'))
    expect(screen.getByTestId('approval-queued')).toBeInTheDocument()
    expect(screen.getByTestId('approval-minimize')).not.toBeDisabled()
    minimize()
    expect(dialog()!.hidden).toBe(true)
  })

  it('minimized: Tab and Escape keydowns are not default-prevented, and focus returns to the element focused before the dialog', () => {
    renderWithTerminal()
    open('h1', request('lead', 'a', NOW, NOW + 500_000))
    expect(document.activeElement).toBe(screen.getByTestId('approval-panel'))
    // While open, both are taken (the baseline the minimized case is measured against).
    expect(fireEvent.keyDown(document.activeElement!, { key: 'Tab' })).toBe(false)
    expect(fireEvent.keyDown(document.activeElement!, { key: 'Escape' })).toBe(false)

    minimize()
    expect(document.activeElement).toBe(terminal())
    expect(fireEvent.keyDown(terminal(), { key: 'Tab' })).toBe(true)
    expect(fireEvent.keyDown(terminal(), { key: 'Tab', shiftKey: true })).toBe(true)
    expect(fireEvent.keyDown(terminal(), { key: 'Escape' })).toBe(true)
    expect(document.activeElement).toBe(terminal())
  })

  it('minimized with the element focused before gone: the hidden dialog keeps no focus', () => {
    const Tree = ({ withTerminal }: { withTerminal: boolean }) => <>{withTerminal && <textarea data-testid="terminal" />}<ApprovalDialogHost /></>
    const { rerender } = render(<Tree withTerminal />)
    terminal().focus()
    open('h1', request('lead', 'a', NOW, NOW + 500_000))
    rerender(<Tree withTerminal={false} />) // the terminal's pane went away meanwhile; the dialog stays mounted
    expect(screen.queryByTestId('terminal')).toBeNull()
    screen.getByTestId('approval-minimize').focus()
    expect(document.activeElement).toBe(screen.getByTestId('approval-minimize'))
    minimize()
    expect(dialog()!.contains(document.activeElement)).toBe(false)
  })

  it('minimized: a focus moved to a terminal stays there (no focus guard)', () => {
    render(<><textarea data-testid="terminal" /><ApprovalDialogHost /></>)
    open('h1', request('lead', 'a', NOW, NOW + 500_000))
    minimize()
    terminal().focus()
    expect(document.activeElement).toBe(terminal())
  })

  for (const kind of KINDS) {
    it(`a new ${kind} request while minimized updates N, raises data-flash by one, and the dialog stays hidden`, () => {
      renderWithTerminal()
      open('h1', request('lead', 'a', NOW - 5_000, NOW + 500_000))
      minimize()
      expect(pill()!.dataset.flash).toBe('0')

      open('h2', request(kind, 'b', NOW, NOW + 200_000))
      expect(pill()!.textContent).toBe('待核准 2 · 3:20')
      expect(pill()!.dataset.flash).toBe('1')
      expect(dialog()!.hidden).toBe(true)
      expect(useApprovalStore.getState().minimized).toBe(true)
      expect(document.activeElement).toBe(terminal())
    })
  }

  it('a request closed while minimized updates N without a flash; the next one stays hidden and leaves the keyboard alone', () => {
    renderWithTerminal()
    const a = request('lead', 'a', NOW - 5_000, NOW + 60_000)
    open('h1', a)
    open('h2', request('self_relay', 'b', NOW, NOW + 500_000))
    minimize()

    closeElsewhere('h1', a)
    expect(pill()!.textContent).toBe('待核准 1 · 8:20')
    expect(pill()!.dataset.flash).toBe('0')
    // The current request changed: a fresh dialog (its key), mounted hidden, which does not take the keyboard.
    expect(dialog()!.dataset.kind).toBe('self_relay')
    expect(dialog()!.hidden).toBe(true)
    expect(document.activeElement).toBe(terminal())
  })

  for (const kind of KINDS) {
    it(`clicking the pill restores the dialog and focuses its panel (a ${kind} request opened while minimized)`, () => {
      renderWithTerminal()
      const a = request('lead', 'a', NOW - 5_000, NOW + 500_000)
      open('h1', a)
      minimize()
      open('h2', request(kind, 'b', NOW, NOW + 400_000))
      closeElsewhere('h1', a) // b is current now, mounted while minimized

      fireEvent.click(pill()!)
      expect(useApprovalStore.getState().minimized).toBe(false)
      expect(pill()).toBeNull()
      expect(dialog()!.hidden).toBe(false)
      expect(dialog()!.dataset.kind).toBe(kind)
      expect(document.activeElement).toBe(screen.getByTestId('approval-panel'))
      // Modal again: the focus guard is back, Tab is trapped and Escape swallowed.
      terminal().focus()
      expect(document.activeElement).toBe(screen.getByTestId('approval-panel'))
      expect(fireEvent.keyDown(document.activeElement!, { key: 'Tab' })).toBe(false)
      expect(fireEvent.keyDown(document.activeElement!, { key: 'Escape' })).toBe(false)
      // 縮小 again hands the keyboard back to the terminal: the restore recorded it (the pill never took focus).
      minimize()
      expect(document.activeElement).toBe(terminal())
    })
  }

  it('grant edits and the 不再詢問 tick survive minimize and restore', () => {
    renderWithTerminal()
    const a = request('lead', 'a', NOW - 5_000, NOW + 500_000)
    open('h1', a)
    open('h1', request('self_relay', 'b', NOW, NOW + 500_000))
    fireEvent.change(screen.getByTestId('approval-max-members'), { target: { value: '5' } })
    fireEvent.change(screen.getByTestId('approval-roots'), { target: { value: '/w/purdex\n/w/nexen' } })
    minimize()
    fireEvent.click(pill()!)
    expect((screen.getByTestId('approval-max-members') as HTMLInputElement).value).toBe('5')
    expect((screen.getByTestId('approval-roots') as HTMLTextAreaElement).value).toBe('/w/purdex\n/w/nexen')

    // The self_relay request (next after a): its tick survives too.
    closeElsewhere('h1', a)
    fireEvent.click(screen.getByTestId('approval-no-more-asking'))
    minimize()
    fireEvent.click(pill()!)
    expect((screen.getByTestId('approval-no-more-asking') as HTMLInputElement).checked).toBe(true)
  })

  it('the countdown ticks while minimized', () => {
    renderWithTerminal()
    open('h1', request('lead', 'a', NOW, NOW + 125_000))
    minimize()
    expect(pill()!.textContent).toBe('待核准 1 · 2:05')
    act(() => { vi.advanceTimersByTime(60_000) })
    expect(pill()!.textContent).toBe('待核准 1 · 1:05')
  })

  it('the last close removes the pill, and the next request opens the dialog', () => {
    renderWithTerminal()
    const a = request('lead', 'a', NOW, NOW + 500_000)
    open('h1', a)
    minimize()
    closeElsewhere('h1', a)
    expect(pill()).toBeNull()
    expect(dialog()).toBeNull()
    expect(useApprovalStore.getState().minimized).toBe(false)

    open('h2', request('self_relay', 'b', NOW, NOW + 500_000))
    expect(dialog()!.hidden).toBe(false)
    expect(pill()).toBeNull()
    expect(document.activeElement).toBe(screen.getByTestId('approval-panel'))
  })

  it('a fresh store (reload) shows the dialog', () => {
    const { unmount } = renderWithTerminal()
    const a = request('lead', 'a', NOW, NOW + 500_000)
    open('h1', a)
    minimize()
    unmount()

    // A reload: a new renderer, a new store, the daemon's snapshot replays the open set.
    useApprovalStore.setState(useApprovalStore.getInitialState(), true)
    render(<ApprovalDialogHost />)
    act(() => { useApprovalStore.getState().applySnapshot('h1', [a]) })
    expect(dialog()!.hidden).toBe(false)
    expect(pill()).toBeNull()
  })
})
