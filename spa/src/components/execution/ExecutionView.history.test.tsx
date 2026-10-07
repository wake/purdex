// ExecutionView — reply-box input history (ArrowUp / ArrowDown). The caret and stash rules are
// pinned in hooks/useInputHistory.test.ts; this file drives them through the real pane, with chips.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { render, screen, fireEvent, act, waitFor } from '@testing-library/react'
import ExecutionView from './ExecutionView'
import { useExecutionStore } from '../../stores/useExecutionStore'
import { useShownHostsStore } from '../../stores/useShownHostsStore'
import { useHostConfigStore } from '../../stores/useHostConfigStore'
import { useNexHostStore } from '../../stores/useNexHostStore'
import * as api from '../../lib/nex/nex-api'
import * as lease from '../../hooks/useExecutionLease'
import * as sub from '../../hooks/useExecutionSubscription'

vi.mock('../../lib/nex/nex-api', () => ({ sendMessage: vi.fn(), interruptExecution: vi.fn(), terminateExecution: vi.fn(), releaseLease: vi.fn(), uploadWorkerFile: vi.fn(), fetchAttachment: vi.fn() }))
vi.mock('../../hooks/useExecutionSubscription', () => ({ useExecutionSubscription: vi.fn(() => ({ problem: null, paused: false })) }))
vi.mock('../../hooks/useExecutionLease', () => ({ useExecutionLease: vi.fn() }))
vi.mock('../../lib/nex/client-id', () => ({ getNexClientId: () => 't-me000000' }))

const H = 'h', E = 'exc_1'
const base = { hostId: H, executionId: E, tabId: 't1', paneId: 'p1', onModeChange: () => {} }
const ensureLease = vi.fn(), release = vi.fn(), touch = vi.fn(), forget = vi.fn()
const summary = { id: E, state: 'idle', provider: 'claude', principal_id: 'p', cwd: '/Users/w/repo', mount_kind: 'dev', brief: 'b', labels: {}, created_at: 0, updated_at: 0, duration_ms: null, event_count: 0, observers: 2, archived: false, effective_profile: 'standard', turn_count: 3 }
const PNG = [0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 1, 2, 3]
const SHA = 'a'.repeat(64)
const ROUTE = { method: 'GET', path: '/api/nex/v1/executions/{id}/attachments/{sha256}' }

function seedCaps() {
  const send = {
    delivery: ['delivered', 'queued'], max_text_bytes: 65536, max_request_bytes: 32 * 1024 * 1024,
    attachments: { image: { media_types: ['image/png'], max_bytes: 5 * 1024 * 1024, max_count: 10, max_total_bytes: 20 * 1024 * 1024, providers: ['claude'], fetch: ROUTE } },
  }
  useNexHostStore.setState({
    byHost: { [H]: { phase: 'ready', capabilities: { send }, info: null, error: null, fetchedAt: Date.now(), generation: 1, fingerprint: 'x' } } as never,
    ensure: async () => {},
  })
}
let seq = 0
const sent = (text: string, attachments?: unknown[]) => act(() => {
  seq++
  useExecutionStore.getState().applyEvents(H, E, [
    { seq, execution_id: E, kind: 'execution.message_accepted', payload: { text, turn_id: `t${seq}`, ...(attachments ? { attachments } : {}) }, created_at: 0 },
  ])
})
const box = () => screen.getByRole('textbox') as HTMLTextAreaElement
const chips = () => screen.queryAllByTestId('upload-chip')
function key(k: 'ArrowUp' | 'ArrowDown', caret?: number) {
  const ta = box()
  if (caret !== undefined) ta.setSelectionRange(caret, caret)
  fireEvent.keyDown(ta, { key: k })
}

const origCreate = URL.createObjectURL, origRevoke = URL.revokeObjectURL
beforeEach(() => {
  seq = 0
  useExecutionStore.setState({ executions: {} })
  ensureLease.mockReset().mockResolvedValue('ls_1')
  vi.mocked(lease.useExecutionLease).mockReturnValue({ ensureLease, release, forget, touch })
  vi.mocked(sub.useExecutionSubscription).mockReturnValue({ problem: null, paused: false })
  vi.mocked(api.sendMessage).mockReset().mockResolvedValue({ turn_id: 't1', delivery: 'delivered' })
  vi.mocked(api.uploadWorkerFile).mockReset()
  vi.mocked(api.fetchAttachment).mockReset().mockResolvedValue(new Blob([new Uint8Array(PNG)], { type: 'image/png' }))
  useExecutionStore.getState().setSummary(H, E, summary as never)
  useExecutionStore.getState().setHistoryLoaded(H, E, true)
  useShownHostsStore.setState({ ids: [H] })
  useHostConfigStore.setState({ byHost: {}, ensureLoaded: async () => {} })
  seedCaps()
  let n = 0
  Object.assign(URL, { createObjectURL: vi.fn(() => `blob:${++n}`), revokeObjectURL: vi.fn() })
})
afterEach(() => { Object.assign(URL, { createObjectURL: origCreate, revokeObjectURL: origRevoke }) })

