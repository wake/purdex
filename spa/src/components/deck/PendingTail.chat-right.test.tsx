// Chat mode: your own messages stay on the right while held / queued (like the delivered bubbles); the deck keeps its full-width blocks.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { act, cleanup, fireEvent, render, screen } from '@testing-library/react'
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

const H = 'h', S = 'sess-1', P = 'p-right'
const KEY = draftKey(P, H, S)
const turn: Turn = { id: 't0', index: 0, started_at: 1, outcome: 'running', items: [{ type: 'user', id: 'u0', at: 1, index: 0, text: 'go', source: 'user' }] }
const user: HeaderQueued = { text: 'also check the logs', at: 5, source: 'user' }
const peer: HeaderQueued = { text: 'ship it', at: 6, source: 'peer', from: 'mlab/purdex-1f-vq' }
const header = (queued: HeaderQueued[]): Header => ({ title: 't', status: 'running', backend: 'terminal', live: true, queued })
const entry = (h: Header): ConversationEntry => ({ doc: { ...emptyDoc(), turns: [turn], header: h, capabilities: { send: 'prompt' } }, status: 'live', reason: '', paging: false, subagents: {} })
const port: SendPort = { submit: () => new Promise<SendOutcome>(() => {}), interrupt: () => Promise.resolve<SendOutcome>({ kind: 'accepted' }) }
const input = <SessionInput paneKey={P} hostId={H} sessionId={S} capabilities={{ send: 'prompt' }} pendingInStream onSwitchToTerminal={() => {}} />
const mountChat = (queued: HeaderQueued[] = []) => render(<ChatView paneKey={P} hostId={H} sessionId={S} title="t" status="running" queued={queued} turns={[turn]} onSwitchToTerminal={() => {}} active input={input} />)
const footer = (ctx: ReturnType<typeof footerContext>) => <SessionInput {...ctx} onSwitchToTerminal={() => {}} sessionCode="c" pendingInStream />
const mountDeck = (queued: HeaderQueued[]) => render(<DeckView paneId={P} hostId={H} sessionId={S} onSwitchToTerminal={() => {}} entry={entry(header(queued))} footer={footer} />)
const box = () => screen.getByRole('textbox') as HTMLTextAreaElement
const send = (v: string) => { fireEvent.change(box(), { target: { value: v } }); fireEvent.keyDown(box(), { key: 'Enter' }) }
const rightOf = (el: HTMLElement) => el.parentElement!

beforeEach(() => {
  cleanup(); vi.useFakeTimers()
  forgetScrollMemosWithPrefix(P)
  useHostConfigStore.setState({ byHost: {}, ensureLoaded: async () => {} })
  sendQueueFor(KEY, () => port)
})
afterEach(() => { cleanup(); clearAllCleared(); clearAllDrafts(); clearAllAttachments(); clearAllUploads(); clearAllSendQueues(); vi.useRealTimers() })

describe('chat mode keeps your messages on the right', () => {
  it('a message the App holds: right-aligned, bubble shape, accent frame kept, click takes it back', () => {
    mountChat()
    send('hold me')
    const m = screen.getByTestId('queued-message')
    expect(rightOf(m)).toHaveClass('items-end')
    expect(m).toHaveClass('max-w-[80%]', 'rounded-2xl', 'rounded-br-sm', 'border-accent')
    fireEvent.click(m)
    expect(box().value).toBe('hold me')
    expect(screen.queryByTestId('queued-message')).toBeNull()
  })

  it('a message in Claude Code\'s queue: right-aligned, bubble shape', () => {
    mountChat([user])
    const el = screen.getByTestId('header-queued')
    expect(el.parentElement).toHaveClass('items-end')
    expect(el).toHaveClass('max-w-[80%]', 'rounded-2xl', 'rounded-br-sm', 'border-accent')
    expect(el).toHaveTextContent('also check the logs')
  })

  it('a message already handed out is on the right too', async () => {
    mountChat()
    send('x')
    await act(async () => { await vi.advanceTimersByTimeAsync(UNDO_MS) })
    expect(rightOf(screen.getByTestId('queued-message'))).toHaveClass('items-end')
  })

  it('a peer\'s queued line stays where it was', () => {
    mountChat([peer])
    const el = screen.getByTestId('header-queued')
    expect(el.parentElement).not.toHaveClass('items-end')
    expect(el).not.toHaveClass('max-w-[80%]')
  })
})

describe('the deck keeps its full-width blocks', () => {
  it('held message and queued line are not right-aligned', () => {
    mountDeck([user])
    send('mine')
    for (const el of [screen.getByTestId('queued-message'), screen.getByTestId('header-queued')]) {
      expect(el).toHaveClass('rounded-md')
      expect(el).not.toHaveClass('max-w-[80%]')
      expect(el.parentElement).not.toHaveClass('items-end')
    }
  })
})
