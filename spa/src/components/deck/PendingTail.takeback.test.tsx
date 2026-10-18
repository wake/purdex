// K-2b (plan D7): take a message back — click it or press Up. The held block (App), Claude Code's queue line (header.queued) and an entry the daemon
// handed over come back through the bound adapter's takeBack(); the text lands in front of the draft. Real DeckView / ChatView, the pane's driver, the real input.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { act, cleanup, fireEvent, render, screen, within } from '@testing-library/react'
import { forgetFolds } from '../../lib/conversations/fold-memory'
import { forgetScrollMemosWithPrefix } from '../../lib/nex/transcript-scroll-memory'
import { emptyDoc } from '../../lib/conversations/model'
import type { Header, HeaderQueued, Turn } from '../../lib/conversations/types'
import type { ConversationEntry } from '../../stores/useConversationStore'
import { clearAllDrafts, draftKey, writeDraft } from '../../lib/conversations/draft-memory'
import { clearAllAttachments } from '../../lib/conversations/attachment-memory'
import { clearAllCleared } from '../../lib/conversations/draft-clear'
import { clearAllUploads } from '../../lib/conversations/attachment-upload'
import { UNDO_MS, clearAllSendQueues, sendQueueFor } from '../../lib/conversations/send-queue'
import type { RecallOutcome, SendOutcome, SendPort } from '../../lib/conversations/send'
import { bindAdapter, clearAllAdapters } from '../../lib/conversations/deck-adapter'
import { HIDE_MS, clearAllTakeBack } from '../../lib/conversations/take-back'
import { useSendQueueDriver } from '../../hooks/useSendQueueDriver'
import { shownHeaderQueue } from '../../hooks/usePending'
import { useHostConfigStore } from '../../stores/useHostConfigStore'
import { DeckView } from './DeckView'
import { ChatView } from './ChatView'
import { SessionInput } from './SessionInput'
import { footerContext } from './footer-context'

HTMLElement.prototype.scrollTo = function () {} as never

const H = 'h', S = 'sess-tb', P = 'p-tb'
const KEY = draftKey(P, H, S)
const turn: Turn = { id: 't0', index: 0, started_at: 1, outcome: 'running', items: [{ type: 'user', id: 'u0', at: 1, index: 0, text: 'go', source: 'user' }] }
const header = (queued?: HeaderQueued[]): Header => ({ title: 't', status: 'running', backend: 'terminal', live: true, ...(queued ? { queued } : {}) })
const row = (text: string, at = 4e12): HeaderQueued => ({ text, at, source: 'user' })
const peerRow: HeaderQueued = { text: 'from a peer', at: 4e12, source: 'peer', from: 'mlab/x' }
const entryOf = (h: Header): ConversationEntry => ({
  doc: { ...emptyDoc(), turns: [turn], header: h, capabilities: { send: 'keys' } }, status: 'live', reason: '', paging: false, subagents: {},
})
const calls: Array<{ text: string; resolve: (o: SendOutcome) => void }> = []
const port: SendPort = {
  submit: (text) => new Promise<SendOutcome>((resolve) => { calls.push({ text, resolve }) }),
  interrupt: () => Promise.resolve<SendOutcome>({ kind: 'accepted' }),
}
const recalls: Array<(o: RecallOutcome) => void> = []
const takeBack = vi.fn(() => new Promise<RecallOutcome>((resolve) => { recalls.push(resolve) }))
const answer = async (o: RecallOutcome) => { await act(async () => { recalls.shift()!(o); await vi.advanceTimersByTimeAsync(0) }) }

