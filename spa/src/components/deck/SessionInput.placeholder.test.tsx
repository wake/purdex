// v3 pt 5: an attached image leaves a short [Image #N] in the draft; sending expands it to the [Image: source: <path>] marker the
// agent reads. The chips follow the draft (a placeholder the reader deleted or broke takes its chip), and the draft follows the chips.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { act, cleanup, fireEvent, render, screen, within } from '@testing-library/react'
import { SessionInput } from './SessionInput'
import { clearAllDrafts, draftKey, readDraft } from '../../lib/conversations/draft-memory'
import { clearAllAttachments, readAttachments } from '../../lib/conversations/attachment-memory'
import { clearAllUploads } from '../../lib/conversations/attachment-upload'
import { clearFsImages } from '../../lib/conversations/fs-image'
import { clearAllSendQueues } from '../../lib/conversations/send-queue'

const mocks = vi.hoisted(() => ({ fetch: vi.fn(), upload: vi.fn() }))
vi.mock('../../lib/host-api', () => ({ pinnedHostFetch: mocks.fetch, agentUploadToPath: mocks.upload }))
vi.mock('../../lib/fs-backend-daemon', () => ({ createDaemonBackendForHost: () => ({ read: () => Promise.reject(new Error('no read')) }) }))

const SID = '11111111-2222-4333-8444-555555555555'
const KEY = draftKey('p1', 'h', SID)
const ui = () => <SessionInput paneKey="p1" hostId="h" sessionId={SID} sessionCode="dev001" capabilities={{ send: 'prompt' }} onSwitchToTerminal={() => {}} />
const box = () => screen.getByRole('textbox') as HTMLTextAreaElement
const png = (name: string) => new File(['x'], name, { type: 'image/png' })
const pdf = (name = 'doc.pdf') => new File(['x'], name, { type: 'application/pdf' })
const paste = (...files: File[]) => fireEvent.paste(box(), { clipboardData: { files, getData: () => '', types: ['Files'] } })
const flush = () => act(async () => { await Promise.resolve(); await Promise.resolve(); await Promise.resolve() })
const attach = async (...files: File[]) => { paste(...files); await flush() }
const type = (v: string) => fireEvent.change(box(), { target: { value: v } })
const enter = () => fireEvent.keyDown(box(), { key: 'Enter' })
const sent = () => screen.getByTestId('queued-message')

beforeEach(() => {
  let n = 0
  URL.createObjectURL = vi.fn(() => `blob:test/${++n}`)
  URL.revokeObjectURL = vi.fn()
  mocks.fetch.mockReset().mockImplementation(() => Promise.resolve(new Response(JSON.stringify({ status: 'accepted' }))))
  mocks.upload.mockReset().mockImplementation((_h: string, f: File) => Promise.resolve({ filename: f.name, path: `/up/dev001/${f.name}` }))
})
afterEach(() => { cleanup(); clearFsImages(); clearAllUploads(); clearAllDrafts(); clearAllAttachments(); clearAllSendQueues() })

describe('placeholders in the draft', () => {
  it('an image leaves [Image #1], not the whole marker; the chip is there', async () => {
    render(ui())
    await attach(png('a.png'))
    expect(box().value).toBe('[Image #1]')
    expect(readDraft(KEY)).toBe('[Image #1]')
    expect(readAttachments(KEY)[0]).toMatchObject({ path: '/up/dev001/a.png', text: '[Image #1]', seq: 1 })
    expect(screen.getAllByTestId('attachment-chip')).toHaveLength(1)
  })

  it('images are numbered in order, behind text with a space; any other file keeps its path on its own line', async () => {
    render(ui())
    type('look')
    await attach(png('a.png'), png('b.png'))
    expect(box().value).toBe('look [Image #1] [Image #2]')
    await attach(pdf())
    expect(box().value).toBe('look [Image #1] [Image #2]\n/up/dev001/doc.pdf')
  })

  it('removing one does not renumber the others, and a new image never takes a number that was used', async () => {
    render(ui())
    await attach(png('a.png'), png('b.png'))
    fireEvent.click(screen.getAllByRole('button', { name: /Remove attachment/ })[0])
    expect(box().value).toBe('[Image #2]')
    expect(readAttachments(KEY).map((a) => a.seq)).toEqual([2])
    await attach(png('c.png'))
    expect(box().value).toBe('[Image #2] [Image #3]')
    // the newest goes too: its number is still not handed out again
    fireEvent.click(screen.getAllByRole('button', { name: /Remove attachment/ })[1])
    await attach(png('d.png'))
    expect(box().value).toBe('[Image #2] [Image #4]')
  })

  it('numbers start over for the next message', async () => {
    render(ui())
    await attach(png('a.png'))
    enter()
    await attach(png('b.png'))
    expect(box().value).toBe('[Image #1]')
  })
})

