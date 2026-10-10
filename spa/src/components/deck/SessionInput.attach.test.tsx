// Attachments in the deck / chat input: paste, drop and the attach button upload through agentUploadToPath (save only), put the
// path in the draft, and show a removable chip; Enter is held back while an upload is still running.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { act, cleanup, fireEvent, render, screen } from '@testing-library/react'
import { SessionInput } from './SessionInput'
import { clearAllDrafts, draftKey, readDraft, writeDraft } from '../../lib/conversations/draft-memory'
import { clearAllAttachments, readAttachments } from '../../lib/conversations/attachment-memory'
import { clearAllSendQueues } from '../../lib/conversations/send-queue'

const mocks = vi.hoisted(() => ({ fetch: vi.fn(), upload: vi.fn() }))
vi.mock('../../lib/host-api', () => ({ pinnedHostFetch: mocks.fetch, agentUploadToPath: mocks.upload }))

const SID = '11111111-2222-4333-8444-555555555555'
const KEY = draftKey('p1', 'h', SID)
const ui = () => <SessionInput paneKey="p1" hostId="h" sessionId={SID} sessionCode="dev001" capabilities={{ send: 'prompt' }} onSwitchToTerminal={() => {}} />
const box = () => screen.getByRole('textbox') as HTMLTextAreaElement
const png = () => new File(['x'], 'shot.png', { type: 'image/png' })
const pdf = () => new File(['x'], 'doc.pdf', { type: 'application/pdf' })
const paste = (data: { files?: File[]; text?: string }) => {
  const clipboardData = { files: data.files ?? [], getData: () => data.text ?? '', types: data.files?.length ? ['Files'] : ['text/plain'] }
  return fireEvent.paste(box(), { clipboardData })
}
const kindErr = (kind: string, status = 0) => Object.assign(new Error(kind), { kind, status })

/** An upload that is settled by hand. */
function pendingUpload() {
  let resolve!: (v: { filename: string; path: string }) => void, reject!: (e: unknown) => void
  let onProgress: ((p: number) => void) | undefined
  mocks.upload.mockImplementationOnce((_h: string, _f: File, _s: string, o: { onProgress?: (p: number) => void }) => {
    onProgress = o?.onProgress
    return new Promise((res, rej) => { resolve = res; reject = rej })
  })
  return { resolve: (v: { filename: string; path: string }) => resolve(v), reject: (e: unknown) => reject(e), progress: (p: number) => onProgress?.(p) }
}
const flush = () => act(async () => { await Promise.resolve(); await Promise.resolve() })

beforeEach(() => {
  mocks.fetch.mockReset().mockImplementation(() => Promise.resolve(new Response(JSON.stringify({ status: 'accepted' }))))
  mocks.upload.mockReset().mockImplementation((_h: string, f: File) => Promise.resolve({ filename: f.name, path: `/up/dev001/${f.name}` }))
})
afterEach(() => { cleanup(); clearAllDrafts(); clearAllAttachments(); clearAllSendQueues() })

