// v3 pt 5: in a sent message, [Image: source: <path>] is a thumbnail tag (read from the host, 10 MB at most), not the path; the text
// around it keeps its order. Deck and chat share the tag with the input's chips.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { act, cleanup, fireEvent, render, screen, within } from '@testing-library/react'
import { UserBlock } from './UserBlock'
import { UserBubble } from './ChatBubbles'
import { DeckView } from './DeckView'
import { ChatView } from './ChatView'
import { FoldContext, useFoldMemory } from '../room/fold-context'
import { ConversationHostContext } from './conversation-host'
import { clearFsImages } from '../../lib/conversations/fs-image'
import { emptyDoc } from '../../lib/conversations/model'
import { forgetFolds } from '../../lib/conversations/fold-memory'
import { clearAllPanels } from '../../lib/conversations/panel-memory'
import { forgetScrollMemosWithPrefix } from '../../lib/nex/transcript-scroll-memory'
import type { ConversationItem, Turn, UserItem } from '../../lib/conversations/types'
import { JPEG_BYTES, PNG_BYTES, TEXT_BYTES } from '../../lib/conversations/image-fixtures'
import type { ConversationEntry } from '../../stores/useConversationStore'

const mocks = vi.hoisted(() => ({ read: vi.fn(), backend: vi.fn() }))
vi.mock('../../lib/fs-backend-daemon', () => ({ createDaemonBackendForHost: (h: string) => { mocks.backend(h); return { read: mocks.read } } }))

const user = (text: string, over: Partial<UserItem> = {}): UserItem => ({ type: 'user', id: 'u1', at: 1, index: 0, text, source: 'user', ...over })
const flush = () => act(async () => { await Promise.resolve(); await Promise.resolve(); await Promise.resolve() })
function Folds({ children }: { children: React.ReactNode }) { return <FoldContext.Provider value={useFoldMemory()}>{children}</FoldContext.Provider> }
const withHost = (ui: React.ReactElement, host: string | null = 'h') => <Folds><ConversationHostContext.Provider value={host}>{ui}</ConversationHostContext.Provider></Folds>
const MSG = 'before [Image: source: /up/dev001/a.png] after'

let made: string[] = []
let revoked: string[] = []
beforeEach(() => {
  made = []; revoked = []
  let n = 0
  URL.createObjectURL = vi.fn(() => { const u = `blob:sent/${++n}`; made.push(u); return u })
  URL.revokeObjectURL = vi.fn((u: string) => { revoked.push(u) })
  mocks.read.mockReset().mockResolvedValue(PNG_BYTES)
  mocks.backend.mockReset()
})
afterEach(() => { vi.useRealTimers(); cleanup(); clearFsImages(); clearAllPanels() })

