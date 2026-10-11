// c9d9c7-62: what waits to be read ends the stream, and it coming or going moves the end like the live line does: the scroll hook's `follow` (which
// keeps a reader at the bottom at the bottom and leaves anyone else alone) is asked again. jsdom has no layout, so the hook is replaced by a spy.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { act, cleanup, fireEvent, render, screen } from '@testing-library/react'
import { emptyDoc } from '../../lib/conversations/model'
import type { Header, HeaderQueued, Turn } from '../../lib/conversations/types'
import type { ConversationEntry } from '../../stores/useConversationStore'
import { clearAllDrafts, draftKey } from '../../lib/conversations/draft-memory'
import { clearAllAttachments } from '../../lib/conversations/attachment-memory'
import { clearAllSendQueues, sendQueueFor } from '../../lib/conversations/send-queue'
import type { SendOutcome, SendPort } from '../../lib/conversations/send'
import { useHostConfigStore } from '../../stores/useHostConfigStore'
import { DeckView } from './DeckView'
import { SessionInput } from './SessionInput'
import { footerContext } from './footer-context'

const follow = vi.fn()
vi.mock('../../hooks/useTranscriptScroll', () => ({
  useTranscriptScroll: () => ({ attach: () => {}, onScroll: () => {}, follow, release: () => {}, shiftBy: () => {}, placed: true }),
}))
HTMLElement.prototype.scrollTo = function () {} as never

const H = 'h', S = 'sess-1', P = 'p-follow'
const KEY = draftKey(P, H, S)
const TURNS: Turn[] = []
const turn: Turn = { id: 't0', index: 0, started_at: 1, outcome: 'running', items: [{ type: 'user', id: 'u0', at: 1, index: 0, text: 'go', source: 'user' }] }
const header = (queued: HeaderQueued[]): Header => ({ title: 't', status: 'running', backend: 'terminal', live: true, queued })
TURNS.push(turn) // one array for every render: only the header changes between them
const entry = (h: Header): ConversationEntry => ({ doc: { ...emptyDoc(), turns: TURNS, header: h, capabilities: { send: 'prompt' } }, status: 'live', reason: '', paging: false, subagents: {} })
const port: SendPort = { submit: () => new Promise<SendOutcome>(() => {}), interrupt: () => Promise.resolve<SendOutcome>({ kind: 'accepted' }) }
const footer = (ctx: ReturnType<typeof footerContext>) => <SessionInput {...ctx} onSwitchToTerminal={() => {}} sessionCode="c" pendingInStream />
const props = { paneId: P, hostId: H, sessionId: S, onSwitchToTerminal: () => {} }

beforeEach(() => {
  cleanup()
  follow.mockReset()
  useHostConfigStore.setState({ byHost: {}, ensureLoaded: async () => {} })
  sendQueueFor(KEY, () => port)
})
afterEach(() => { cleanup(); clearAllDrafts(); clearAllAttachments(); clearAllSendQueues() })

describe('the end of the stream moved', () => {
  it('a message of Claude Code\'s queue appearing and going asks the scroll to follow', () => {
    const { rerender } = render(<DeckView {...props} entry={entry(header([]))} footer={footer} />)
    follow.mockClear()
    rerender(<DeckView {...props} entry={entry(header([{ text: 'x', at: 1, source: 'user' }]))} footer={footer} />)
    expect(follow).toHaveBeenCalled()
    follow.mockClear()
    rerender(<DeckView {...props} entry={entry(header([]))} footer={footer} />)
    expect(follow).toHaveBeenCalled()
  })

  it('a header that is a new object with the same queued lines does not ask again', () => {
    const q = [{ text: 'x', at: 1, source: 'user' as const }]
    const { rerender } = render(<DeckView {...props} entry={entry(header(q))} footer={footer} />)
    follow.mockClear()
    rerender(<DeckView {...props} entry={entry(header([...q]))} footer={footer} />)
    expect(follow).not.toHaveBeenCalled()
  })

  it('a message the App holds appearing and being taken back asks the scroll to follow', () => {
    render(<DeckView {...props} entry={entry(header([]))} footer={footer} />)
    const box = screen.getByRole('textbox')
    follow.mockClear()
    fireEvent.change(box, { target: { value: 'held' } })
    fireEvent.keyDown(box, { key: 'Enter' })
    expect(follow).toHaveBeenCalled()
    follow.mockClear()
    act(() => { fireEvent.keyDown(box, { key: 'ArrowUp' }) })
    expect(follow).toHaveBeenCalled()
  })
})
