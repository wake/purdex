// ExecutionView — the send buffer (undo window). After Enter the message looks
// sent (dimmed pending line, thinking indicator, empty box) but is only posted
// once the window ends; Esc / Ctrl+C inside it takes it back, text and chips.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { render, screen, fireEvent, act } from '@testing-library/react'
import ExecutionView from './ExecutionView'
import { useExecutionStore } from '../../stores/useExecutionStore'
import { useWorkerSettingsStore } from '../../stores/useWorkerSettingsStore'
import { useShownHostsStore } from '../../stores/useShownHostsStore'
import { useHostConfigStore } from '../../stores/useHostConfigStore'
import { clearAllHeldSends } from '../../lib/nex/send-buffer'
import { clearAllWorkerDrafts, readWorkerDraft, workerDraftKey } from '../../lib/nex/worker-draft-memory'
import { NexApiError } from '../../lib/nex/types'
import * as api from '../../lib/nex/nex-api'
import * as lease from '../../hooks/useExecutionLease'
import * as sub from '../../hooks/useExecutionSubscription'

vi.mock('../../lib/nex/nex-api', () => ({ sendMessage: vi.fn(), interruptExecution: vi.fn(), terminateExecution: vi.fn(), releaseLease: vi.fn(), uploadWorkerFile: vi.fn(), fetchAttachment: vi.fn() }))
vi.mock('../../hooks/useExecutionSubscription', () => ({ useExecutionSubscription: vi.fn(() => ({ problem: null, paused: false })) }))
vi.mock('../../hooks/useExecutionLease', () => ({ useExecutionLease: vi.fn() }))
vi.mock('../../lib/nex/client-id', () => ({ getNexClientId: () => 't-me000000' }))

const H = 'h', E = 'exc_1', KEY = 'h:exc_1'
const base = { hostId: H, executionId: E, tabId: 't1', paneId: 'p1', onModeChange: () => {} }
const ensureLease = vi.fn(), release = vi.fn(), touch = vi.fn(), forget = vi.fn()
const summary = () => ({ id: E, state: 'idle', provider: 'claude', principal_id: 'p', cwd: '/Users/w/repo', mount_kind: 'dev', brief: 'b', labels: {}, created_at: 0, updated_at: 0, duration_ms: null, event_count: 0, observers: 2, archived: false, effective_profile: 'standard', turn_count: 3 })
const st = () => useExecutionStore.getState().executions[KEY]
const box = () => screen.getByRole('textbox') as HTMLTextAreaElement
const type = (text: string) => fireEvent.change(box(), { target: { value: text } })
const enter = () => fireEvent.keyDown(box(), { key: 'Enter' })
const esc = () => fireEvent.keyDown(box(), { key: 'Escape' })
const advance = (ms: number) => act(async () => { await vi.advanceTimersByTimeAsync(ms) })
const chips = () => screen.queryAllByTestId('upload-chip')

beforeEach(() => {
  vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout', 'setInterval', 'clearInterval', 'Date'] })
  clearAllHeldSends(); clearAllWorkerDrafts()
  useWorkerSettingsStore.setState({ sendBufferSec: 3 })
  useExecutionStore.setState({ executions: {} })
  ensureLease.mockReset().mockResolvedValue('ls_1'); release.mockReset(); touch.mockReset(); forget.mockReset()
  vi.mocked(lease.useExecutionLease).mockReturnValue({ ensureLease, release, forget, touch })
  vi.mocked(sub.useExecutionSubscription).mockReturnValue({ problem: null, paused: false })
  vi.mocked(api.sendMessage).mockReset().mockResolvedValue({ turn_id: 't1', delivery: 'delivered' })
  vi.mocked(api.interruptExecution).mockReset().mockResolvedValue({ turn_id: 't1', state: 'idle' })
  vi.mocked(api.uploadWorkerFile).mockReset().mockResolvedValue({ path: '/p/a.txt', name: 'a.txt', size: 1 })
  useExecutionStore.getState().setSummary(H, E, summary() as never)
  useExecutionStore.getState().setHistoryLoaded(H, E, true)
  useShownHostsStore.setState({ ids: [H] })
  useHostConfigStore.setState({ byHost: {}, ensureLoaded: async () => {} })
})
afterEach(() => { clearAllHeldSends(); vi.useRealTimers() })

