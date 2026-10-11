// K-2a-3 (plan D6): a message the daemon handed over keeps the pending frame until the transcript has it, and is not drawn twice while Claude Code's
// own queue line renders the same text. Real DeckView / ChatView with the pane's driver (it reports the rendered header line to the queue).
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { act, cleanup, fireEvent, render, screen } from '@testing-library/react'
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
import { useSendQueueDriver } from '../../hooks/useSendQueueDriver'
import { shownHeaderQueue } from '../../hooks/usePending'
import { useHostConfigStore } from '../../stores/useHostConfigStore'
import { DeckView } from './DeckView'
import { ChatView } from './ChatView'
import { SessionInput } from './SessionInput'
import { footerContext } from './footer-context'

HTMLElement.prototype.scrollTo = function () {} as never

const H = 'h', S = 'sess-2', P = 'p-handed'
const KEY = draftKey(P, H, S)
const turn: Turn = { id: 't0', index: 0, started_at: 1, outcome: 'running', items: [{ type: 'user', id: 'u0', at: 1, index: 0, text: 'go', source: 'user' }] }
const header = (status: string, queued?: HeaderQueued[]): Header => ({ title: 't', status, backend: 'terminal', live: true, ...(queued ? { queued } : {}) })
const line = (text: string): HeaderQueued => ({ text, at: 5, source: 'user' })
const entryOf = (h: Header): ConversationEntry => ({
  doc: { ...emptyDoc(), turns: [turn], header: h, capabilities: { send: 'keys' } }, status: 'live', reason: '', paging: false, subagents: {},
})
const calls: Array<{ text: string; resolve: (o: SendOutcome) => void }> = []
const port: SendPort = {
  submit: (text) => new Promise<SendOutcome>((resolve) => { calls.push({ text, resolve }) }),
  interrupt: () => Promise.resolve<SendOutcome>({ kind: 'accepted' }),
}
const footer = (ctx: ReturnType<typeof footerContext>) => <SessionInput {...ctx} onSwitchToTerminal={() => {}} sessionCode="c" pendingInStream />
const props = { paneId: P, hostId: H, sessionId: S, onSwitchToTerminal: () => {} }

/** The pane: its driver reports the rendered header line, its view draws the stream and the tail. */
function Pane({ h, view }: { h: Header; view: 'deck' | 'chat' }) {
  useSendQueueDriver(P, H, S, [], shownHeaderQueue(h.queued, h.status))
  if (view === 'chat') {
    return <ChatView paneKey={P} hostId={H} sessionId={S} title="t" status={h.status} queued={h.queued} turns={[turn]} onSwitchToTerminal={() => {}} active input={<SessionInput paneKey={P} hostId={H} sessionId={S} capabilities={{ send: 'keys' }} pendingInStream onSwitchToTerminal={() => {}} />} />
  }
  return <DeckView {...props} entry={entryOf(h)} footer={footer} />
}
const mount = (h: Header, view: 'deck' | 'chat' = 'deck') => render(<Pane h={h} view={view} />)
const box = () => screen.getByRole('textbox') as HTMLTextAreaElement
const send = async (text: string, outcome: SendOutcome) => {
  fireEvent.change(box(), { target: { value: text } })
  fireEvent.keyDown(box(), { key: 'Enter' })
  await act(async () => { await vi.advanceTimersByTimeAsync(UNDO_MS) })
  await act(async () => { calls[calls.length - 1].resolve(outcome); await vi.advanceTimersByTimeAsync(0) })
}
const handed: SendOutcome = { kind: 'handed', mode: 'fill' }

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

describe.each(['deck', 'chat'] as const)('the %s', (view) => {
  it('a handed message keeps the pending frame (accent, not a grey card) until the transcript has it', async () => {
    mount(header('running'), view)
    await send('queued by claude', handed)
    const m = screen.getByTestId('queued-message')
    expect(m).toHaveAttribute('data-state', 'sent')
    expect(m).toHaveClass('border-accent', 'bg-accent/12')
    expect(m).toHaveTextContent('Sent')
    expect(m).not.toHaveTextContent('not confirmed')
  })

  it('while Claude Code\'s queue line renders the same text the entry is hidden — the line stands for it, once', async () => {
    const ui = mount(header('running'), view)
    await send('queued by claude', handed)
    expect(screen.getAllByTestId('queued-message')).toHaveLength(1)
    ui.rerender(<Pane h={header('running', [line('queued by claude\n')])} view={view} />)
    expect(screen.queryByTestId('queued-message')).toBeNull()
    expect(screen.getAllByTestId('header-queued')).toHaveLength(1)
  })

  it('the line stops being rendered with no echo: the entry is shown again', async () => {
    const ui = mount(header('running', [line('queued by claude')]), view)
    await send('queued by claude', handed)
    expect(screen.queryByTestId('queued-message')).toBeNull()
    ui.rerender(<Pane h={header('running', [])} view={view} />)
    expect(screen.getByTestId('queued-message')).toHaveAttribute('data-state', 'sent')
  })

  it('status idle: the header line is not rendered at all, so the entry is shown even if header.queued still lists it', async () => {
    const ui = mount(header('running'), view)
    await send('queued by claude', handed)
    ui.rerender(<Pane h={header('idle', [line('queued by claude')])} view={view} />)
    expect(screen.queryByTestId('header-queued')).toBeNull()
    expect(screen.getByTestId('queued-message')).toBeInTheDocument()
  })

  it('keys mode says it was not confirmed by the mod while it is pending', async () => {
    mount(header('running'), view)
    await send('by keys', { kind: 'handed', mode: 'keys' })
    expect(screen.getByTestId('queued-message')).toHaveTextContent('not confirmed by the mod')
  })

  it('handed and 10 s with neither a line nor an echo: shown as possibly sent', async () => {
    mount(header('running'), view)
    await send('lost one', handed)
    await act(async () => { await vi.advanceTimersByTimeAsync(10_000) })
    expect(screen.getByTestId('queued-message')).toHaveAttribute('data-state', 'maybe')
    expect(screen.getByTestId('queued-message')).toHaveTextContent('May have been sent')
  })
})