const footer = (ctx: ReturnType<typeof footerContext>) => <SessionInput {...ctx} onSwitchToTerminal={() => {}} sessionCode="c" pendingInStream />
function Pane({ h, view }: { h: Header; view: 'deck' | 'chat' }) {
  useSendQueueDriver(P, H, S, [], shownHeaderQueue(h.queued, h.status))
  if (view === 'chat') {
    return <ChatView paneKey={P} hostId={H} sessionId={S} title="t" status={h.status} queued={h.queued} turns={[turn]} onSwitchToTerminal={() => {}} active input={<SessionInput paneKey={P} hostId={H} sessionId={S} capabilities={{ send: 'keys' }} pendingInStream onSwitchToTerminal={() => {}} />} />
  }
  return <DeckView paneId={P} hostId={H} sessionId={S} onSwitchToTerminal={() => {}} entry={entryOf(h)} footer={footer} />
}
const mount = (h: Header, view: 'deck' | 'chat' = 'deck') => render(<Pane h={h} view={view} />)
const box = () => screen.getByRole('textbox') as HTMLTextAreaElement
const type = (v: string) => fireEvent.change(box(), { target: { value: v } })
const enter = () => fireEvent.keyDown(box(), { key: 'Enter' })
const up = () => fireEvent.keyDown(box(), { key: 'ArrowUp' })
const hold = (text: string) => { type(text); enter() }
const handOut = async (outcome: SendOutcome = { kind: 'handed', mode: 'fill' }) => {
  await act(async () => { await vi.advanceTimersByTimeAsync(UNDO_MS) })
  await act(async () => { calls[calls.length - 1].resolve(outcome); await vi.advanceTimersByTimeAsync(0) })
}
const headerRows = () => screen.queryAllByTestId('header-queued')

beforeEach(() => {
  cleanup()
  vi.useFakeTimers()
  calls.length = 0; recalls.length = 0; takeBack.mockClear()
  forgetFolds(`${P}\0${S}`)
  forgetScrollMemosWithPrefix(P)
  useHostConfigStore.setState({ byHost: {}, ensureLoaded: async () => {} })
  bindAdapter(KEY, { kind: 'terminal', canSend: () => true, recall: true, port: () => port, upload: null, takeBack })
  sendQueueFor(KEY, () => port)
})
afterEach(() => { cleanup(); clearAllCleared(); clearAllDrafts(); clearAllAttachments(); clearAllUploads(); clearAllSendQueues(); clearAllAdapters(); clearAllTakeBack(); vi.useRealTimers() })

