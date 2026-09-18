// spa/src/components/HandoffConfirmDialog.test.tsx — P-C.3b task 3: the
// confirm step in front of `handToNex`: busy state, one call per confirm,
// success / open-execution / error toasts, retry after an error.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, cleanup, act, waitFor } from '@testing-library/react'
import { HandoffConfirmDialog } from './HandoffConfirmDialog'
import { handToNex } from '../lib/nex/handoff'
import { HandoffApiError } from '../lib/nex/handoff-api'
import { useUndoToast } from '../stores/useUndoToast'
import { useTabStore } from '../stores/useTabStore'
import { createTab } from '../types/tab'
import { getPrimaryPane } from '../lib/pane-tree'

vi.mock('../lib/nex/handoff', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../lib/nex/handoff')>()),
  handToNex: vi.fn(),
}))

const mockedHandToNex = vi.mocked(handToNex)

const args = { hostId: 'h1', sessionCode: 'zk16vd', tmuxInstance: 'inst-1', cachedName: 'purdex', tabId: 't1', paneId: 'p1' }
const ok = { execution_id: 'exc_1', state: 'running', effective_profile: 'handoff', session_id: 'sid-1', cwd: '/w', session_kept: true }

function deferred<T>() {
  let resolve!: (v: T) => void
  let reject!: (e: unknown) => void
  const promise = new Promise<T>((res, rej) => { resolve = res; reject = rej })
  return { promise, resolve, reject }
}

function renderDialog() {
  const onClose = vi.fn()
  render(<HandoffConfirmDialog {...args} onClose={onClose} />)
  return { onClose }
}

const confirmBtn = () => screen.getByTestId('handoff-confirm') as HTMLButtonElement
const cancelBtn = () => screen.getByTestId('handoff-cancel') as HTMLButtonElement
const keepBox = () => screen.getByTestId('handoff-keep-session') as HTMLInputElement
const toast = () => useUndoToast.getState().toast

/** A tab whose primary pane shows the given session (or a different one). */
function sessionTab(hostId: string, sessionCode: string): string {
  const tab = createTab({ kind: 'tmux-session', hostId, sessionCode, mode: 'terminal', cachedName: 'x', tmuxInstance: 'inst-1' })
  useTabStore.getState().addTab(tab)
  return tab.id
}

beforeEach(() => {
  cleanup()
  mockedHandToNex.mockReset()
  useUndoToast.setState({ toast: null })
  useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null })
})
afterEach(() => vi.restoreAllMocks())

describe('HandoffConfirmDialog — rendering', () => {
  it('shows the localized title, body, Cancel and Confirm', () => {
    renderDialog()
    expect(screen.getByTestId('handoff-dialog')).toBeInTheDocument()
    expect(screen.getByText('Hand this session to nex?')).toBeInTheDocument()
    expect(screen.getByText(/continues headless under nex/)).toBeInTheDocument()
    expect(cancelBtn().textContent).toBe('Cancel')
    expect(confirmBtn().textContent).toContain('Hand to nex')
  })

  it('Cancel closes without calling handToNex', () => {
    const { onClose } = renderDialog()
    fireEvent.click(cancelBtn())
    expect(onClose).toHaveBeenCalledTimes(1)
    expect(mockedHandToNex).not.toHaveBeenCalled()
  })

  it('Escape closes when idle', () => {
    const { onClose } = renderDialog()
    fireEvent.keyDown(document, { key: 'Escape' })
    expect(onClose).toHaveBeenCalledTimes(1)
  })
})

