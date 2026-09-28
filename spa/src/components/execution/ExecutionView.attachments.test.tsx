// ExecutionView — native image attachments (worker-pane theme phase E, E3).
// The phase D path flow is pinned in ExecutionView.test.tsx; this file pins
// what changes when the host accepts images for the execution's provider,
// and that nothing changes when it does not (Review Focus 1–3).
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { render, screen, fireEvent, act, waitFor, within } from '@testing-library/react'
import ExecutionView from './ExecutionView'
import { useExecutionStore } from '../../stores/useExecutionStore'
import { useShownHostsStore } from '../../stores/useShownHostsStore'
import { useHostConfigStore, emptyHostConfigEntry } from '../../stores/useHostConfigStore'
import { useNexHostStore } from '../../stores/useNexHostStore'
import { NexApiError } from '../../lib/nex/types'
import { requestBytes } from '../../lib/nex/worker-upload'
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
const summary = (extra = {}) => ({ id: E, state: 'idle', provider: 'claude', principal_id: 'p', cwd: '/Users/w/repo', mount_kind: 'dev', brief: 'b', labels: {}, created_at: 0, updated_at: 0, duration_ms: null, event_count: 0, observers: 2, archived: false, effective_profile: 'standard', turn_count: 3, ...extra })
const MiB = 1024 * 1024
const saved = (name: string) => `/Users/w/repo/.purdex-uploads/${E}/${name}`

const PNG = [0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 1, 2, 3]
const png = (name: string, bytes = PNG) => new File([new Uint8Array(bytes)], name, { type: 'image/png' })
const b64 = (bytes: number[]) => btoa(String.fromCharCode(...bytes))
const txt = (name: string) => new File(['x'], name, { type: 'text/plain' })

/** Seed the host's capabilities; `image: null` is a daemon older than 2026-09-28 (no send.attachments). */
function seedCaps(image: Record<string, unknown> | null, maxRequestBytes = 32 * MiB) {
  const send = image
    ? { delivery: ['delivered', 'queued'], max_text_bytes: 65536, max_request_bytes: maxRequestBytes, attachments: { image } }
    : { delivery: ['delivered', 'queued'], max_text_bytes: 65536 }
  useNexHostStore.setState({
    byHost: { [H]: { phase: 'ready', capabilities: { send }, info: null, error: null, fetchedAt: Date.now(), generation: 1, fingerprint: 'x' } } as never,
    ensure: async () => {},
  })
}
const imageCaps = (over: Record<string, unknown> = {}) => ({
  media_types: ['image/png', 'image/jpeg', 'image/gif', 'image/webp'], max_bytes: 5 * MiB, max_count: 10,
  max_total_bytes: 20 * MiB, providers: ['claude'], fetch: { method: 'GET', path: '/api/nex/v1/executions/{id}/attachments/{sha256}' }, ...over,
})

function dropFiles(files: File[]) {
  const root = screen.getByTestId('execution-view')
  fireEvent.dragEnter(root, { dataTransfer: { types: ['Files'], files } })
  fireEvent.drop(root, { dataTransfer: { types: ['Files'], files } })
}
function type(text: string) {
  fireEvent.change(screen.getByRole('textbox'), { target: { value: text } })
}
function enter() {
  fireEvent.keyDown(screen.getByRole('textbox'), { key: 'Enter' })
}
const chips = () => screen.queryAllByTestId('upload-chip')

/**
 * Holds every FileReader.readAsDataURL until `flush()` — a slow encode the
 * test can act inside of (remove a chip, click a quick reply).
 */
function holdReads() {
  const pending: Array<() => void> = []
  const Real = globalThis.FileReader
  class Held extends Real {
    readAsDataURL(blob: Blob) { pending.push(() => super.readAsDataURL(blob)) }
  }
  vi.stubGlobal('FileReader', Held)
  return {
    get count() { return pending.length },
    flush: () => { for (const f of pending.splice(0)) f() },
  }
}

let createUrl: ReturnType<typeof vi.fn>, revokeUrl: ReturnType<typeof vi.fn>
const origCreate = URL.createObjectURL, origRevoke = URL.revokeObjectURL