describe('SessionInput attachments', () => {
  it('pasting an image uploads it to the tmux session code and writes [Image: source: path] into the draft', async () => {
    render(ui())
    paste({ files: [png()] })
    await flush()
    expect(mocks.upload).toHaveBeenCalledTimes(1)
    const [hostId, file, code] = mocks.upload.mock.calls[0]
    expect([hostId, (file as File).name, code]).toEqual(['h', 'shot.png', 'dev001'])
    expect(box().value).toBe('[Image: source: /up/dev001/shot.png]')
    expect(readDraft(KEY)).toBe('[Image: source: /up/dev001/shot.png]')
    expect(screen.getByTestId('attachment-chip')).toHaveTextContent('Attached shot.png')
  })

  it('a non-image file puts only its path, on a new line after existing text', async () => {
    render(ui())
    fireEvent.change(box(), { target: { value: 'see this' } })
    paste({ files: [pdf()] })
    await flush()
    expect(box().value).toBe('see this\n/up/dev001/doc.pdf')
  })

  it('plain text paste is not intercepted', () => {
    render(ui())
    const notPrevented = paste({ text: 'just text' })
    expect(notPrevented).toBe(true)
    expect(mocks.upload).not.toHaveBeenCalled()
  })

  it('a file paste is intercepted (the browser does not also paste it)', () => {
    render(ui())
    expect(paste({ files: [png()] })).toBe(false)
  })

  it('dropping files uploads them', async () => {
    render(ui())
    fireEvent.drop(screen.getByTestId('session-input-drop'), { dataTransfer: { files: [png(), pdf()], types: ['Files'] } })
    await flush()
    expect(mocks.upload).toHaveBeenCalledTimes(2)
    expect(box().value).toBe('[Image: source: /up/dev001/shot.png]\n/up/dev001/doc.pdf')
    expect(screen.getAllByTestId('attachment-chip')).toHaveLength(2)
  })

  it('the attach button picks files', async () => {
    render(ui())
    const input = screen.getByTestId('attach-file-input') as HTMLInputElement
    fireEvent.change(input, { target: { files: [pdf()] } })
    await flush()
    expect(box().value).toBe('/up/dev001/doc.pdf')
    expect(screen.getByRole('button', { name: 'Attach files' })).toBeInTheDocument()
  })

  it('while uploading it shows progress, and Enter is held back with a hint (nothing is queued)', async () => {
    const up = pendingUpload()
    render(ui())
    fireEvent.change(box(), { target: { value: 'hello' } })
    paste({ files: [png()] })
    await flush()
    act(() => up.progress(40))
    expect(screen.getByTestId('attachment-uploading')).toHaveTextContent('shot.png')
    expect(screen.getByTestId('attachment-uploading')).toHaveTextContent('40%')

    fireEvent.keyDown(box(), { key: 'Enter' })
    expect(screen.getByTestId('session-input-hint')).toHaveTextContent('still uploading')
    expect(screen.queryByTestId('queued-message')).toBeNull()
    expect(box().value).toBe('hello')
    fireEvent.click(screen.getByRole('button', { name: 'Send' }))
    expect(screen.queryByTestId('queued-message')).toBeNull()

    up.resolve({ filename: 'shot.png', path: '/up/dev001/shot.png' })
    await flush()
    expect(screen.queryByTestId('attachment-uploading')).toBeNull()
    expect(box().value).toBe('hello\n[Image: source: /up/dev001/shot.png]')
    fireEvent.keyDown(box(), { key: 'Enter' })
    expect(screen.getByTestId('queued-message')).toHaveTextContent('[Image: source: /up/dev001/shot.png]')
  })

  it('the user can keep typing while an upload runs, and the text is not lost when it lands', async () => {
    const up = pendingUpload()
    render(ui())
    paste({ files: [png()] })
    await flush()
    fireEvent.change(box(), { target: { value: 'typed meanwhile' } })
    up.resolve({ filename: 'shot.png', path: '/up/dev001/shot.png' })
    await flush()
    expect(box().value).toBe('typed meanwhile\n[Image: source: /up/dev001/shot.png]')
  })

  it.each([
    [kindErr('too_large', 413), 'File too large', '256'],
    [kindErr('not_found', 404), 'Session not found', ''],
    [kindErr('http', 500), 'Upload failed', '500'],
    [kindErr('network'), 'Connection lost', ''],
  ])('maps an upload error to a visible message (%#)', async (err, text, extra) => {
    mocks.upload.mockRejectedValueOnce(err)
    render(ui())
    paste({ files: [png()] })
    await flush()
    const hint = screen.getByTestId('session-input-hint')
    expect(hint).toHaveTextContent(text)
    if (extra) expect(hint).toHaveTextContent(extra)
    expect(box().value).toBe('')
    expect(screen.queryByTestId('attachment-chip')).toBeNull()
    expect(screen.queryByTestId('attachment-uploading')).toBeNull()
  })

  it('removing a chip takes only its text out of the draft and deletes nothing on the server', async () => {
    render(ui())
    fireEvent.change(box(), { target: { value: 'keep me' } })
    paste({ files: [png()] })
    await flush()
    mocks.fetch.mockClear()
    fireEvent.click(screen.getByRole('button', { name: /Remove attachment/ }))
    expect(screen.queryByTestId('attachment-chip')).toBeNull()
    expect(box().value).toBe('keep me')
    expect(readDraft(KEY)).toBe('keep me')
    expect(readAttachments(KEY)).toEqual([])
    expect(mocks.fetch).not.toHaveBeenCalled()
  })

  it('chips and the draft come back from the module memory after a remount', async () => {
    const first = render(ui())
    paste({ files: [png()] })
    await flush()
    first.unmount()
    render(ui())
    expect(box().value).toBe('[Image: source: /up/dev001/shot.png]')
    expect(screen.getByTestId('attachment-chip')).toHaveTextContent('shot.png')
    expect(readAttachments(KEY)).toHaveLength(1)
  })

  it('sending clears the chips along with the draft', async () => {
    render(ui())
    paste({ files: [png()] })
    await flush()
    fireEvent.keyDown(box(), { key: 'Enter' })
    expect(screen.queryByTestId('attachment-chip')).toBeNull()
    expect(readAttachments(KEY)).toEqual([])
  })

  it('without a session code there is no attach affordance, and a paste is left alone', () => {
    writeDraft(KEY, '')
    render(<SessionInput paneKey="p1" hostId="h" sessionId={SID} capabilities={{ send: 'prompt' }} onSwitchToTerminal={() => {}} />)
    expect(screen.queryByTestId('attach-file-input')).toBeNull()
    expect(paste({ files: [png()] })).toBe(true)
    expect(mocks.upload).not.toHaveBeenCalled()
  })
})