describe('HandoffConfirmDialog — keep the tmux session (exec-to-terminal spec §4.3 / G4)', () => {
  it('shows the checkbox, labelled and checked by default', () => {
    renderDialog()
    expect(keepBox().checked).toBe(true)
    expect(screen.getByLabelText('Keep the tmux session')).toBe(keepBox())
  })

  it('unchecked → handToNex gets keepSession:false', async () => {
    mockedHandToNex.mockResolvedValueOnce({ result: { ...ok, session_kept: false }, swapped: true })
    renderDialog()
    fireEvent.click(keepBox())
    expect(keepBox().checked).toBe(false)
    await act(async () => { fireEvent.click(confirmBtn()) })
    expect(mockedHandToNex).toHaveBeenCalledWith({ ...args, keepSession: false })
  })

  it('is checked again on every open — never remembered (user ruling 2026-09-19)', async () => {
    mockedHandToNex.mockResolvedValueOnce({ result: { ...ok, session_kept: false }, swapped: true })
    const { onClose } = renderDialog()
    fireEvent.click(keepBox())
    await act(async () => { fireEvent.click(confirmBtn()) })
    expect(onClose).toHaveBeenCalledTimes(1)
    cleanup()
    renderDialog()
    expect(keepBox().checked).toBe(true)

    // Also after a plain cancel.
    fireEvent.click(keepBox())
    fireEvent.click(cancelBtn())
    cleanup()
    renderDialog()
    expect(keepBox().checked).toBe(true)
  })

  it('the checkbox is inert while busy', async () => {
    const d = deferred<{ result: typeof ok; swapped: boolean }>()
    mockedHandToNex.mockReturnValueOnce(d.promise)
    renderDialog()
    fireEvent.click(confirmBtn())
    expect(keepBox().disabled).toBe(true)
    await act(async () => { d.resolve({ result: ok, swapped: true }) })
  })

  it('names the other panes on this session (same host + code, this pane excluded); silent when there are none', () => {
    // This pane's own tab + two more tabs on the same session, one on another
    // session and one on the same code but another host.
    const own = sessionTab(args.hostId, args.sessionCode)
    const ownPane = getPrimaryPane(useTabStore.getState().tabs[own].layout).id
    sessionTab(args.hostId, args.sessionCode)
    sessionTab(args.hostId, args.sessionCode)
    sessionTab(args.hostId, 'other1')
    sessionTab('h2', args.sessionCode)
    const onClose = vi.fn()
    render(<HandoffConfirmDialog {...args} tabId={own} paneId={ownPane} onClose={onClose} />)
    expect(screen.getByTestId('handoff-other-panes')).toHaveTextContent('2 other panes use this session')
    cleanup()

    useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null })
    const alone = sessionTab(args.hostId, args.sessionCode)
    const alonePane = getPrimaryPane(useTabStore.getState().tabs[alone].layout).id
    sessionTab(args.hostId, 'other1')
    render(<HandoffConfirmDialog {...args} tabId={alone} paneId={alonePane} onClose={onClose} />)
    expect(screen.queryByTestId('handoff-other-panes')).toBeNull()
  })

  it('swapped:false with session_kept:false → the "Open execution" action opens the execution WITHOUT `from`', async () => {
    mockedHandToNex.mockResolvedValueOnce({ result: { ...ok, session_kept: false }, swapped: false })
    const open = vi.spyOn(useTabStore.getState(), 'openSingletonTab').mockReturnValue('tab-x')
    renderDialog()
    fireEvent.click(keepBox())
    await act(async () => { fireEvent.click(confirmBtn()) })
    toast()!.action!()
    expect(open).toHaveBeenCalledWith({ kind: 'execution', executionId: 'exc_1', host: 'h1' })
    expect(open.mock.calls[0][0]).not.toHaveProperty('from')
  })
})

describe('HandoffConfirmDialog — confirm', () => {
  it('rapid double-click on Confirm → exactly one handToNex call, both buttons disabled while busy', async () => {
    const d = deferred<{ result: typeof ok; swapped: boolean }>()
    mockedHandToNex.mockReturnValueOnce(d.promise)
    const { onClose } = renderDialog()

    fireEvent.click(confirmBtn())
    fireEvent.click(confirmBtn())
    expect(mockedHandToNex).toHaveBeenCalledTimes(1)
    expect(mockedHandToNex).toHaveBeenCalledWith({ ...args, keepSession: true })
    expect(confirmBtn().disabled).toBe(true)
    expect(cancelBtn().disabled).toBe(true)
    fireEvent.keyDown(document, { key: 'Escape' })
    expect(onClose).not.toHaveBeenCalled()

    await act(async () => { d.resolve({ result: ok, swapped: true }) })
    expect(onClose).toHaveBeenCalledTimes(1)
  })

  it('success closes the dialog and toasts handoff.success with no action', async () => {
    mockedHandToNex.mockResolvedValueOnce({ result: ok, swapped: true })
    const { onClose } = renderDialog()
    await act(async () => { fireEvent.click(confirmBtn()) })
    expect(onClose).toHaveBeenCalledTimes(1)
    expect(toast()?.message).toBe('Handed to nex.')
    expect(toast()?.action).toBeUndefined()
  })

  it('swapped:false closes and toasts with an "Open execution" action that opens the from-bearing execution', async () => {
    mockedHandToNex.mockResolvedValueOnce({ result: ok, swapped: false })
    const open = vi.spyOn(useTabStore.getState(), 'openSingletonTab').mockReturnValue('tab-x')
    const { onClose } = renderDialog()
    await act(async () => { fireEvent.click(confirmBtn()) })
    expect(onClose).toHaveBeenCalledTimes(1)
    expect(toast()?.message).toBe('Handed to nex.')
    expect(toast()?.actionLabel).toBe('Open execution')
    expect(toast()?.action).toBeTypeOf('function')
    toast()!.action!()
    expect(open).toHaveBeenCalledWith({
      kind: 'execution',
      executionId: 'exc_1',
      host: 'h1',
      from: { sessionCode: 'zk16vd', tmuxInstance: 'inst-1', cachedName: 'purdex' },
    })
  })
})