describe.each(['deck', 'chat'] as const)('the %s', (view) => {
  it('the held block is a button as a whole; clicking it takes back that entry only', () => {
    mount(header(), view)
    hold('one'); hold('two')
    const blocks = screen.getAllByTestId('queued-message')
    expect(blocks[1]).toHaveAttribute('role', 'button')
    expect(blocks[1]).toHaveAttribute('title', 'Click to take it back')
    fireEvent.click(blocks[1])
    expect(box().value).toBe('two')
    expect(screen.getAllByTestId('queued-message')).toHaveLength(1)
    expect(screen.getByTestId('queued-message')).toHaveTextContent('one')
    expect(takeBack).not.toHaveBeenCalled() // an App-held entry needs no daemon
  })

  it('a header row with three queued: all three lines come back in front of the draft, the rows hide until the header catches up', async () => {
    const ui = mount(header([row('a'), row('b'), row('c')]), view)
    type('typed')
    fireEvent.click(headerRows()[1])
    expect(takeBack).toHaveBeenCalledTimes(1)
    await answer({ kind: 'back', text: 'a\nb\nc' })
    expect(box().value).toBe('a\nb\nc\ntyped')
    expect(headerRows()).toHaveLength(0) // hidden optimistically while header.queued still lists them
    ui.rerender(<Pane h={header([row('a'), row('b'), row('c')])} view={view} />)
    expect(headerRows()).toHaveLength(0)
    ui.rerender(<Pane h={header([])} view={view} />) // the header caught up
    ui.rerender(<Pane h={header([row('a')])} view={view} />) // a new message with the same text is shown again
    expect(headerRows()).toHaveLength(1)
  })

  it('the hide gives up after 5 s even if the header still lists the rows', async () => {
    mount(header([row('a')]), view)
    fireEvent.click(headerRows()[0])
    await answer({ kind: 'back', text: 'a' })
    expect(headerRows()).toHaveLength(0)
    await act(async () => { await vi.advanceTimersByTimeAsync(HIDE_MS) })
    expect(headerRows()).toHaveLength(1)
  })

  it('Up on an empty input with both kinds: Claude Code\'s text first, then the held text', async () => {
    mount(header([row('queued by claude')]), view)
    hold('held one')
    up()
    expect(takeBack).toHaveBeenCalledTimes(1)
    expect(box().value).toBe('held one') // the held entry is back at once
    await answer({ kind: 'back', text: 'queued by claude' })
    expect(box().value).toBe('queued by claude\nheld one')
    expect(screen.queryByTestId('queued-message')).toBeNull()
  })

  it('a failed takeBack still returns the held entries and shows its message (dismissable)', async () => {
    mount(header([row('queued by claude')]), view)
    hold('held one')
    up()
    await answer({ kind: 'not_run', reason: 'input_not_empty', status: 409 })
    expect(box().value).toBe('held one')
    expect(screen.getByTestId('take-back-notice')).toHaveTextContent('The terminal input has text; nothing was taken back')
    expect(headerRows()).toHaveLength(1) // nothing came back: the row stays
    fireEvent.click(within(screen.getByTestId('take-back-notice')).getByRole('button', { name: 'Dismiss' }))
    expect(screen.queryByTestId('take-back-notice')).toBeNull()
  })

  it('may-have-popped says so', async () => {
    mount(header([row('x')]), view)
    fireEvent.click(headerRows()[0])
    await answer({ kind: 'may_have_popped', reason: 'read_failed' })
    expect(screen.getByTestId('take-back-notice')).toHaveTextContent('It may have been sent, or it is still in the terminal input')
    expect(box().value).toBe('')
  })

  it('a second click (or Up) while one is in flight is ignored; the row shows a spinner', async () => {
    mount(header([row('a')]), view)
    fireEvent.click(headerRows()[0])
    expect(screen.getByTestId('take-back-spinner')).toBeInTheDocument()
    fireEvent.click(headerRows()[0])
    up()
    expect(takeBack).toHaveBeenCalledTimes(1)
    await answer({ kind: 'back', text: 'a' })
    expect(screen.queryByTestId('take-back-spinner')).toBeNull()
  })

  it('a peer row is not clickable', () => {
    mount(header([peerRow]), view)
    const el = headerRows()[0]
    expect(el).not.toHaveAttribute('role')
    fireEvent.click(el)
    expect(takeBack).not.toHaveBeenCalled()
  })

  it('an entry the daemon handed over (no row stands for it) is clickable and goes away when its text comes back', async () => {
    mount(header(), view)
    hold('sent already')
    await handOut()
    const m = screen.getByTestId('queued-message')
    expect(m).toHaveAttribute('data-state', 'sent')
    expect(m).toHaveAttribute('role', 'button')
    fireEvent.click(m)
    expect(takeBack).toHaveBeenCalledTimes(1)
    await answer({ kind: 'back', text: 'sent already' })
    expect(box().value).toBe('sent already')
    expect(screen.queryByTestId('queued-message')).toBeNull()
  })

  it('nothing queued: the notice says it was already sent; the handed entry stays', async () => {
    mount(header(), view)
    hold('sent already')
    await handOut()
    fireEvent.click(screen.getByTestId('queued-message'))
    await answer({ kind: 'not_run', reason: 'nothing_queued', status: 409 })
    expect(screen.getByTestId('take-back-notice')).toHaveTextContent('Already sent; it cannot be taken back')
    expect(screen.getByTestId('queued-message')).toBeInTheDocument()
  })
})

describe('the keyboard', () => {
  it.each(['Enter', ' '])('%j on the held block takes it back', (key) => {
    mount(header())
    hold('one')
    fireEvent.keyDown(screen.getByTestId('queued-message'), { key })
    expect(box().value).toBe('one')
  })

  it('Enter on a header row takes Claude Code\'s queue back', () => {
    mount(header([row('a')]))
    fireEvent.keyDown(headerRows()[0], { key: 'Enter' })
    expect(takeBack).toHaveBeenCalledTimes(1)
  })

  it('Up with nothing held and no header rows does nothing (the caret\'s)', () => {
    mount(header())
    up()
    expect(takeBack).not.toHaveBeenCalled()
  })

  it('a worker (no takeBack): header rows are not clickable and Up asks nothing', () => {
    bindAdapter(KEY, { kind: 'worker', canSend: () => true, recall: true, port: () => port, upload: null })
    mount(header([row('a')]))
    expect(headerRows()[0]).not.toHaveAttribute('role')
    up()
    expect(takeBack).not.toHaveBeenCalled()
  })

  it('a draft already typed, the caret on its first line: Up takes Claude Code\'s queue back (MS2-4, L5)', () => {
    mount(header([row('a')]))
    writeDraft(KEY, 'typed')
    type('typed')
    up()
    expect(takeBack).toHaveBeenCalledTimes(1)
  })
})