describe('ExecutionView — input history', () => {
  it('Up walks back one message per press; Down walks forward and returns to the unsent draft', () => {
    render(<ExecutionView {...base} isActive />)
    sent('first'); sent('second'); sent('third')
    fireEvent.change(box(), { target: { value: 'A' } })
    key('ArrowUp', 0)
    expect(box().value).toBe('third')
    key('ArrowUp') // the caret was parked at the start: it walks on
    expect(box().value).toBe('second')
    key('ArrowUp')
    expect(box().value).toBe('first')
    key('ArrowUp')
    expect(box().value).toBe('first') // the oldest: nothing further
    key('ArrowDown')
    expect(box().value).toBe('second')
    key('ArrowDown'); key('ArrowDown')
    expect(box().value).toBe('A')
    key('ArrowDown') // not walking any more: an ordinary key
    expect(box().value).toBe('A')
  })

  it('Up, Up, Down, Down returns to the draft', () => {
    render(<ExecutionView {...base} isActive />)
    sent('one'); sent('two')
    fireEvent.change(box(), { target: { value: 'A' } })
    key('ArrowUp', 0); key('ArrowUp'); key('ArrowDown'); key('ArrowDown')
    expect(box().value).toBe('A')
  })

  it('does nothing while the caret is not at the very start (Up) / end (Down), or with no history', () => {
    render(<ExecutionView {...base} isActive />)
    fireEvent.change(box(), { target: { value: 'A\nB' } })
    key('ArrowUp', 1) // on the first line, but not position 0
    expect(box().value).toBe('A\nB')
    sent('old')
    key('ArrowUp', 2)
    expect(box().value).toBe('A\nB')
    key('ArrowUp', 0)
    expect(box().value).toBe('old')
  })

  it('an empty history leaves the box alone', () => {
    render(<ExecutionView {...base} isActive />)
    fireEvent.change(box(), { target: { value: 'A' } })
    key('ArrowUp', 0)
    expect(box().value).toBe('A')
  })

  it('does not navigate during an IME composition', () => {
    render(<ExecutionView {...base} isActive />)
    sent('old')
    fireEvent.keyDown(box(), { key: 'ArrowUp', keyCode: 229 })
    expect(box().value).toBe('')
    fireEvent.keyDown(box(), { key: 'ArrowUp', isComposing: true })
    expect(box().value).toBe('')
  })

  it('de-duplicates adjacent equal messages, skips injected notifications and the interrupt sentinel', () => {
    render(<ExecutionView {...base} isActive />)
    sent('same'); sent('same'); sent('<task-notification>done</task-notification>')
    key('ArrowUp', 0); expect(box().value).toBe('same')
    key('ArrowUp'); expect(box().value).toBe('same') // the oldest, nothing before it
    key('ArrowDown'); key('ArrowDown')
    expect(box().value).toBe('')
  })

  it('includes the just-sent optimistic message before the daemon echoes it', () => {
    render(<ExecutionView {...base} isActive />)
    act(() => { useExecutionStore.getState().setPendingLocal(H, E, { text: 'optimistic', delivery: null }) })
    key('ArrowUp', 0)
    expect(box().value).toBe('optimistic')
  })

  it('a recalled message with [file:] lines comes back as its text and a path chip; the draft chips return with the draft', async () => {
    render(<ExecutionView {...base} isActive />)
    sent('look at this\n\n[file: /Users/w/repo/.purdex-uploads/exc_1/a.txt]')
    // the draft: text 'A' and a path chip of its own
    fireEvent.change(box(), { target: { value: 'A' } })
    const draftFile = new File(['x'], 'draft.txt', { type: 'text/plain' })
    vi.mocked(api.uploadWorkerFile).mockResolvedValueOnce({ path: '/Users/w/repo/.purdex-uploads/exc_1/draft.txt', name: 'draft.txt', size: 1 })
    const root = screen.getByTestId('execution-view')
    fireEvent.dragEnter(root, { dataTransfer: { types: ['Files'], files: [draftFile] } })
    fireEvent.drop(root, { dataTransfer: { types: ['Files'], files: [draftFile] } })
    await waitFor(() => expect(chips()[0]?.dataset.status).toBe('done'))
    expect(chips()[0]).toHaveTextContent('draft.txt')

    key('ArrowUp', 0)
    expect(box().value).toBe('look at this')
    expect(chips()).toHaveLength(1)
    expect(chips()[0]).toHaveTextContent('a.txt')
    expect(chips()[0].dataset.status).toBe('done')

    key('ArrowDown')
    expect(box().value).toBe('A')
    expect(chips()).toHaveLength(1)
    expect(chips()[0]).toHaveTextContent('draft.txt')
  })

  it('sending a recalled message sends its text and its attachments, then the walk is over', async () => {
    render(<ExecutionView {...base} isActive />)
    sent('look at this\n\n[file: /Users/w/repo/.purdex-uploads/exc_1/a.txt]')
    key('ArrowUp', 0)
    fireEvent.keyDown(box(), { key: 'Enter' })
    await waitFor(() => expect(api.sendMessage).toHaveBeenCalledWith(H, E, 'ls_1', 'look at this\n\n[file: /Users/w/repo/.purdex-uploads/exc_1/a.txt]'))
    await waitFor(() => expect(chips()).toHaveLength(0))
    expect(box().value).toBe('')
  })

  it('a recalled message with a native image fetches the bytes back into a native chip and re-sends it', async () => {
    render(<ExecutionView {...base} isActive />)
    sent('see', [{ media_type: 'image/png', bytes: PNG.length, sha256: SHA }])
    key('ArrowUp', 0)
    expect(box().value).toBe('see')
    await waitFor(() => expect(chips()).toHaveLength(1))
    expect(api.fetchAttachment).toHaveBeenCalledWith(H, E, SHA, ROUTE, expect.anything())
    expect(chips()[0].dataset.status).toBe('done')
    expect(api.uploadWorkerFile).not.toHaveBeenCalled()
    fireEvent.keyDown(box(), { key: 'Enter' })
    await waitFor(() => expect(api.sendMessage).toHaveBeenCalledWith(H, E, 'ls_1', 'see',
      [{ type: 'image', media_type: 'image/png', data: btoa(String.fromCharCode(...PNG)) }]))
  })

  it('an image that can no longer be fetched is skipped: the text still comes back, no chip', async () => {
    vi.mocked(api.fetchAttachment).mockRejectedValue(new Error('gone'))
    render(<ExecutionView {...base} isActive />)
    sent('see', [{ media_type: 'image/png', bytes: 3, sha256: SHA }])
    key('ArrowUp', 0)
    await waitFor(() => expect(api.fetchAttachment).toHaveBeenCalled())
    expect(box().value).toBe('see')
    expect(chips()).toHaveLength(0)
  })

  it('walking away before the image arrives drops it (no chip appears on the draft)', async () => {
    let finish!: (b: Blob) => void
    vi.mocked(api.fetchAttachment).mockReturnValue(new Promise<Blob>((r) => { finish = r }))
    render(<ExecutionView {...base} isActive />)
    sent('see', [{ media_type: 'image/png', bytes: 3, sha256: SHA }])
    fireEvent.change(box(), { target: { value: 'A' } })
    key('ArrowUp', 0)
    key('ArrowDown')
    expect(box().value).toBe('A')
    await act(async () => { finish(new Blob([new Uint8Array(PNG)], { type: 'image/png' })); await Promise.resolve() })
    expect(chips()).toHaveLength(0)
  })

  it('the interrupt keys keep working: Esc on an empty box interrupts a live turn', async () => {
    vi.mocked(api.interruptExecution).mockResolvedValue({ turn_id: 't1', state: 'idle' })
    render(<ExecutionView {...base} isActive />)
    sent('old')
    act(() => { useExecutionStore.setState((s) => ({ executions: { ...s.executions, [`${H}:${E}`]: { ...s.executions[`${H}:${E}`], turnLive: true } } })) })
    fireEvent.keyDown(box(), { key: 'Escape' })
    await waitFor(() => expect(api.interruptExecution).toHaveBeenCalled())
  })

  it('nothing navigates while the box is locked by an unaccepted send', async () => {
    vi.mocked(api.sendMessage).mockReturnValue(new Promise(() => {}))
    render(<ExecutionView {...base} isActive />)
    sent('old')
    fireEvent.change(box(), { target: { value: 'now' } })
    fireEvent.keyDown(box(), { key: 'Enter' })
    await waitFor(() => expect(api.sendMessage).toHaveBeenCalled())
    expect(box()).toHaveAttribute('aria-disabled', 'true')
    key('ArrowUp', 0)
    expect(box().value).toBe('')
  })
})
