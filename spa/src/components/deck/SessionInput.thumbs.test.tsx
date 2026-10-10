// v3 pt 8: the input's attachment chips are thumbnails (a picture for an image, an icon and a short name for any other file);
// resting on one floats a larger preview, a click opens the lightbox, and every blob: URL made for them is revoked when it is let go.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { act, cleanup, fireEvent, render, screen, within } from '@testing-library/react'
import { SessionInput } from './SessionInput'
import { clearAllDrafts, draftKey, readDraft, writeDraft } from '../../lib/conversations/draft-memory'
import { addAttachment, clearAllAttachments, readAttachments } from '../../lib/conversations/attachment-memory'
import { clearAllUploads } from '../../lib/conversations/attachment-upload'
import { clearFsImages } from '../../lib/conversations/fs-image'
import { clearAllSendQueues } from '../../lib/conversations/send-queue'
import { releasePane } from '../../lib/conversations/pane-release'
import { PNG_BYTES } from '../../lib/conversations/image-fixtures'

const mocks = vi.hoisted(() => ({ fetch: vi.fn(), upload: vi.fn(), read: vi.fn() }))
vi.mock('../../lib/host-api', () => ({ pinnedHostFetch: mocks.fetch, agentUploadToPath: mocks.upload }))
vi.mock('../../lib/fs-backend-daemon', () => ({ createDaemonBackendForHost: () => ({ read: mocks.read }) }))

const SID = '11111111-2222-4333-8444-555555555555'
const KEY = draftKey('p1', 'h', SID)
const ui = () => <SessionInput paneKey="p1" hostId="h" sessionId={SID} sessionCode="dev001" capabilities={{ send: 'prompt' }} onSwitchToTerminal={() => {}} />
const box = () => screen.getByRole('textbox') as HTMLTextAreaElement
const png = (name = 'shot.png') => new File(['x'], name, { type: 'image/png' })
const pdf = (name = 'doc.pdf') => new File(['x'], name, { type: 'application/pdf' })
const paste = (files: File[]) => fireEvent.paste(box(), { clipboardData: { files, getData: () => '', types: ['Files'] } })
const flush = () => act(async () => { await Promise.resolve(); await Promise.resolve(); await Promise.resolve() })

let made: string[] = []
let revoked: string[] = []
let seq = 0

function pendingUpload() {
  let resolve!: (v: { filename: string; path: string }) => void, reject!: (e: unknown) => void
  let onProgress: ((p: number) => void) | undefined
  mocks.upload.mockImplementationOnce((_h: string, _f: File, _s: string, o: { onProgress?: (p: number) => void }) => {
    onProgress = o?.onProgress
    return new Promise((res, rej) => { resolve = res; reject = rej })
  })
  return { resolve: (v: { filename: string; path: string }) => resolve(v), reject: (e: unknown) => reject(e), progress: (p: number) => onProgress?.(p) }
}

beforeEach(() => {
  made = []; revoked = []; seq = 0
  URL.createObjectURL = vi.fn(() => { const u = `blob:test/${++seq}`; made.push(u); return u })
  URL.revokeObjectURL = vi.fn((u: string) => { revoked.push(u) })
  mocks.fetch.mockReset().mockImplementation(() => Promise.resolve(new Response(JSON.stringify({ status: 'accepted' }))))
  mocks.upload.mockReset().mockImplementation((_h: string, f: File) => Promise.resolve({ filename: f.name, path: `/up/dev001/${f.name}` }))
  mocks.read.mockReset().mockRejectedValue(new Error('no read'))
})
afterEach(() => { vi.useRealTimers(); cleanup(); clearFsImages(); clearAllUploads(); clearAllDrafts(); clearAllAttachments(); clearAllSendQueues() })

describe('attachment thumbnails', () => {
  it('an image chip shows its picture (the local file, not a read from the host) and no "attached" text', async () => {
    render(ui())
    paste([png()])
    await flush()
    const chip = screen.getByTestId('attachment-chip')
    const img = within(chip).getByRole('img', { name: 'shot.png' })
    expect(img).toHaveAttribute('src', made[0])
    expect(chip).not.toHaveTextContent(/attached/i)
    expect(mocks.read).not.toHaveBeenCalled()
  })

  it('a non-image chip shows a file icon and the file name; a long name is cut in the middle and keeps its extension', async () => {
    render(ui())
    paste([pdf(), pdf('quarterly-financial-report-2026.pdf')])
    await flush()
    const chips = screen.getAllByTestId('attachment-chip')
    expect(within(chips[0]).queryByRole('img')).toBeNull()
    expect(chips[0].querySelector('svg')).not.toBeNull()
    expect(within(chips[0]).getByTestId('attachment-name')).toHaveTextContent('doc.pdf')
    const long = within(chips[1]).getByTestId('attachment-name').textContent ?? ''
    expect(long).toMatch(/^quarterly.*…\.pdf$/)
    expect(long.length).toBeLessThan('quarterly-financial-report-2026.pdf'.length)
  })

  it('the remove button only takes the chip and its draft text out; nothing is sent to the host', async () => {
    render(ui())
    paste([png()])
    await flush()
    fireEvent.click(screen.getByRole('button', { name: /Remove attachment shot\.png/ }))
    expect(screen.queryByTestId('attachment-chip')).toBeNull()
    expect(readDraft(KEY)).toBeUndefined()
    expect(mocks.fetch).not.toHaveBeenCalled()
    expect(mocks.read).not.toHaveBeenCalled()
  })

  it('an upload in flight shows the thumbnail under a veil with the percentage', async () => {
    const up = pendingUpload()
    render(ui())
    paste([png()])
    await flush()
    act(() => up.progress(40))
    const veil = screen.getByTestId('attachment-uploading')
    expect(veil).toHaveTextContent('40%')
    expect(veil).toHaveTextContent('Uploading shot.png 40%')
    expect(within(veil.parentElement as HTMLElement).getByRole('img', { name: 'shot.png' })).toHaveAttribute('src', made[0])
    up.resolve({ filename: 'shot.png', path: '/up/dev001/shot.png' })
    await flush()
    expect(screen.queryByTestId('attachment-uploading')).toBeNull()
    expect(screen.getByRole('img', { name: 'shot.png' })).toHaveAttribute('src', made[0]) // the same URL carried over
    expect(revoked).toEqual([])
  })
})