describe('the deck block', () => {
  it('shows a thumbnail instead of the marker, between the text before and after it, read from the message\'s host', async () => {
    const { container } = render(withHost(<UserBlock item={user(MSG)} />))
    await flush()
    const body = container.querySelector('.whitespace-pre-wrap') as HTMLElement
    expect(body.textContent).not.toContain('source:')
    expect(body.textContent).not.toContain('/up/dev001')
    const kinds = [...body.childNodes].map((n) => (n.nodeType === 3 ? `t:${n.textContent}` : (n as HTMLElement).dataset.testid ?? (n as HTMLElement).tagName))
    expect(kinds).toEqual(['t:before ', 'sent-image-tag', 't: after'])
    expect(within(body).getByRole('img', { name: 'a.png' })).toHaveAttribute('src', made[0])
    expect(mocks.backend).toHaveBeenCalledWith('h')
    expect(mocks.read).toHaveBeenCalledWith('/up/dev001/a.png', expect.any(AbortSignal))
  })

  it('several tags and text between them keep their order', async () => {
    const { container } = render(withHost(<UserBlock item={user('[Image: source: /up/1.png]\nmiddle\n[Image: source: /up/2.png] end')} />))
    await flush()
    const body = container.querySelector('.whitespace-pre-wrap') as HTMLElement
    expect(body.textContent).toBe('\nmiddle\n end')
    expect(within(body).getAllByRole('img').map((i) => i.getAttribute('alt'))).toEqual(['1.png', '2.png'])
  })

  it('a plain message is untouched, and a bash input is not scanned', () => {
    const { container } = render(<UserBlock item={user('hello')} />)
    expect(container.querySelector('.whitespace-pre-wrap')?.textContent).toBe('hello')
    cleanup()
    const bash = render(<UserBlock item={user('echo [Image: source: /x.png]', { source: 'bash' })} />)
    expect(bash.container.querySelector('.whitespace-pre-wrap')?.textContent).toBe('! echo [Image: source: /x.png]')
  })

  it('reads a path once for however many tags show it, and again for none after the reader leaves it in the cache', async () => {
    const view = render(withHost(<><UserBlock item={user(MSG)} /><UserBlock item={user('again [Image: source: /up/dev001/a.png]')} /></>))
    await flush()
    expect(mocks.read).toHaveBeenCalledTimes(1)
    view.rerender(withHost(<><UserBlock item={user(MSG)} /><UserBlock item={user('again [Image: source: /up/dev001/a.png]')} /></>))
    expect(mocks.read).toHaveBeenCalledTimes(1)
    view.unmount()
    render(withHost(<UserBlock item={user(MSG)} />))
    await flush()
    expect(mocks.read).toHaveBeenCalledTimes(1) // the picture is still cached; no second read, no second blob
    expect(made).toHaveLength(1)
    expect(revoked).toEqual([])
  })

  it('shows a skeleton while the picture is read', async () => {
    let done!: (b: Uint8Array) => void
    mocks.read.mockReturnValueOnce(new Promise((res) => { done = res }))
    render(withHost(<UserBlock item={user(MSG)} />))
    expect(screen.getByTestId('attachment-skeleton')).toBeInTheDocument()
    done(PNG_BYTES)
    await flush()
    expect(screen.queryByTestId('attachment-skeleton')).toBeNull()
  })

  it('a picture that cannot be read (or is over 10 MB) is an icon and its file name, with no error', async () => {
    mocks.read.mockRejectedValueOnce(Object.assign(new Error('too large'), { status: 413 }))
    render(withHost(<UserBlock item={user(MSG)} />))
    await flush()
    const tag = screen.getByTestId('sent-image-tag')
    expect(within(tag).queryByRole('img')).toBeNull()
    expect(within(tag).getByTestId('attachment-name')).toHaveTextContent('a.png')
    expect(tag.querySelector('svg')).not.toBeNull()
    expect(screen.queryByRole('alert')).toBeNull()
  })

  it('without a host to read from it is the same icon and name, and nothing is read', async () => {
    render(withHost(<UserBlock item={user(MSG)} />, null))
    await flush()
    expect(mocks.read).not.toHaveBeenCalled()
    expect(within(screen.getByTestId('sent-image-tag')).getByTestId('attachment-name')).toHaveTextContent('a.png')
  })

  it('resting on the tag floats the preview; a click opens the lightbox', async () => {
    render(withHost(<UserBlock item={user(MSG)} />))
    await flush()
    vi.useFakeTimers()
    const thumb = within(screen.getByTestId('sent-image-tag')).getByTestId('attachment-thumb')
    fireEvent.mouseEnter(thumb)
    act(() => { vi.advanceTimersByTime(400) })
    expect(screen.getByTestId('attachment-hover-preview')).toHaveTextContent('a.png')
    fireEvent.mouseLeave(thumb)
    expect(screen.queryByTestId('attachment-hover-preview')).toBeNull()
    fireEvent.click(screen.getByRole('button', { name: 'View a.png' }))
    expect(screen.getByRole('dialog')).toBeInTheDocument()
    fireEvent.keyDown(document, { key: 'Escape' })
    expect(screen.queryByRole('dialog')).toBeNull()
  })
})

describe('the chat bubble', () => {
  it('shows the same tag in the person\'s bubble', async () => {
    render(withHost(<UserBubble item={user(MSG)} />))
    await flush()
    const bubble = screen.getByTestId('chat-user-bubble')
    expect(bubble.textContent).toBe('before  after')
    expect(within(bubble).getByRole('img', { name: 'a.png' })).toHaveAttribute('src', made[0])
  })
})

const turnOf = (text: string): Turn => ({ id: 't1', index: 0, started_at: 1, outcome: 'done', items: [user(text)] as ConversationItem[] })

describe('in the whole views, whose host is the pane\'s', () => {
  beforeEach(() => { cleanup(); forgetFolds('p-deck\0s'); forgetScrollMemosWithPrefix('p-deck'); clearAllPanels() })

  it('the deck draws it', async () => {
    const entry: ConversationEntry = { doc: { ...emptyDoc(), turns: [turnOf(MSG)] }, status: 'live', reason: '', paging: false, subagents: {} }
    render(<DeckView paneId="p-deck" hostId="host-7" sessionId="s" entry={entry} onSwitchToTerminal={() => {}} />)
    await flush()
    expect(within(screen.getByTestId('deck-user')).getByRole('img', { name: 'a.png' })).toBeInTheDocument()
    expect(mocks.backend).toHaveBeenCalledWith('host-7')
  })

  it('the chat draws it', async () => {
    render(<ChatView paneKey="p-chat" hostId="host-9" sessionId="s" title="t" status="idle" turns={[{ id: 't1', index: 0, items: [user(MSG)] as ConversationItem[] }]} onSwitchToTerminal={() => {}} active />)
    await flush()
    expect(within(screen.getByTestId('chat-user-bubble')).getByRole('img', { name: 'a.png' })).toBeInTheDocument()
    expect(mocks.backend).toHaveBeenCalledWith('host-9')
  })
})