beforeEach(() => {
  useExecutionStore.setState({ executions: {} })
  ensureLease.mockReset().mockResolvedValue('ls_1'); release.mockReset(); touch.mockReset(); forget.mockReset()
  vi.mocked(lease.useExecutionLease).mockReturnValue({ ensureLease, release, forget, touch })
  vi.mocked(sub.useExecutionSubscription).mockReturnValue({ problem: null, paused: false })
  vi.mocked(api.sendMessage).mockReset().mockResolvedValue({ turn_id: 't1', delivery: 'delivered' })
  vi.mocked(api.fetchAttachment).mockReset().mockResolvedValue(new Blob(['x'], { type: 'image/png' }))
  vi.mocked(api.uploadWorkerFile).mockReset().mockImplementation(async (_h, _e, f) => ({ path: saved(f.name), name: f.name, size: 1 }))
  useExecutionStore.getState().setSummary(H, E, summary() as never)
  useExecutionStore.getState().setHistoryLoaded(H, E, true)
  useShownHostsStore.setState({ ids: [H] })
  useHostConfigStore.setState({ byHost: {}, ensureLoaded: async () => {} })
  let n = 0
  createUrl = vi.fn(() => `blob:${++n}`)
  revokeUrl = vi.fn()
  Object.assign(URL, { createObjectURL: createUrl, revokeObjectURL: revokeUrl })
})
afterEach(() => {
  vi.unstubAllGlobals()
  Object.assign(URL, { createObjectURL: origCreate, revokeObjectURL: origRevoke })
})

