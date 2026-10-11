// c9d9c7-62: what waits to be read is drawn at the end of the stream, inside the scroll box, after the live line — Claude Code's own queue first
// (header.queued), then what the App still holds. Real DeckView / ChatView with the real input below as the footer.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { act, cleanup, fireEvent, render, screen, within } from '@testing-library/react'
import { forgetFolds } from '../../lib/conversations/fold-memory'
import { forgetScrollMemosWithPrefix } from '../../lib/nex/transcript-scroll-memory'
import { emptyDoc } from '../../lib/conversations/model'
import type { Header, HeaderQueued, Turn } from '../../lib/conversations/types'
import type { ConversationEntry } from '../../stores/useConversationStore'
import { clearAllDrafts, draftKey } from '../../lib/conversations/draft-memory'
import { clearAllAttachments } from '../../lib/conversations/attachment-memory'
import { clearAllCleared } from '../../lib/conversations/draft-clear'
import { clearAllUploads } from '../../lib/conversations/attachment-upload'
import { UNDO_MS, clearAllSendQueues, sendQueueFor } from '../../lib/conversations/send-queue'
import type { SendOutcome, SendPort } from '../../lib/conversations/send'
import { useHostConfigStore } from '../../stores/useHostConfigStore'
import { DeckView } from './DeckView'
import { ChatView } from './ChatView'
import { SessionInput } from './SessionInput'
import { footerContext } from './footer-context'

HTMLElement.prototype.scrollTo = function () {} as never

const H = 'h', S = 'sess-1', P = 'p-tail'
const KEY = draftKey(P, H, S)
const turn: Turn = { id: 't0', index: 0, started_at: 1, outcome: 'running', items: [{ type: 'user', id: 'u0', at: 1, index: 0, text: 'go', source: 'user' }] }
const header = (status: string, queued?: HeaderQueued[]): Header => ({ title: 't', status, backend: 'terminal', live: true, ...(queued ? { queued } : {}) })
const entry = (h: Header): ConversationEntry => ({
  doc: { ...emptyDoc(), turns: [turn], header: h, capabilities: { send: 'prompt' } }, status: 'live', reason: '', paging: false, subagents: {},
})
const calls: Array<{ text: string; resolve: (o: SendOutcome) => void }> = []
const port: SendPort = {
  submit: (text) => new Promise<SendOutcome>((resolve) => { calls.push({ text, resolve }) }),
  interrupt: () => Promise.resolve<SendOutcome>({ kind: 'accepted' }),
}
const footer = (ctx: ReturnType<typeof footerContext>) => <SessionInput {...ctx} sessionCode="c" pendingInStream />
const props = { paneId: P, hostId: H, sessionId: S, onSwitchToTerminal: () => {} }
const mountDeck = (h: Header) => render(<DeckView {...props} entry={entry(h)} footer={footer} />)
const box = () => screen.getByRole('textbox') as HTMLTextAreaElement
const type = (v: string) => fireEvent.change(box(), { target: { value: v } })
const enter = () => fireEvent.keyDown(box(), { key: 'Enter' })

beforeEach(() => {
  cleanup()
  vi.useFakeTimers()
  calls.length = 0
  forgetFolds(`${P}\0${S}`)
  forgetScrollMemosWithPrefix(P)
  useHostConfigStore.setState({ byHost: {}, ensureLoaded: async () => {} })
  sendQueueFor(KEY, () => port)
})
afterEach(() => { cleanup(); clearAllCleared(); clearAllDrafts(); clearAllAttachments(); clearAllUploads(); clearAllSendQueues(); vi.useRealTimers() })