describe('hover preview', () => {
  const thumb = () => within(screen.getByTestId('attachment-chip')).getByTestId('attachment-thumb')

  it('floats after resting ~400 ms, shows the picture and the name, and goes when the pointer leaves', async () => {
    render(ui())
    paste([png()])
    await flush()
    vi.useFakeTimers()
    fireEvent.mouseEnter(thumb())
    act(() => { vi.advanceTimersByTime(399) })
    expect(screen.queryByTestId('attachment-hover-preview')).toBeNull()
    act(() => { vi.advanceTimersByTime(1) })
    const card = screen.getByTestId('attachment-hover-preview')
    expect(card).toHaveTextContent('shot.png')
    expect(card.querySelector('img')).toHaveAttribute('src', made[0])
    // portal + fixed + no pointer: never clipped by the area, never in the way of a click
    expect(card.parentElement).toBe(document.body)
    expect(card.style.position).toBe('fixed')
    expect(card.style.pointerEvents).toBe('none')
    fireEvent.mouseLeave(thumb())
    expect(screen.queryByTestId('attachment-hover-preview')).toBeNull()
  })

  it('leaving before the delay shows nothing', async () => {
    render(ui())
    paste([png()])
    await flush()
    vi.useFakeTimers()
    fireEvent.mouseEnter(thumb())
    act(() => { vi.advanceTimersByTime(200) })
    fireEvent.mouseLeave(thumb())
    act(() => { vi.advanceTimersByTime(1000) })
    expect(screen.queryByTestId('attachment-hover-preview')).toBeNull()
  })

  it('a non-image shows its whole name and its size', async () => {
    render(ui())
    paste([new File(['x'.repeat(2048)], 'quarterly-financial-report-2026.pdf', { type: 'application/pdf' })])
    await flush()
    vi.useFakeTimers()
    fireEvent.mouseEnter(thumb())
    act(() => { vi.advanceTimersByTime(400) })
    const card = screen.getByTestId('attachment-hover-preview')
    expect(card).toHaveTextContent('quarterly-financial-report-2026.pdf')
    expect(card).toHaveTextContent('2.0 KB')
    expect(card.querySelector('img')).toBeNull()
  })
})

describe('hover preview placement', () => {
  const thumb = () => within(screen.getByTestId('attachment-chip')).getByTestId('attachment-thumb')
  const place = (top: number) => {
    Object.defineProperty(window, 'innerHeight', { configurable: true, value: 800 })
    Object.defineProperty(window, 'innerWidth', { configurable: true, value: 1000 })
    vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockImplementation(() => ({ left: 500, top, width: 48, height: 48, right: 548, bottom: top + 48, x: 500, y: top, toJSON: () => ({}) }))
    vi.spyOn(HTMLElement.prototype, 'offsetWidth', 'get').mockReturnValue(376)
    vi.spyOn(HTMLElement.prototype, 'offsetHeight', 'get').mockReturnValue(300)
  }
  afterEach(() => { vi.restoreAllMocks() })
  async function hoverAt(top: number) {
    render(ui())
    paste([png()])
    await flush()
    place(top)
    vi.useFakeTimers()
    fireEvent.mouseEnter(thumb())
    act(() => { vi.advanceTimersByTime(400) })
    return screen.getByTestId('attachment-hover-preview')
  }

  it('floats above the thumbnail when there is room', async () => {
    const card = await hoverAt(600)
    expect(card.style.top).toBe(`${600 - 8 - 300}px`)
  })

  it('goes below it when the thumbnail is near the top of the window (the preview would be cut off)', async () => {
    const card = await hoverAt(100)
    expect(card.style.top).toBe(`${100 + 48 + 8}px`)
    expect(card.dataset.side).toBe('below')
  })
})