describe('ExecutionView — native image attachments', () => {
  it('an older daemon (no send.attachments): an image goes by path exactly as in phase D (Review Focus 1)', async () => {
    seedCaps(null)
    render(<ExecutionView {...base} isActive />)
    dropFiles([png('p.png')])
    await waitFor(() => expect(chips()[0].dataset.status).toBe('done'))
    expect(screen.queryByTestId('upload-chip-note')).toBeNull()
    type('look')
    enter()
    await waitFor(() => expect(api.sendMessage).toHaveBeenCalledWith(H, E, 'ls_1', `look\n\n[file: ${saved('p.png')}]`))
    expect(vi.mocked(api.sendMessage).mock.calls[0]).toHaveLength(4)
  })

  it('a codex execution on a host that takes images for claude only: by path, never attachments (Review Focus 2)', async () => {
    seedCaps(imageCaps())
    useExecutionStore.getState().setSummary(H, E, summary({ provider: 'codex' }) as never)
    render(<ExecutionView {...base} isActive />)
    dropFiles([png('p.png')])
    await waitFor(() => expect(chips()[0].dataset.status).toBe('done'))
    expect(api.uploadWorkerFile).toHaveBeenCalledTimes(1)
    enter()
    await waitFor(() => expect(api.sendMessage).toHaveBeenCalledWith(H, E, 'ls_1', `[file: ${saved('p.png')}]`))
    expect(vi.mocked(api.sendMessage).mock.calls[0]).toHaveLength(4)
  })

  it('an image-only send posts text "" plus the base64 attachment, and clears the chip', async () => {
    seedCaps(imageCaps())
    render(<ExecutionView {...base} isActive />)
    dropFiles([png('p.png')])
    expect(chips()[0].dataset.status).toBe('done')
    expect(api.uploadWorkerFile).not.toHaveBeenCalled()
    enter()
    await waitFor(() => expect(api.sendMessage).toHaveBeenCalledWith(H, E, 'ls_1', '',
      [{ type: 'image', media_type: 'image/png', data: b64(PNG) }]))
    await waitFor(() => expect(chips()).toHaveLength(0))
  })

  it('mixes native images and [file:] lines in one message', async () => {
    seedCaps(imageCaps())
    render(<ExecutionView {...base} isActive />)
    dropFiles([png('p.png'), txt('a.txt')])
    await waitFor(() => expect(chips().map((c) => c.dataset.status)).toEqual(['done', 'done']))
    type('see')
    enter()
    await waitFor(() => expect(api.sendMessage).toHaveBeenCalledWith(H, E, 'ls_1', `see\n\n[file: ${saved('a.txt')}]`,
      [{ type: 'image', media_type: 'image/png', data: b64(PNG) }]))
  })

  it('six 4 MiB images: five native, the sixth by path with a note; the send carries five (Review Focus 3)', async () => {
    seedCaps(imageCaps())
    render(<ExecutionView {...base} isActive />)
    const files = [1, 2, 3, 4, 5, 6].map((n) => png(`${n}.png`))
    for (const f of files) Object.defineProperty(f, 'size', { value: 4 * MiB })
    dropFiles(files)
    await waitFor(() => expect(chips().map((c) => c.dataset.status)).toEqual(['done', 'done', 'done', 'done', 'done', 'done']))
    expect(vi.mocked(api.uploadWorkerFile).mock.calls.map((c) => c[2].name)).toEqual(['6.png'])
    expect(screen.getAllByTestId('upload-chip-note')).toHaveLength(1)
    expect(chips()[5]).toHaveTextContent('sent as a file path')
    enter()
    await waitFor(() => expect(api.sendMessage).toHaveBeenCalledTimes(1))
    const [, , , text, attachments] = vi.mocked(api.sendMessage).mock.calls[0]
    expect(text).toBe(`[file: ${saved('6.png')}]`)
    expect(attachments).toHaveLength(5)
  })

  it('text typed after planning that pushes the body over max_request_bytes blocks the send with a reason (Review Focus 3)', async () => {
    const limit = requestBytes('', [{ size: PNG.length, type: 'image/png' }]) + 4
    seedCaps(imageCaps(), limit)
    render(<ExecutionView {...base} isActive />)
    dropFiles([png('p.png')])
    expect(chips()[0].dataset.status).toBe('done')
    type('this draft is now far too long')
    enter()
    await act(async () => {})
    expect(api.sendMessage).not.toHaveBeenCalled()
    const reason = screen.getByTestId('send-block')
    expect(reason).toHaveAttribute('role', 'status')
    expect(reason).toHaveTextContent('The message is too large to send')
    expect(chips()).toHaveLength(1)
    expect((screen.getByRole('textbox') as HTMLTextAreaElement).value).toBe('this draft is now far too long')
    // Shortened, it goes.
    type('ok')
    enter()
    await waitFor(() => expect(api.sendMessage).toHaveBeenCalledTimes(1))
    expect(screen.queryByTestId('send-block')).toBeNull()
  })

  it('the optimistic line owns fresh preview URLs: kept when the chips clear, revoked when pendingLocal clears', async () => {
    seedCaps(imageCaps())
    render(<ExecutionView {...base} isActive />)
    dropFiles([png('p.png')])
    const chipUrl = 'blob:1'
    enter()
    await waitFor(() => expect(chips()).toHaveLength(0))
    // The chip's thumbnail went with the chip …
    expect(revokeUrl).toHaveBeenCalledWith(chipUrl)
    const pending = useExecutionStore.getState().executions[`${H}:${E}`].pendingLocal
    expect(pending?.attachments).toEqual([{ previewUrl: 'blob:2', media_type: 'image/png' }])
    // … but the pending line's own URL is still alive.
    expect(revokeUrl).not.toHaveBeenCalledWith('blob:2')
    act(() => useExecutionStore.getState().setPendingLocal(H, E, null))
    expect(revokeUrl.mock.calls.filter(([u]) => u === 'blob:2')).toHaveLength(1)
  })

  it('two panes on one execution: unmounting either leaves the pending previews alive; clearing pendingLocal revokes once', async () => {
    seedCaps(imageCaps())
    const other = render(<ExecutionView {...base} paneId="p2" isActive={false} />)
    const sender = render(<ExecutionView {...base} isActive />)
    const scope = sender.container
    const root = scope.querySelector('[data-testid="execution-view"]') as HTMLElement
    fireEvent.dragEnter(root, { dataTransfer: { types: ['Files'], files: [png('p.png')] } })
    fireEvent.drop(root, { dataTransfer: { types: ['Files'], files: [png('p.png')] } })
    fireEvent.keyDown(scope.querySelector('textarea') as HTMLElement, { key: 'Enter' })
    await waitFor(() => expect(useExecutionStore.getState().executions[`${H}:${E}`].pendingLocal?.attachments).toEqual([{ previewUrl: 'blob:2', media_type: 'image/png' }]))
    other.unmount()
    expect(revokeUrl).not.toHaveBeenCalledWith('blob:2')
    // The sending pane still draws its pending thumbnail from the live URL.
    expect(within(scope).getByTestId('attachment-thumb').querySelector('img')).toHaveAttribute('src', 'blob:2')
    sender.unmount()
    expect(revokeUrl).not.toHaveBeenCalledWith('blob:2')
    act(() => useExecutionStore.getState().setPendingLocal(H, E, null))
    expect(revokeUrl.mock.calls.filter(([u]) => u === 'blob:2')).toHaveLength(1)
  })

  it('a failed send revokes its pending previews exactly once', async () => {
    seedCaps(imageCaps())
    vi.mocked(api.sendMessage).mockRejectedValueOnce(new NexApiError(400, 'attachments_too_large', 'too much'))
    render(<ExecutionView {...base} isActive />)
    dropFiles([png('a.png')])
    enter()
    await waitFor(() => expect(screen.getByTestId('send-error')).toBeInTheDocument())
    expect(useExecutionStore.getState().executions[`${H}:${E}`].pendingLocal).toBeNull()
    expect(revokeUrl.mock.calls.filter(([u]) => u === 'blob:2')).toHaveLength(1)
  })

  it('attachment_too_large at index 1 fails the second native chip and keeps the draft', async () => {
    seedCaps(imageCaps())
    vi.mocked(api.sendMessage).mockRejectedValueOnce(new NexApiError(400, 'attachment_too_large', 'big', undefined, 1))
    render(<ExecutionView {...base} isActive />)
    dropFiles([png('a.png'), txt('t.txt'), png('b.png')])
    await waitFor(() => expect(chips().map((c) => c.dataset.status)).toEqual(['done', 'done', 'done']))
    type('two pics')
    enter()
    await waitFor(() => expect(chips().map((c) => c.dataset.status)).toEqual(['done', 'done', 'failed']))
    expect(chips()[2]).toHaveTextContent('Image too large')
    expect((screen.getByRole('textbox') as HTMLTextAreaElement).value).toBe('two pics')
    // The failed send's own previews are gone.
    expect(useExecutionStore.getState().executions[`${H}:${E}`].pendingLocal).toBeNull()
  })

  it('a whole-batch attachment error shows its own copy', async () => {
    seedCaps(imageCaps())
    vi.mocked(api.sendMessage).mockRejectedValueOnce(new NexApiError(400, 'attachments_too_large', 'too much'))
    render(<ExecutionView {...base} isActive />)
    dropFiles([png('a.png')])
    type('x')
    enter()
    await waitFor(() => expect(screen.getByTestId('send-error')).toHaveTextContent('Images too large in total'))
    expect(chips()).toHaveLength(1)
  })

  it('capabilities gone by send time (daemon downgraded): native chips fall back to path and nothing is sent yet', async () => {
    seedCaps(imageCaps())
    render(<ExecutionView {...base} isActive />)
    dropFiles([png('p.png')])
    expect(api.uploadWorkerFile).not.toHaveBeenCalled()
    act(() => seedCaps(null))
    type('hi')
    enter()
    await act(async () => {})
    expect(api.sendMessage).not.toHaveBeenCalled()
    await waitFor(() => expect(api.uploadWorkerFile).toHaveBeenCalledTimes(1))
    await waitFor(() => expect(chips()[0].dataset.status).toBe('done'))
    expect((screen.getByRole('textbox') as HTMLTextAreaElement).value).toBe('hi')
    enter()
    await waitFor(() => expect(api.sendMessage).toHaveBeenCalledWith(H, E, 'ls_1', `hi\n\n[file: ${saved('p.png')}]`))
  })
  it('a chip removed while its image is still encoding is not sent (PR #1527 A1)', async () => {
    seedCaps(imageCaps())
    const reads = holdReads()
    const B = [0x89, 0x50, 0x4e, 0x47, 9, 9, 9]
    render(<ExecutionView {...base} isActive />)
    dropFiles([png('a.png'), png('b.png', B)])
    type('two')
    enter()
    await waitFor(() => expect(reads.count).toBe(2))
    fireEvent.click(screen.getByRole('button', { name: 'Remove b.png' }))
    act(() => reads.flush())
    await waitFor(() => expect(api.sendMessage).toHaveBeenCalledTimes(1))
    expect(api.sendMessage).toHaveBeenCalledWith(H, E, 'ls_1', 'two', [{ type: 'image', media_type: 'image/png', data: b64(PNG) }])
    // Only the kept image got an optimistic preview.
    expect(useExecutionStore.getState().executions[`${H}:${E}`].pendingLocal?.attachments).toHaveLength(1)
  })

  it('removing the only image mid-encode of an image-only send posts nothing (PR #1527 A1)', async () => {
    seedCaps(imageCaps())
    const reads = holdReads()
    render(<ExecutionView {...base} isActive />)
    dropFiles([png('a.png')])
    enter()
    await waitFor(() => expect(reads.count).toBe(1))
    fireEvent.click(screen.getByRole('button', { name: 'Remove a.png' }))
    await act(async () => { reads.flush(); await new Promise((r) => setTimeout(r, 20)) })
    expect(api.sendMessage).not.toHaveBeenCalled()
    expect(useExecutionStore.getState().executions[`${H}:${E}`].pendingLocal ?? null).toBeNull()
  })

  it('a path chip removed while another image encodes loses its [file:] line (PR #1527 A1)', async () => {
    seedCaps(imageCaps())
    render(<ExecutionView {...base} isActive />)
    dropFiles([png('a.png'), txt('t.txt')])
    await waitFor(() => expect(chips().map((c) => c.dataset.status)).toEqual(['done', 'done']))
    const reads = holdReads()
    type('go')
    enter()
    await waitFor(() => expect(reads.count).toBe(1))
    fireEvent.click(screen.getByRole('button', { name: 'Remove t.txt' }))
    act(() => reads.flush())
    await waitFor(() => expect(api.sendMessage).toHaveBeenCalledWith(H, E, 'ls_1', 'go', [{ type: 'image', media_type: 'image/png', data: b64(PNG) }]))
  })
  it('attachments_unsupported: the capability cache is invalidated and the images go by path; the resend has no attachments (PR #1527 A2)', async () => {
    seedCaps(imageCaps())
    const invalidate = vi.fn(async () => {})
    useNexHostStore.setState({ invalidate })
    vi.mocked(api.sendMessage).mockRejectedValueOnce(new NexApiError(400, 'attachments_unsupported', 'no images'))
    let finishUpload: () => void = () => {}
    vi.mocked(api.uploadWorkerFile).mockImplementationOnce((_h, _e, f) => new Promise((r) => { finishUpload = () => r({ path: saved(f.name), name: f.name, size: 1 }) }))
    render(<ExecutionView {...base} isActive />)
    dropFiles([png('a.png')])
    expect(chips()[0].dataset.status).toBe('done')
    type('look')
    enter()
    await waitFor(() => expect(api.sendMessage).toHaveBeenCalledTimes(1))
    expect(vi.mocked(api.sendMessage).mock.calls[0][4]).toHaveLength(1)
    // The chip is re-uploaded by path; the draft stays.
    await waitFor(() => expect(chips()[0].dataset.status).toBe('uploading'))
    expect(invalidate).toHaveBeenCalledWith(H)
    expect(api.uploadWorkerFile).toHaveBeenCalledTimes(1)
    expect((screen.getByRole('textbox') as HTMLTextAreaElement).value).toBe('look')
    await act(async () => finishUpload())
    await waitFor(() => expect(chips()[0].dataset.status).toBe('done'))
    expect(screen.getByTestId('upload-chip-note')).toHaveTextContent('sent as a file path')
    enter()
    await waitFor(() => expect(api.sendMessage).toHaveBeenCalledTimes(2))
    expect(vi.mocked(api.sendMessage).mock.calls[1]).toEqual([H, E, 'ls_1', `look\n\n[file: ${saved('a.png')}]`])
  })

  it('request_too_large from the daemon keeps the chips and shows the reason, no demotion (PR #1527 A2)', async () => {
    seedCaps(imageCaps())
    const invalidate = vi.fn(async () => {})
    useNexHostStore.setState({ invalidate })
    vi.mocked(api.sendMessage).mockRejectedValueOnce(new NexApiError(413, 'request_too_large', 'too big'))
    render(<ExecutionView {...base} isActive />)
    dropFiles([png('a.png')])
    type('x')
    enter()
    await waitFor(() => expect(screen.getByTestId('send-error')).toHaveTextContent('Message too large'))
    expect(chips().map((c) => c.dataset.status)).toEqual(['done'])
    expect(api.uploadWorkerFile).not.toHaveBeenCalled()
    expect(invalidate).not.toHaveBeenCalled()
    expect(api.sendMessage).toHaveBeenCalledTimes(1)
  })
  it('while images encode, the quick replies and the input are disabled (PR #1527 A3)', async () => {
    seedCaps(imageCaps())
    const e = emptyHostConfigEntry('ready')
    useHostConfigStore.setState({ byHost: { [H]: { ...e, quickReplies: [{ id: 'go', text: 'go on' }], quickRepliesSupported: true, revisions: { ...e.revisions, quickReplies: 1 } } } })
    const reads = holdReads()
    render(<ExecutionView {...base} isActive />)
    const reply = () => screen.getByTestId('quick-reply')
    expect(reply()).not.toBeDisabled()
    dropFiles([png('a.png')])
    enter()
    await waitFor(() => expect(reads.count).toBe(1))
    expect(reply()).toBeDisabled()
    expect(screen.getByRole('textbox')).toBeDisabled()
    // The chip can still be removed meanwhile (A1); with nothing left the
    // send is dropped, and the gate lifts once the encode settles.
    fireEvent.click(screen.getByRole('button', { name: 'Remove a.png' }))
    expect(reply()).toBeDisabled()
    act(() => reads.flush())
    await waitFor(() => expect(reply()).not.toBeDisabled())
    expect(screen.getByRole('textbox')).not.toBeDisabled()
    expect(api.sendMessage).not.toHaveBeenCalled()
  })
})