describe('where it is drawn', () => {
  it('a message the App holds is in the scroll box after the live line, not in the footer above the input', () => {
    mountDeck(header('running'))
    type('hold me'); enter()
    const held = screen.getByTestId('queued-message')
    const scroll = screen.getByTestId('deck-scroll')
    expect(scroll.contains(held)).toBe(true)
    expect(screen.getByTestId('session-input').contains(held)).toBe(false)
    const live = screen.getByTestId('turn-live')
    expect(live.compareDocumentPosition(held) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
    expect(held).toHaveClass('border-accent', 'bg-accent/12') // not accepted yet: the first-cut block
  })

  it('the chat view draws it in its scroll box too', () => {
    render(<ChatView paneKey={P} hostId={H} sessionId={S} title="t" status="running" turns={[turn]} onSwitchToTerminal={() => {}} active input={<SessionInput paneKey={P} hostId={H} sessionId={S} capabilities={{ send: 'prompt' }} pendingInStream onSwitchToTerminal={() => {}} />} />)
    type('hold me'); enter()
    expect(screen.getByTestId('chat-scroll').contains(screen.getByTestId('queued-message'))).toBe(true)
  })

  it('Undo on the line brings the message into the box; Up in an empty box does too', () => {
    mountDeck(header('running'))
    type('one'); enter()
    type('two'); enter()
    fireEvent.click(within(screen.getAllByTestId('queued-message')[1]).getByRole('button', { name: 'Undo' }))
    expect(box().value).toBe('two')
    expect(screen.getAllByTestId('queued-message')).toHaveLength(1)
    type(''); fireEvent.keyDown(box(), { key: 'ArrowUp' })
    expect(box().value).toBe('one')
    expect(screen.queryByTestId('queued-message')).toBeNull()
  })

  it('a message the mod has taken stays at the end as a card saying it is on its way (never "queued")', async () => {
    mountDeck(header('running'))
    type('out'); enter()
    await act(async () => { await vi.advanceTimersByTimeAsync(UNDO_MS) })
    const m = screen.getByTestId('queued-message')
    expect(screen.getByTestId('deck-scroll').contains(m)).toBe(true)
    expect(m.textContent).not.toMatch(/queued/i)
  })
})

describe('Claude Code\'s own queue (header.queued)', () => {
  const user: HeaderQueued = { text: 'also check the logs', at: 5, source: 'user' }
  const peer: HeaderQueued = { text: 'ship it', at: 6, source: 'peer', from: 'mlab/purdex-1f-vq' }

  it('a user message: accent frame, "You · queued", the text, a note that it waits in the terminal, no Undo', () => {
    mountDeck(header('running', [user]))
    const el = screen.getByTestId('header-queued')
    expect(screen.getByTestId('deck-scroll').contains(el)).toBe(true)
    expect(el.dataset.source).toBe('user')
    expect(el).toHaveClass('border-accent', 'bg-accent/12')
    expect(el).toHaveTextContent('You · queued')
    expect(el).toHaveTextContent('also check the logs')
    expect(el).toHaveTextContent('Queued in the terminal')
    expect(within(el).queryByRole('button')).toBeNull()
  })

  it('a peer message: teal, faded, "From <name> · queued"', () => {
    mountDeck(header('running', [peer]))
    const el = screen.getByTestId('header-queued')
    expect(el.dataset.source).toBe('peer')
    expect(el.className).toMatch(/border-peer/)
    expect(el.className).toMatch(/opacity-/)
    expect(el).toHaveTextContent('From mlab/purdex-1f-vq · queued')
    expect(el).toHaveTextContent('ship it')
  })

  it('an older daemon (no field) or an empty list draws nothing', () => {
    mountDeck(header('running'))
    expect(screen.queryByTestId('header-queued')).toBeNull()
    expect(screen.queryByTestId('pending-tail')).toBeNull()
    cleanup()
    mountDeck(header('running', []))
    expect(screen.queryByTestId('pending-tail')).toBeNull()
  })

  it('only while the conversation runs or waits: an idle header with a leftover queue draws nothing', () => {
    mountDeck(header('idle', [user]))
    expect(screen.queryByTestId('header-queued')).toBeNull()
    cleanup()
    mountDeck(header('waiting', [user]))
    expect(screen.getByTestId('header-queued')).toBeInTheDocument()
  })

  it('Claude Code\'s queue first, then what the App holds', () => {
    mountDeck(header('running', [user, peer]))
    type('mine'); enter()
    const order = screen.getByTestId('pending-tail').children
    expect(Array.from(order).map((c) => c.getAttribute('data-testid'))).toEqual(['header-queued', 'header-queued', 'queued-messages'])
  })

  it('once it is read in (the header no longer lists it) the line goes', () => {
    const { rerender } = mountDeck(header('running', [user]))
    expect(screen.getByTestId('header-queued')).toBeInTheDocument()
    rerender(<DeckView {...props} entry={entry(header('running', []))} footer={footer} />)
    expect(screen.queryByTestId('header-queued')).toBeNull()
  })

  it('a malformed value from the daemon draws nothing and does not take the pane down', () => {
    for (const bad of [{}, 'x', 1, [null], [1, 'a'], [{ source: 'user' }], [{ text: '' }]]) {
      cleanup()
      mountDeck({ ...header('running'), queued: bad as unknown as HeaderQueued[] })
      expect(screen.queryByTestId('header-queued')).toBeNull()
    }
  })

  it('odd entries are coerced and a long list is cut', () => {
    const many = Array.from({ length: 30 }, (_, i) => ({ text: `m${i}`, at: i, source: 'user' }))
    mountDeck(header('running', many))
    expect(screen.getAllByTestId('header-queued')).toHaveLength(20)
    cleanup()
    mountDeck({ ...header('running'), queued: [{ text: 'no at, no source' }] as unknown as HeaderQueued[] })
    expect(screen.getByTestId('header-queued')).toHaveTextContent('no at, no source')
  })

  it('a conversation with no turns yet and a first message the App holds shows it, without the "nothing here" block', () => {
    render(<DeckView {...props} entry={{ ...entry(header('running')), doc: { ...entry(header('running')).doc, turns: [] } }} footer={footer} />)
    expect(screen.getByTestId('deck-empty')).toBeInTheDocument()
    type('first'); enter()
    expect(screen.queryByTestId('deck-empty')).toBeNull()
    expect(screen.getByTestId('queued-message')).toBeInTheDocument()
  })

  it('the chat draws it too', () => {
    render(<ChatView paneKey={P} hostId={H} sessionId={S} title="t" status="running" queued={[user]} turns={[turn]} onSwitchToTerminal={() => {}} active />)
    expect(screen.getByTestId('chat-scroll').contains(screen.getByTestId('header-queued'))).toBe(true)
  })
})