describe('lightbox', () => {
  async function open() {
    render(ui())
    paste([png()])
    await flush()
    const trigger = screen.getByRole('button', { name: 'View shot.png' })
    trigger.focus()
    fireEvent.click(trigger)
    return trigger
  }

  it('a click on the picture opens the original in a modal dialog and takes focus', async () => {
    await open()
    const dlg = screen.getByRole('dialog')
    expect(dlg).toHaveAttribute('aria-modal', 'true')
    expect(within(dlg).getByRole('img', { name: 'shot.png' })).toHaveAttribute('src', made[0])
    expect(screen.getByRole('button', { name: 'Close preview' })).toHaveFocus()
  })

  it('Esc closes it and focus goes back to the thumbnail', async () => {
    const trigger = await open()
    fireEvent.keyDown(document, { key: 'Escape' })
    expect(screen.queryByRole('dialog')).toBeNull()
    expect(trigger).toHaveFocus()
  })

  it('a click on the backdrop closes it; a click on the picture does not', async () => {
    const trigger = await open()
    fireEvent.click(within(screen.getByRole('dialog')).getByRole('img'))
    expect(screen.getByRole('dialog')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('dialog'))
    expect(screen.queryByRole('dialog')).toBeNull()
    expect(trigger).toHaveFocus()
  })

  it('Esc inside the lightbox does not reach the page behind it', async () => {
    await open()
    const behind = vi.fn()
    window.addEventListener('keydown', behind)
    fireEvent.keyDown(document.body, { key: 'Escape' })
    window.removeEventListener('keydown', behind)
    expect(behind).not.toHaveBeenCalled()
  })

  it('a non-image has nothing to open', async () => {
    render(ui())
    paste([pdf()])
    await flush()
    expect(screen.queryByRole('button', { name: /View doc\.pdf/ })).toBeNull()
  })
})

describe('blob: URLs are revoked', () => {
  it('when the chip is removed', async () => {
    render(ui())
    paste([png()])
    await flush()
    fireEvent.click(screen.getByRole('button', { name: /Remove attachment/ }))
    expect(revoked).toEqual(made)
  })

  it('when the message is sent', async () => {
    render(ui())
    paste([png()])
    await flush()
    fireEvent.keyDown(box(), { key: 'Enter' })
    expect(readAttachments(KEY)).toEqual([])
    expect(revoked).toEqual(made)
  })

  it('when the pane is released (chips, and an upload still on its way)', async () => {
    const up = pendingUpload()
    render(ui())
    paste([png('a.png'), png('b.png')]) // the first stays pending, the second lands
    await flush()
    expect(made).toHaveLength(2)
    releasePane('p1')
    expect([...revoked].sort()).toEqual([...made].sort())
    up.resolve({ filename: 'a.png', path: '/up/dev001/a.png' })
    await flush()
    expect(readAttachments(KEY)).toEqual([])
    expect(new Set(revoked).size).toBe(2) // nothing made after the release, nothing leaked
  })

  it('when an upload fails', async () => {
    const up = pendingUpload()
    render(ui())
    paste([png()])
    await flush()
    expect(revoked).toEqual([])
    up.reject(Object.assign(new Error('net'), { kind: 'network' }))
    await flush()
    expect(revoked).toEqual(made)
  })

  it('not while the tab is away: switching only unmounts the input', async () => {
    const { unmount } = render(ui())
    paste([png()])
    await flush()
    unmount()
    expect(revoked).toEqual([])
    render(ui())
    expect(screen.getByRole('img', { name: 'shot.png' })).toHaveAttribute('src', made[0])
  })
})

describe('a chip with no local file (restored after a reload)', () => {
  const restore = (over: Record<string, unknown> = {}) => {
    addAttachment(KEY, { id: 'r1', name: 'old.png', path: '/up/dev001/old.png', text: '[Image: source: /up/dev001/old.png]', image: true, size: 5, ...over })
    writeDraft(KEY, '[Image: source: /up/dev001/old.png]')
  }

  it('reads the picture from the host (pinned to it) and shows it', async () => {
    mocks.read.mockResolvedValue(PNG_BYTES)
    restore()
    render(ui())
    await flush()
    expect(mocks.read).toHaveBeenCalledWith('/up/dev001/old.png', expect.any(AbortSignal))
    expect(screen.getByRole('img', { name: 'old.png' })).toHaveAttribute('src', made[0])
  })

  it('shows the icon and the name, without an error, when it cannot be read', async () => {
    mocks.read.mockRejectedValue(Object.assign(new Error('too big'), { status: 413 }))
    restore()
    render(ui())
    await flush()
    const chip = screen.getByTestId('attachment-chip')
    expect(within(chip).queryByRole('img')).toBeNull()
    expect(within(chip).getByTestId('attachment-name')).toHaveTextContent('old.png')
    expect(screen.queryByTestId('session-input-hint')).toBeNull()
  })

  it('refuses a picture larger than 10 MB even if the host sent it', async () => {
    mocks.read.mockResolvedValue(new Uint8Array(10 * 1024 * 1024 + 1))
    restore()
    render(ui())
    await flush()
    expect(within(screen.getByTestId('attachment-chip')).queryByRole('img')).toBeNull()
    expect(made).toEqual([])
  })
})