describe('sending expands the placeholders', () => {
  it('each [Image #N] becomes [Image: source: path] and the rest of the text is untouched, whatever the position', async () => {
    render(ui())
    await attach(png('a.png'), png('b.png'))
    type('check [Image #2] first,\nthen [Image #1] please')
    enter()
    expect(sent()).toHaveTextContent('check [Image: source: /up/dev001/b.png] first, then [Image: source: /up/dev001/a.png] please')
    expect(sent().textContent).not.toContain('#')
    expect(box().value).toBe('')
    expect(screen.queryByTestId('attachment-chip')).toBeNull()
  })

  it('a placeholder the reader broke goes out as plain text, and its chip is gone', async () => {
    render(ui())
    await attach(png('a.png'), png('b.png'))
    type('[Image #1 and [Image #2]')
    expect(screen.getAllByTestId('attachment-chip')).toHaveLength(1) // shown: only the one the draft still holds
    expect(readAttachments(KEY).map((a) => a.seq)).toEqual([1, 2]) // kept: an undo may bring the placeholder back
    enter()
    expect(sent()).toHaveTextContent('[Image #1 and [Image: source: /up/dev001/b.png]')
  })

  it('a number no chip owns is sent as typed', async () => {
    render(ui())
    await attach(png('a.png'))
    type('[Image #9] vs [Image #1]')
    enter()
    expect(sent()).toHaveTextContent('[Image #9] vs [Image: source: /up/dev001/a.png]')
  })

  it('the same placeholder twice expands twice, with one chip', async () => {
    render(ui())
    await attach(png('a.png'))
    type('[Image #1] and again [Image #1]')
    expect(screen.getAllByTestId('attachment-chip')).toHaveLength(1)
    enter()
    expect(sent()).toHaveTextContent('[Image: source: /up/dev001/a.png] and again [Image: source: /up/dev001/a.png]')
  })

  it('a send that is refused keeps the draft with its placeholders, and the chips', async () => {
    render(ui())
    await attach(png('a.png'))
    const long = `${'x'.repeat(4001)} [Image #1]`
    type(long)
    enter()
    expect(screen.queryByTestId('queued-message')).toBeNull()
    expect(screen.getByTestId('session-input-hint')).toBeInTheDocument()
    expect(box().value).toBe(long)
    expect(readDraft(KEY)).toBe(long)
    expect(screen.getAllByTestId('attachment-chip')).toHaveLength(1)
  })

  it('a send held back by a running upload keeps the placeholders too', async () => {
    let resolve!: (v: { filename: string; path: string }) => void
    render(ui())
    await attach(png('a.png'))
    mocks.upload.mockImplementationOnce(() => new Promise((res) => { resolve = res }))
    await attach(png('b.png'))
    enter()
    expect(screen.queryByTestId('queued-message')).toBeNull()
    expect(box().value).toBe('[Image #1]')
    resolve({ filename: 'b.png', path: '/up/dev001/b.png' })
    await flush()
    expect(box().value).toBe('[Image #1] [Image #2]')
  })

  it('what the agent reads is what it always was: the same text a long marker in the draft sent', async () => {
    render(ui())
    await attach(png('a.png'))
    type('look at [Image #1]')
    enter()
    expect(sent()).toHaveTextContent('look at [Image: source: /up/dev001/a.png]')
  })
})

describe('the chips follow the draft, the draft follows the chips', () => {
  it('deleting a placeholder from the draft hides its chip (the memory keeps it for an undo)', async () => {
    render(ui())
    await attach(png('a.png'), png('b.png'))
    type('[Image #2]')
    expect(screen.getAllByTestId('attachment-chip')).toHaveLength(1)
    expect(within(screen.getByTestId('attachment-chip')).getByRole('img')).toHaveAttribute('alt', 'b.png')
    expect(readAttachments(KEY).map((a) => a.seq)).toEqual([1, 2])
  })

  it('removing a chip takes its placeholder out of the draft, in the middle of a line too', async () => {
    render(ui())
    await attach(png('a.png'), png('b.png'))
    type('a [Image #1] b\n[Image #2]')
    fireEvent.click(screen.getAllByRole('button', { name: /Remove attachment/ })[0])
    expect(box().value).toBe('a b\n[Image #2]')
    fireEvent.click(screen.getByRole('button', { name: /Remove attachment/ }))
    expect(box().value).toBe('a b')
  })
})

