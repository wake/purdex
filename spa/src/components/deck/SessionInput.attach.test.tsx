// Attachments in the deck / chat input: paste, drop and the attach button upload through agentUploadToPath (save only), put the
// path in the draft, and show a removable chip; Enter is held back while an upload is still running.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { act, cleanup, fireEvent, render, screen } from '@testing-library/react'
import { SessionInput } from './SessionInput'
import { clearAllDrafts, draftKey, readDraft, writeDraft } from '../../lib/conversations/draft-memory'
import { clearAllAttachments, readAttachments } from '../../lib/conversations/attachment-memory'
import { clearAllUploads } from '../../lib/conversations/attachment-upload'
import { clearAllSendQueues } from '../../lib/conversations/send-queue'
import { releasePane, retireStaleSessions } from '../../lib/conversations/pane-release'

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
afterEach(() => { cleanup(); clearAllUploads(); clearAllDrafts(); clearAllAttachments(); clearAllSendQueues() })

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

  it('a payload with files AND text keeps its text (not prevented) and still uploads the files', async () => {
    render(ui())
    expect(paste({ files: [png()], text: 'caption' })).toBe(true)
    await flush()
    expect(mocks.upload).toHaveBeenCalledTimes(1)
    expect(box().value).toBe('[Image: source: /up/dev001/shot.png]')
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

  it('a chip follows the draft: delete the marker line by hand and the chip (and its memory) goes', async () => {
    render(ui())
    paste({ files: [png()] })
    await flush()
    fireEvent.change(box(), { target: { value: 'rewritten' } })
    expect(screen.queryByTestId('attachment-chip')).toBeNull()
    expect(readAttachments(KEY)).toEqual([])
  })

  it('two identical markers: removing one chip takes out one line and leaves the other chip', async () => {
    mocks.upload.mockImplementation(() => Promise.resolve({ filename: 'shot.png', path: '/up/dev001/shot.png' }))
    render(ui())
    paste({ files: [png()] }); paste({ files: [png()] })
    await flush()
    expect(box().value).toBe('[Image: source: /up/dev001/shot.png]\n[Image: source: /up/dev001/shot.png]')
    expect(screen.getAllByTestId('attachment-chip')).toHaveLength(2)
    fireEvent.click(screen.getAllByRole('button', { name: /Remove attachment/ })[0])
    expect(box().value).toBe('[Image: source: /up/dev001/shot.png]')
    expect(screen.getAllByTestId('attachment-chip')).toHaveLength(1)
  })

  it('leaving the pane does not cancel an upload: it lands in the memories and the next mount shows it', async () => {
    const up = pendingUpload()
    const view = render(ui())
    paste({ files: [png()] })
    await flush()
    view.unmount()
    up.resolve({ filename: 'shot.png', path: '/up/dev001/shot.png' })
    await flush()
    expect(readDraft(KEY)).toBe('[Image: source: /up/dev001/shot.png]')
    expect(readAttachments(KEY)).toHaveLength(1)
    render(ui())
    expect(box().value).toBe('[Image: source: /up/dev001/shot.png]')
    expect(screen.getByTestId('attachment-chip')).toBeInTheDocument()
  })

  it('a remounted input still shows the upload on its way, and still holds Enter back', async () => {
    const up = pendingUpload()
    const view = render(ui())
    fireEvent.change(box(), { target: { value: 'hi' } })
    paste({ files: [png()] })
    await flush()
    view.unmount()
    render(ui())
    act(() => up.progress(70))
    expect(screen.getByTestId('attachment-uploading')).toHaveTextContent('70%')
    fireEvent.keyDown(box(), { key: 'Enter' })
    expect(screen.getByTestId('session-input-hint')).toHaveTextContent('still uploading')
    up.resolve({ filename: 'shot.png', path: '/up/dev001/shot.png' })
    await flush()
    expect(screen.queryByTestId('attachment-uploading')).toBeNull()
    expect(box().value).toBe('hi\n[Image: source: /up/dev001/shot.png]') // the live input picked the landing up
  })

  it('a failure while the input was away is shown when it comes back', async () => {
    const up = pendingUpload()
    const view = render(ui())
    paste({ files: [png()] })
    await flush()
    view.unmount()
    up.reject(kindErr('too_large', 413))
    await flush()
    render(ui())
    expect(screen.getByTestId('session-input-hint')).toHaveTextContent('File too large')
  })

  it('a release aborts a running upload (away or not) and what lands after it is not written back', async () => {
    const up = pendingUpload()
    const view = render(ui())
    paste({ files: [png()] })
    await flush()
    view.unmount()
    releasePane('p1')
    up.resolve({ filename: 'shot.png', path: '/up/dev001/shot.png' })
    await flush()
    expect(readDraft(KEY)).toBeUndefined()
    expect(readAttachments(KEY)).toEqual([])
    render(ui())
    expect(screen.queryByTestId('attachment-uploading')).toBeNull()
  })

  it('an upload that lands after the pane was released (still mounted) rebuilds nothing', async () => {
    render(ui())
    paste({ files: [png()] })
    releasePane('p1')
    await flush()
    expect(readDraft(KEY)).toBeUndefined()
    expect(readAttachments(KEY)).toEqual([])
  })

  it('an upload that lands after the pane moved to another session rebuilds nothing for the old one', async () => {
    render(ui())
    paste({ files: [png()] })
    retireStaleSessions('p1', 'h', 'other-session')
    await flush()
    expect(readDraft(KEY)).toBeUndefined()
    expect(readAttachments(KEY)).toEqual([])
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

  describe('drag highlight', () => {
    const zone = () => screen.getByTestId('session-input-drop')
    const files = { types: ['Files'], files: [] as File[] }
    const lit = () => zone().getAttribute('data-dragging')
    it('lights up for files, stays lit between children, goes out when the last leave comes', () => {
      render(ui())
      expect(lit()).toBe('false')
      fireEvent.dragEnter(zone(), { dataTransfer: files })
      expect(lit()).toBe('true')
      expect(zone().className).toContain('border-dashed')
      fireEvent.dragEnter(box(), { dataTransfer: files }) // into the textarea
      fireEvent.dragLeave(zone(), { dataTransfer: files }) // out of the wrapper
      expect(lit()).toBe('true')
      fireEvent.dragLeave(box(), { dataTransfer: files })
      expect(lit()).toBe('false')
    })
    it('the box itself takes the drop style over its focus ring, and gives it back afterwards', () => {
      render(ui())
      box().focus()
      expect(document.activeElement).toBe(box())
      expect(box().className).not.toContain('border-dashed')
      fireEvent.dragEnter(zone(), { dataTransfer: files })
      for (const c of ['border-dashed', 'border-accent', 'bg-accent/10', 'outline-none']) expect(box().className).toContain(c)
      fireEvent.dragLeave(zone(), { dataTransfer: files })
      expect(box().className).not.toContain('border-dashed')
      expect(box().className).not.toContain('outline-none')
      fireEvent.dragEnter(zone(), { dataTransfer: files })
      fireEvent.drop(zone(), { dataTransfer: files })
      expect(box().className).not.toContain('border-dashed')
      expect(box().className).toContain('border-border-subtle')
      expect(document.activeElement).toBe(box()) // focus itself was never taken away
    })
    it('drop clears it', () => {
      render(ui())
      fireEvent.dragEnter(zone(), { dataTransfer: files })
      fireEvent.drop(zone(), { dataTransfer: files })
      expect(lit()).toBe('false')
    })
    it('Esc clears it', () => {
      render(ui())
      fireEvent.dragEnter(zone(), { dataTransfer: files })
      fireEvent.keyDown(window, { key: 'Escape' })
      expect(lit()).toBe('false')
    })
    it('a text drag does not light it, nor does a missing session code', () => {
      render(ui())
      fireEvent.dragEnter(zone(), { dataTransfer: { types: ['text/plain'], files: [] } })
      expect(lit()).toBe('false')
      cleanup()
      render(<SessionInput paneKey="p1" hostId="h" sessionId={SID} capabilities={{ send: 'prompt' }} onSwitchToTerminal={() => {}} />)
      fireEvent.dragEnter(zone(), { dataTransfer: files })
      expect(lit()).toBe('false')
    })
  })

  describe('box height', () => {
    let scrollHeight = 0
    beforeEach(() => {
      vi.spyOn(HTMLTextAreaElement.prototype, 'scrollHeight', 'get').mockImplementation(() => scrollHeight)
    })
    afterEach(() => vi.restoreAllMocks())
    it('is at least 2 lines, follows the content, and stops at 8 lines with a scrollbar', () => {
      scrollHeight = 10
      render(ui())
      expect(box().style.height).toBe('40px') // 2 lines of the 20px fallback
      scrollHeight = 100
      fireEvent.change(box(), { target: { value: 'a\nb\nc\nd\ne' } })
      expect(box().style.height).toBe('100px')
      expect(box().style.overflowY).toBe('hidden')
      scrollHeight = 400
      fireEvent.change(box(), { target: { value: 'x'.repeat(50) } })
      expect(box().style.height).toBe('160px') // 8 lines
      expect(box().style.overflowY).toBe('auto')
    })
    it('long paths wrap anywhere', () => {
      render(ui())
      expect(box().style.overflowWrap).toBe('anywhere')
    })
  })

  it('without a session code there is no attach affordance, and a paste is left alone', () => {
    writeDraft(KEY, '')
    render(<SessionInput paneKey="p1" hostId="h" sessionId={SID} capabilities={{ send: 'prompt' }} onSwitchToTerminal={() => {}} />)
    expect(screen.queryByTestId('attach-file-input')).toBeNull()
    expect(paste({ files: [png()] })).toBe(true)
    expect(mocks.upload).not.toHaveBeenCalled()
  })
})