describe('ExecutionView — send buffer', () => {
  it('holds the POST for N seconds, showing the dimmed line and the thinking indicator meanwhile', async () => {
    render(<ExecutionView {...base} isActive />)
    type('hello'); enter()
    expect(api.sendMessage).not.toHaveBeenCalled()
    expect(box().value).toBe('')
    expect(screen.getByText('hello')).toBeInTheDocument()
    expect(st().pendingLocal?.buffered).toBeDefined()
    expect(screen.getByTestId('thinking-indicator')).toBeInTheDocument()
    expect(screen.getByTestId('send-buffer-countdown')).toHaveTextContent('3s')
    await advance(2900)
    expect(api.sendMessage).not.toHaveBeenCalled()
    await advance(200)
    expect(api.sendMessage).toHaveBeenCalledWith(H, E, 'ls_1', 'hello')
    expect(screen.queryByTestId('send-buffer-countdown')).toBeNull()
  })

  it('does not lock the box during the buffer; the lock starts when the POST is issued', async () => {
    let resolve!: (v: { turn_id: string; delivery: 'delivered' }) => void
    vi.mocked(api.sendMessage).mockReturnValueOnce(new Promise((r) => { resolve = r }))
    render(<ExecutionView {...base} isActive />)
    type('hello'); enter()
    expect(st().sendLocked).toBe(false)
    expect(box().getAttribute('aria-disabled')).toBe('false')
    type('next thought')
    expect(box().value).toBe('next thought')
    await advance(3000)
    expect(st().sendLocked).toBe(true)
    await act(async () => { resolve({ turn_id: 't1', delivery: 'delivered' }); await vi.advanceTimersByTimeAsync(0) })
    expect(st().sendLocked).toBe(false)
  })

  it('0 seconds sends at once, as before', async () => {
    useWorkerSettingsStore.setState({ sendBufferSec: 0 })
    render(<ExecutionView {...base} isActive />)
    type('now'); enter()
    await advance(0)
    expect(api.sendMessage).toHaveBeenCalledWith(H, E, 'ls_1', 'now')
    expect(st().pendingLocal?.buffered).toBeUndefined()
  })

  it('Esc inside the window cancels: no POST, the line goes, the text is back in the box', async () => {
    render(<ExecutionView {...base} isActive />)
    type('oops'); enter()
    esc()
    expect(st().pendingLocal).toBeNull()
    expect(box().value).toBe('oops')
    await advance(10_000)
    expect(api.sendMessage).not.toHaveBeenCalled()
  })

  it('Ctrl+C with no selection cancels too', async () => {
    render(<ExecutionView {...base} isActive />)
    type('oops'); enter()
    fireEvent.keyDown(box(), { key: 'c', ctrlKey: true })
    expect(box().value).toBe('oops')
    await advance(10_000)
    expect(api.sendMessage).not.toHaveBeenCalled()
  })

  it('a cancel puts the text in front of newer typing, separated by a newline — neither is lost', async () => {
    render(<ExecutionView {...base} isActive />)
    type('first'); enter()
    type('second')
    esc()
    expect(box().value).toBe('first\nsecond')
  })

  it('a cancel brings the attachment chips back', async () => {
    render(<ExecutionView {...base} isActive />)
    const root = screen.getByTestId('execution-view')
    const files = [new File(['x'], 'a.txt', { type: 'text/plain' })]
    fireEvent.dragEnter(root, { dataTransfer: { types: ['Files'], files } })
    fireEvent.drop(root, { dataTransfer: { types: ['Files'], files } })
    await advance(0)
    expect(chips()).toHaveLength(1)
    type('with file'); enter()
    expect(chips()).toHaveLength(0)
    esc()
    expect(chips()).toHaveLength(1)
    expect(box().value).toBe('with file')
    await advance(10_000)
    expect(api.sendMessage).not.toHaveBeenCalled()
  })

  it('a message that is sent takes its chips with it (they do not come back)', async () => {
    render(<ExecutionView {...base} isActive />)
    const root = screen.getByTestId('execution-view')
    const files = [new File(['x'], 'a.txt', { type: 'text/plain' })]
    fireEvent.dragEnter(root, { dataTransfer: { types: ['Files'], files } })
    fireEvent.drop(root, { dataTransfer: { types: ['Files'], files } })
    await advance(0)
    type('with file'); enter()
    await advance(3000)
    expect(String(vi.mocked(api.sendMessage).mock.calls[0][3])).toContain('[file: /p/a.txt]')
    expect(chips()).toHaveLength(0)
  })

  it('Esc priority: cancels a held message before interrupting a live turn; with none held it interrupts', async () => {
    useExecutionStore.setState({ executions: { [KEY]: { ...st(), turnLive: true } } })
    render(<ExecutionView {...base} isActive />)
    type('held'); enter()
    esc()
    await advance(0)
    expect(api.interruptExecution).not.toHaveBeenCalled()
    expect(box().value).toBe('held')
    type('')
    esc()
    await advance(0)
    expect(api.interruptExecution).toHaveBeenCalledTimes(1)
  })

  it('a second send commits the first at once, then holds the new one; Esc takes back only the newest', async () => {
    render(<ExecutionView {...base} isActive />)
    type('A'); enter()
    type('B'); enter()
    await advance(0)
    expect(api.sendMessage).toHaveBeenCalledTimes(1)
    expect(api.sendMessage).toHaveBeenCalledWith(H, E, 'ls_1', 'A')
    expect(st().pendingLocal).toMatchObject({ text: 'B', buffered: expect.any(Object) })
    esc()
    expect(box().value).toBe('B')
    await advance(10_000)
    expect(api.sendMessage).toHaveBeenCalledTimes(1)
  })

  it('the second message still goes out after the window when it is not taken back', async () => {
    render(<ExecutionView {...base} isActive />)
    type('A'); enter()
    type('B'); enter()
    await advance(3100)
    expect(vi.mocked(api.sendMessage).mock.calls.map((c) => c[3])).toEqual(['A', 'B'])
  })

  it('unmounting the pane inside the window still sends the message', async () => {
    const { unmount } = render(<ExecutionView {...base} isActive />)
    type('keep me'); enter()
    unmount()
    await advance(0)
    expect(api.sendMessage).toHaveBeenCalledWith(H, E, 'ls_1', 'keep me')
    await advance(10_000)
    expect(api.sendMessage).toHaveBeenCalledTimes(1)
  })

  it('unmount then a failed POST restores the text into the worker draft memory', async () => {
    vi.mocked(api.sendMessage).mockRejectedValueOnce(new NexApiError(400, 'invalid_text', 'bad'))
    const { unmount } = render(<ExecutionView {...base} isActive />)
    type('lost?'); enter()
    unmount()
    await advance(0)
    expect(readWorkerDraft(workerDraftKey(H, E))).toBe('lost?')
  })

  it('a failed POST after the window brings text back to the box', async () => {
    vi.mocked(api.sendMessage).mockRejectedValueOnce(new NexApiError(400, 'invalid_text', 'bad'))
    render(<ExecutionView {...base} isActive />)
    type('retry me'); enter()
    type('typed meanwhile')
    await advance(3000)
    expect(box().value).toBe('retry me\ntyped meanwhile')
    expect(st().pendingLocal).toBeNull()
  })

  it('take-back and exit are refused while a message is held', async () => {
    render(<ExecutionView {...base} isActive />)
    type('held'); enter()
    expect((screen.getByTestId('header-exit') as HTMLButtonElement).disabled).toBe(true)
  })
})