describe('ExecutionView — image attachments on user lines (E4)', () => {
  const ROUTE = { method: 'GET', path: '/api/nex/v1/executions/{id}/attachments/{sha256}' }
  const sha = (c: string) => c.repeat(64)
  const accepted = (seq: number, text: string, attachments: unknown[]) =>
    ({ seq, execution_id: E, kind: 'execution.message_accepted', payload: { turn_id: `t${seq}`, text, attachments }, created_at: 0 })
  const lineIn = (mode: 'room' | 'chat') => mode === 'room' ? screen.getAllByTestId('room-user-line') : screen.getAllByTestId('chat-bubble-user')

  it.each(['room', 'chat'] as const)('an image-only message renders its line with the thumbnail in %s (Review Focus 4)', async (mode) => {
    seedCaps(imageCaps())
    useExecutionStore.getState().applyEvents(H, E, [accepted(1, '', [{ media_type: 'image/png', bytes: 11, sha256: sha('a') }])])
    render(<ExecutionView {...base} mode={mode} isActive />)
    const lines = lineIn(mode)
    expect(lines).toHaveLength(1)
    await waitFor(() => expect(within(lines[0]).getByTestId('attachment-thumb').dataset.state).toBe('ready'))
    expect(api.fetchAttachment).toHaveBeenCalledWith(H, E, sha('a'), ROUTE, expect.any(AbortSignal))
    expect(within(lines[0]).getByRole('img')).toHaveAttribute('src', 'blob:1')
  })

  it.each(['room', 'chat'] as const)('a line with text and two images shows the text and one thumbnail per image in %s', async (mode) => {
    seedCaps(imageCaps())
    useExecutionStore.getState().applyEvents(H, E, [accepted(1, 'compare these', [
      { media_type: 'image/png', bytes: 11, sha256: sha('a') }, { media_type: 'image/jpeg', bytes: 12, sha256: sha('b') },
    ])])
    render(<ExecutionView {...base} mode={mode} isActive />)
    const [line] = lineIn(mode)
    expect(line).toHaveTextContent('compare these')
    expect(within(line).getAllByTestId('attachment-thumb')).toHaveLength(2)
    await waitFor(() => expect(api.fetchAttachment).toHaveBeenCalledTimes(2))
  })

  it.each(['room', 'chat'] as const)('a 404 shows the broken-image placeholder with the media type in %s (Review Focus 5)', async (mode) => {
    seedCaps(imageCaps())
    vi.mocked(api.fetchAttachment).mockRejectedValue(new NexApiError(404, 'attachment_not_found', 'gone'))
    useExecutionStore.getState().applyEvents(H, E, [accepted(1, '', [{ media_type: 'image/gif', bytes: 11, sha256: sha('a') }])])
    render(<ExecutionView {...base} mode={mode} isActive />)
    const thumb = () => within(lineIn(mode)[0]).getByTestId('attachment-thumb')
    await waitFor(() => expect(thumb().dataset.state).toBe('error'))
    expect(thumb()).toHaveTextContent('image/gif')
  })

  it.each(['room', 'chat'] as const)('the optimistic line shows its local previews in %s, without fetching', (mode) => {
    seedCaps(imageCaps())
    useExecutionStore.getState().setPendingLocal(H, E, { text: '', delivery: null, attachments: [{ previewUrl: 'blob:local', media_type: 'image/png' }] })
    render(<ExecutionView {...base} mode={mode} isActive />)
    const [line] = lineIn(mode)
    expect(within(line).getByRole('img')).toHaveAttribute('src', 'blob:local')
    expect(api.fetchAttachment).not.toHaveBeenCalled()
  })
})