describe('HandoffConfirmDialog — errors keep the dialog open', () => {
  async function failWith(err: HandoffApiError) {
    mockedHandToNex.mockRejectedValueOnce(err)
    const r = renderDialog()
    await act(async () => { fireEvent.click(confirmBtn()) })
    return r
  }

  it('no_cc → error toast, dialog stays, buttons re-enabled', async () => {
    const { onClose } = await failWith(new HandoffApiError(409, 'no_cc', { error: 'x', code: 'no_cc' }))
    expect(toast()?.message).toBe('No Claude Code session is running in this pane.')
    expect(onClose).not.toHaveBeenCalled()
    expect(screen.getByTestId('handoff-dialog')).toBeInTheDocument()
    await waitFor(() => expect(confirmBtn().disabled).toBe(false))
    expect(cancelBtn().disabled).toBe(false)
  })

  it('delegate_rejected rolled back → reason + restored; no manual-resume line even when session_id is present (CC is already back)', async () => {
    await failWith(new HandoffApiError(409, 'delegate_rejected', { reject_reason: 'quota', rolled_back: true }))
    expect(toast()?.message).toBe('nex rejected the handoff (quota); the terminal was restored.')
    cleanup()
    await failWith(new HandoffApiError(409, 'delegate_rejected', { reject_reason: 'quota', rolled_back: true, session_id: 'sid-9' }))
    expect(toast()?.message).toBe('nex rejected the handoff (quota); the terminal was restored.')
  })

  it('delegate_rejected NOT rolled back → second line with the manual resume command', async () => {
    await failWith(new HandoffApiError(409, 'delegate_rejected', { reject_reason: 'quota', rolled_back: false, session_id: 'sid-9' }))
    expect(toast()?.message).toBe(
      'nex rejected the handoff (quota); the terminal was not restored.\nResume by hand: claude --resume sid-9',
    )
  })

  it('cc_exit_timeout → step in the message; no manual-resume line (the code never carries session_id)', async () => {
    await failWith(new HandoffApiError(504, 'cc_exit_timeout', { step: 'exit_confirm', session_id: 'sid-9' }))
    expect(toast()?.message).toBe('Claude Code did not exit in time (step: exit_confirm).')
  })

  it('tmux_instance_mismatch with session_id → manual-resume line', async () => {
    await failWith(new HandoffApiError(409, 'tmux_instance_mismatch', { session_id: 'sid-7' }))
    expect(toast()?.message).toBe(
      'The tmux session was replaced since this pane opened; reopen it and try again.\nResume by hand: claude --resume sid-7',
    )
  })

  it('a second Confirm after an error retries (new handToNex call)', async () => {
    const { onClose } = await failWith(new HandoffApiError(503, 'nex_unavailable', {}))
    mockedHandToNex.mockResolvedValueOnce({ result: ok, swapped: true })
    await waitFor(() => expect(confirmBtn().disabled).toBe(false))
    await act(async () => { fireEvent.click(confirmBtn()) })
    expect(mockedHandToNex).toHaveBeenCalledTimes(2)
    expect(onClose).toHaveBeenCalledTimes(1)
  })

  it('a non-API error falls back to the generic message and keeps the dialog open', async () => {
    mockedHandToNex.mockRejectedValueOnce(new TypeError('boom'))
    const { onClose } = renderDialog()
    await act(async () => { fireEvent.click(confirmBtn()) })
    expect(toast()?.message).toBe('Handoff failed (unknown).')
    expect(onClose).not.toHaveBeenCalled()
  })
})