describe('the tag behind the box', () => {
  it('marks only the placeholders of live chips, behind the text, without touching the value', async () => {
    render(ui())
    expect(screen.queryByTestId('draft-tags')).toBeNull()
    await attach(png('a.png'))
    type('see [Image #1] and [Image #9]')
    const tags = screen.getByTestId('draft-tags')
    expect(tags).toHaveAttribute('aria-hidden', 'true')
    expect(tags.textContent?.replace(/\u200b/g, '')).toBe('see [Image #1] and [Image #9]')
    expect([...tags.querySelectorAll('mark')].map((m) => m.textContent)).toEqual(['[Image #1]'])
    expect(box().value).toBe('see [Image #1] and [Image #9]')
    expect(tags.compareDocumentPosition(box()) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
  })
})

describe('undo brings the attachment back', () => {
  it('delete the placeholder: the chip hides and is not sent; undo the text: the chip is back (same picture) and is expanded again', async () => {
    render(ui())
    await attach(png('a.png'), png('b.png'))
    const before = '[Image #1] [Image #2]'
    expect(box().value).toBe(before)
    const src = screen.getAllByRole('img', { name: 'a.png' })[0].getAttribute('src')

    type('[Image #2]') // the first placeholder deleted
    expect(screen.getAllByTestId('attachment-chip')).toHaveLength(1)
    expect(readAttachments(KEY)).toHaveLength(2)
    expect(URL.revokeObjectURL).not.toHaveBeenCalled() // the picture is still held

    type(before) // what the browser's own undo does: the text comes back
    expect(screen.getAllByTestId('attachment-chip')).toHaveLength(2)
    expect(screen.getByRole('img', { name: 'a.png' })).toHaveAttribute('src', src)

    enter()
    expect(sent()).toHaveTextContent('[Image: source: /up/dev001/a.png] [Image: source: /up/dev001/b.png]')
    expect(sent().textContent).not.toContain('#')
  })

  it('while the placeholder is gone the picture is not sent, whatever the memory holds', async () => {
    render(ui())
    await attach(png('a.png'))
    type('look')
    enter()
    expect(sent()).toHaveTextContent('look')
    expect(sent().textContent).not.toContain('source')
  })

  it('a send takes every attachment with it, the hidden ones too (their pictures are let go)', async () => {
    render(ui())
    await attach(png('a.png'), png('b.png'))
    type('[Image #2]')
    enter()
    expect(readAttachments(KEY)).toEqual([])
    expect(URL.revokeObjectURL).toHaveBeenCalledTimes(2)
  })

  it('releasing the pane lets a hidden chip go too', async () => {
    render(ui())
    await attach(png('a.png'))
    type('')
    expect(readAttachments(KEY)).toHaveLength(1)
    const { releasePane } = await import('../../lib/conversations/pane-release')
    releasePane('p1')
    expect(readAttachments(KEY)).toEqual([])
    expect(URL.revokeObjectURL).toHaveBeenCalledTimes(1)
  })

  it('the remove button still lets a shown chip go at once', async () => {
    render(ui())
    await attach(png('a.png'))
    fireEvent.click(screen.getByRole('button', { name: /Remove attachment/ }))
    expect(readAttachments(KEY)).toEqual([])
    expect(URL.revokeObjectURL).toHaveBeenCalledTimes(1)
  })
})

describe('a hidden chip is not kept for ever', () => {
  const T0 = new Date('2026-10-11T12:00:00Z').getTime()
  afterEach(() => { vi.useRealTimers() })

  it('when another attachment lands, a chip whose placeholder has been gone for more than 60 s is let go; a younger one stays', async () => {
    vi.useFakeTimers({ toFake: ['Date'] })
    vi.setSystemTime(T0)
    render(ui())
    await attach(png('a.png'), png('b.png'))
    type('[Image #2]') // #1 hides now
    vi.setSystemTime(T0 + 30_000)
    await attach(png('c.png')) // too early: #1 stays
    expect(readAttachments(KEY).map((a) => a.seq)).toEqual([1, 2, 3])
    type('[Image #2]') // #1 stays hidden, #3 hides now (T0 + 30 s)
    vi.setSystemTime(T0 + 61_000)
    await attach(png('d.png'))
    // #1 has been hidden 61 s: gone. #3 only 31 s: kept.
    expect(readAttachments(KEY).map((a) => a.seq)).toEqual([2, 3, 4])
    expect(URL.revokeObjectURL).toHaveBeenCalledTimes(1)
  })

  it('a chip that comes back (undo) is not on the clock any more', async () => {
    vi.useFakeTimers({ toFake: ['Date'] })
    vi.setSystemTime(T0)
    render(ui())
    await attach(png('a.png'))
    type('')
    type('[Image #1]')
    vi.setSystemTime(T0 + 120_000)
    type('')
    vi.setSystemTime(T0 + 130_000)
    await attach(png('b.png'))
    expect(readAttachments(KEY).map((a) => a.seq)).toEqual([1, 2]) // hidden for 10 s only, not 130
  })
})