describe('only the person\'s own messages are read for pictures', () => {
  const MARK = '[Image: source: /up/dev001/a.png]'
  it.each(['peer', 'background', 'task', 'schedule', 'scheduled', 'bash', 'brand-new-source'])('a %s message is plain text and reads nothing', async (source) => {
    const { container } = render(withHost(<UserBlock item={user(`x ${MARK}`, { source })} />))
    await flush()
    expect(mocks.read).not.toHaveBeenCalled()
    expect(screen.queryByTestId('sent-image-tag')).toBeNull()
    if (source !== 'command_output') expect(container.textContent).toContain(MARK)
  })
  it.each(['user', 'queued'])('a %s message is parsed', async (source) => {
    render(withHost(<UserBlock item={user(`x ${MARK}`, { source })} />))
    await flush()
    expect(mocks.read).toHaveBeenCalledTimes(1)
  })
  it('the chat bubble parses the person\'s only; an unknown source and the block-drawn ones stay plain', async () => {
    render(withHost(<><UserBubble item={user(`x ${MARK}`, { id: 'p', source: 'peer' })} /><UserBubble item={user(`y ${MARK}`, { id: 'w', source: 'weird' })} /></>))
    await flush()
    expect(mocks.read).not.toHaveBeenCalled()
    expect(document.body.textContent).toContain(`y ${MARK}`)
    cleanup()
    render(withHost(<UserBubble item={user(`z ${MARK}`, { source: 'queued' })} />))
    await flush()
    expect(mocks.read).toHaveBeenCalledTimes(1)
  })
})

describe('what may be read', () => {
  it.each(['/etc/passwd', '/up/dev001/notes.txt', '/up/dev001/a.png.sh', '/up/dev001/a.svg'])('a path that is not an image file (%s) is plain text and reads nothing', async (p) => {
    const { container } = render(withHost(<UserBlock item={user(`see [Image: source: ${p}]`)} />))
    await flush()
    expect(mocks.read).not.toHaveBeenCalled()
    expect(container.textContent).toContain(`[Image: source: ${p}]`)
  })
  it.each(['/dev/zero.png', '/dev/fd/3.png', '/proc/self/mem.png', '/sys/x/y.png'])('%s is never read: icon and name', async (p) => {
    render(withHost(<UserBlock item={user(`see [Image: source: ${p}]`)} />))
    await flush()
    expect(mocks.read).not.toHaveBeenCalled()
    expect(within(screen.getByTestId('sent-image-tag')).getByTestId('attachment-name')).toBeInTheDocument()
  })
  it('a body that is not an image (wrong magic bytes) is an icon and the name, with no blob made', async () => {
    mocks.read.mockResolvedValue(TEXT_BYTES)
    render(withHost(<UserBlock item={user(MSG)} />))
    await flush()
    expect(made).toEqual([])
    expect(within(screen.getByTestId('sent-image-tag')).queryByRole('img')).toBeNull()
    expect(within(screen.getByTestId('sent-image-tag')).getByTestId('attachment-name')).toHaveTextContent('a.png')
  })
  it('the type of the blob is what the bytes are, not what the extension says', async () => {
    const blobs: Blob[] = []
    URL.createObjectURL = vi.fn((b: Blob) => { blobs.push(b); return 'blob:sniffed' })
    mocks.read.mockResolvedValue(JPEG_BYTES)
    render(withHost(<UserBlock item={user(MSG)} />)) // named .png
    await flush()
    expect(blobs[0].type).toBe('image/jpeg')
  })
})

describe('a tag is read when it comes into view, not before', () => {
  type Obs = { cb: (e: Array<{ isIntersecting: boolean }>) => void; target?: Element; disconnected: boolean }
  let observers: Obs[] = []
  const real = globalThis.IntersectionObserver
  beforeEach(() => {
    observers = []
    globalThis.IntersectionObserver = class {
      o: Obs
      constructor(cb: Obs['cb']) { this.o = { cb, disconnected: false }; observers.push(this.o) }
      observe(t: Element) { this.o.target = t }
      unobserve() {}
      disconnect() { this.o.disconnected = true }
      takeRecords() { return [] }
    } as unknown as typeof IntersectionObserver
  })
  afterEach(() => { globalThis.IntersectionObserver = real })

  it('nothing is read while the tag is off screen; it is read once it intersects', async () => {
    render(withHost(<UserBlock item={user(MSG)} />))
    await flush()
    expect(mocks.read).not.toHaveBeenCalled()
    expect(screen.getByTestId('attachment-skeleton')).toBeInTheDocument()
    act(() => { observers[0].cb([{ isIntersecting: true }]) })
    await flush()
    expect(mocks.read).toHaveBeenCalledTimes(1)
    expect(observers[0].disconnected).toBe(true)
  })

  it('a tag that goes away before it was seen never reads', async () => {
    const v = render(withHost(<UserBlock item={user(MSG)} />))
    v.unmount()
    await flush()
    expect(observers[0].disconnected).toBe(true)
    expect(mocks.read).not.toHaveBeenCalled()
  })
})
