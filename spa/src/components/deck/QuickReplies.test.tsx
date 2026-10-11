// #2664: the quick replies above the deck / chat input (terminal and worker): a row of short texts from the host's list; a tap goes through the pane's
// own send queue like the Send button (so a terminal's 1.5 s undo window and a worker's Nexen path are the adapter's), leaves the typed draft alone,
// and the row steps aside for a dock card and hides where the conversation cannot be sent to.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { act, cleanup, fireEvent, render, screen } from '@testing-library/react'
import { QuickReplies } from './QuickReplies'
import { bindAdapter, clearAllAdapters, type DeckAdapter } from '../../lib/conversations/deck-adapter'
import { clearAllDrafts, draftKey, readDraft, writeDraft } from '../../lib/conversations/draft-memory'
import { sendQueueFor, UNDO_MS, clearAllSendQueues } from '../../lib/conversations/send-queue'
import { subscribeRecall } from '../../lib/conversations/draft-recall'
import { useHostConfigStore } from '../../stores/useHostConfigStore'
import { useWorkerSettingsStore } from '../../stores/useWorkerSettingsStore'
import { useQuickReplyPrefsStore } from '../../stores/useQuickReplyPrefsStore'
import { useHostStore } from '../../stores/useHostStore'
import { wireIdOfHost } from '../../lib/profile/host-identity'
import type { SendPort } from '../../lib/conversations/send'

const H = 'h', S = 's1', P = 'p1'
const KEY = draftKey(P, H, S)

const submit = vi.fn(async (_text: string, _id: string) => ({ kind: 'accepted' as const }))
const adapter = (canSend = true): DeckAdapter => ({
  kind: 'terminal', canSend: () => canSend, recall: true,
  port: (): SendPort => ({ submit, interrupt: async () => ({ kind: 'accepted' as const }) }),
  upload: null,
})
const ui = (over: Partial<Parameters<typeof QuickReplies>[0]> = {}) => (
  <QuickReplies paneKey={P} hostId={H} sessionId={S} capabilities={{ send: 'prompt' } as never} asking={false} {...over} />
)
/** A host whose config says these replies (the collection loaded once, so no defaults). */
function hostReplies(texts: string[], titled = false) {
  useHostConfigStore.setState({
    byHost: { [H]: { status: 'ready', quickRepliesSupported: true, quickReplies: texts.map((content, i) => ({ id: `r${i}`, title: titled ? `T${i}` : content, content })), revisions: { quickReplies: 1 }, problems: {} } },
    ensureLoaded: async () => {},
  } as never)
}

beforeEach(() => {
  vi.useFakeTimers()
  submit.mockClear()
  bindAdapter(KEY, adapter())
  sendQueueFor(KEY, () => adapter().port()) // the pane's driver makes the queue before the footer draws
  hostReplies(['continue', 'run the tests'])
  useWorkerSettingsStore.setState({ quickRepliesCollapsed: false })
  useQuickReplyPrefsStore.setState({ rowEnabled: true, disabled: {} })
})
afterEach(() => { cleanup(); vi.useRealTimers(); clearAllAdapters(); clearAllDrafts(); clearAllSendQueues() })

describe('QuickReplies', () => {
  it('a button shows the title, carries the content as its tooltip and sends the content', async () => {
    hostReplies(['continue please', 'run the tests'], true)
    render(ui())
    const buttons = screen.getAllByTestId('quick-reply')
    expect(buttons.map((b) => b.textContent)).toEqual(['T0', 'T1'])
    expect(buttons[1]).toHaveAttribute('title', 'run the tests')
    fireEvent.click(buttons[1])
    const q = sendQueueFor(KEY, () => adapter().port())
    expect(q.entries().map((e) => e.text)).toEqual(['run the tests'])
  })

  it('the destructive check looks at the content, not the title', () => {
    hostReplies(['rm -rf build'], true)
    render(ui())
    fireEvent.click(screen.getByTestId('quick-reply'))
    expect(screen.getByTestId('quick-reply-note')).toBeInTheDocument()
  })

  it('a pill per reply of the host\'s list', () => {
    render(ui())
    expect(screen.getAllByTestId('quick-reply').map((b) => b.textContent)).toEqual(['continue', 'run the tests'])
  })

  it('a host that never wrote a list shows the defaults; an emptied list shows no row at all', () => {
    useHostConfigStore.setState({ byHost: { [H]: { status: 'unsupported', quickRepliesSupported: false, quickReplies: [], revisions: { quickReplies: 0 }, problems: {} } } } as never)
    render(ui())
    expect(screen.getAllByTestId('quick-reply').length).toBeGreaterThan(0)
    cleanup()
    hostReplies([])
    const { container } = render(ui())
    expect(container).toBeEmptyDOMElement()
  })

  it('a tap queues that text on the pane\'s queue (the App holds it for the undo window), then the adapter sends it', async () => {
    render(ui())
    fireEvent.click(screen.getAllByTestId('quick-reply')[1])
    const q = sendQueueFor(KEY, () => adapter().port())
    expect(q.entries().map((e) => [e.text, e.state])).toEqual([['run the tests', 'undo']])
    expect(submit).not.toHaveBeenCalled()
    await act(async () => { await vi.advanceTimersByTimeAsync(UNDO_MS) })
    expect(submit).toHaveBeenCalledTimes(1)
    expect(submit.mock.calls[0][0]).toBe('run the tests')
  })

  it('the typed draft is left alone', () => {
    writeDraft(KEY, 'half a thought')
    render(ui())
    fireEvent.click(screen.getAllByTestId('quick-reply')[0])
    expect(readDraft(KEY)).toBe('half a thought')
  })

  it('a reply still inside its undo window can be taken back into the box', () => {
    const recalled = vi.fn()
    const off = subscribeRecall(KEY, recalled)
    render(ui())
    fireEvent.click(screen.getAllByTestId('quick-reply')[0])
    const q = sendQueueFor(KEY, () => adapter().port())
    const id = q.entries()[0].id
    expect(q.undo(id)).toBe('continue')
    off()
  })

  it('steps aside while a question or a permission card waits', () => {
    const { container, rerender } = render(ui({ asking: true }))
    expect(container).toBeEmptyDOMElement()
    rerender(ui({ asking: false }))
    expect(screen.getAllByTestId('quick-reply')).toHaveLength(2)
  })

  it('is hidden where the conversation cannot be sent to (no send capability)', () => {
    bindAdapter(KEY, adapter(false))
    const { container } = render(ui({ capabilities: undefined }))
    expect(container).toBeEmptyDOMElement()
  })

  it('is hidden while the pane says it is locked', () => {
    const { container } = render(ui({ locked: true }))
    expect(container).toBeEmptyDOMElement()
  })

  it('the toggle folds the row to its chevron, and the choice outlives the component (a tab switch)', () => {
    const { unmount } = render(ui())
    fireEvent.click(screen.getByTestId('quick-reply-toggle'))
    expect(screen.queryAllByTestId('quick-reply')).toHaveLength(0)
    unmount()
    render(ui())
    expect(screen.queryAllByTestId('quick-reply')).toHaveLength(0)
    expect(screen.getByTestId('quick-reply-toggle')).toHaveAttribute('aria-expanded', 'false')
    fireEvent.click(screen.getByTestId('quick-reply-toggle'))
    expect(screen.getAllByTestId('quick-reply')).toHaveLength(2)
  })

  it('a destructive-looking reply needs a second tap, like the Send button', () => {
    hostReplies(['rm -rf build'])
    render(ui())
    fireEvent.click(screen.getByTestId('quick-reply'))
    const q = sendQueueFor(KEY, () => adapter().port())
    expect(q.entries()).toHaveLength(0)
    expect(screen.getByTestId('quick-reply-note')).toBeInTheDocument()
    fireEvent.click(screen.getByTestId('quick-reply'))
    expect(q.entries().map((e) => e.text)).toEqual(['rm -rf build'])
  })
  // atk of #2685: the confirmation belongs to the conversation and to the moment it was asked in
  it('a second tap in ANOTHER session does not count as the confirmation of the first (the pane moved on: /clear, a relay)', () => {
    hostReplies(['rm -rf build'])
    const { rerender } = render(ui())
    fireEvent.click(screen.getByTestId('quick-reply'))
    expect(screen.getByTestId('quick-reply-note')).toBeInTheDocument()
    const S2 = 's2'
    const KEY2 = draftKey(P, H, S2)
    bindAdapter(KEY2, adapter())
    sendQueueFor(KEY2, () => adapter().port())
    rerender(ui({ sessionId: S2 }))
    expect(screen.queryByTestId('quick-reply-note')).toBeNull()
    fireEvent.click(screen.getByTestId('quick-reply'))
    expect(sendQueueFor(KEY2, () => adapter().port()).entries()).toHaveLength(0) // asks anew
    expect(screen.getByTestId('quick-reply-note')).toBeInTheDocument()
  })

  it('the row stepping aside (a card, a lost stream) disarms the confirmation: it asks again when the row is back', () => {
    hostReplies(['rm -rf build'])
    const { rerender } = render(ui())
    fireEvent.click(screen.getByTestId('quick-reply'))
    rerender(ui({ asking: true }))
    rerender(ui({ asking: false }))
    expect(screen.queryByTestId('quick-reply-note')).toBeNull()
    fireEvent.click(screen.getByTestId('quick-reply'))
    expect(sendQueueFor(KEY, () => adapter().port()).entries()).toHaveLength(0)
    rerender(ui({ locked: true }))
    rerender(ui({ locked: false }))
    fireEvent.click(screen.getByTestId('quick-reply'))
    expect(sendQueueFor(KEY, () => adapter().port()).entries()).toHaveLength(0)
  })

  it('the confirmation is announced (a status region) and tied to the button that is waiting for it', () => {
    hostReplies(['rm -rf build', 'continue'])
    render(ui())
    const [danger, other] = screen.getAllByTestId('quick-reply')
    expect(danger).not.toHaveAttribute('aria-describedby')
    fireEvent.click(danger)
    const note = screen.getByTestId('quick-reply-note')
    expect(note).toHaveAttribute('role', 'status')
    expect(danger).toHaveAttribute('aria-describedby', note.id)
    expect(other).not.toHaveAttribute('aria-describedby')
  })
})

// QR-2b: the Mac's switches (the prefs store, keyed by WIRE host id) hide the whole row or single items
describe('QuickReplies — the switches', () => {
  it('the row switched off: nothing is drawn, not even the toggle', () => {
    useQuickReplyPrefsStore.setState({ rowEnabled: false })
    const { container } = render(ui())
    expect(container).toBeEmptyDOMElement()
  })

  it('an item switched off for this host is not drawn; the others are; an id the host no longer lists is ignored', () => {
    useQuickReplyPrefsStore.setState({ disabled: { [H]: ['r0', 'long-gone'] } })
    render(ui())
    expect(screen.getAllByTestId('quick-reply').map((b) => b.textContent)).toEqual(['run the tests'])
  })

  it('every item switched off leaves no row (nothing to show)', () => {
    useQuickReplyPrefsStore.setState({ disabled: { [H]: ['r0', 'r1'] } })
    const { container } = render(ui())
    expect(container).toBeEmptyDOMElement()
  })

  it('another host\'s off-list does not touch this one', () => {
    useQuickReplyPrefsStore.setState({ disabled: { other: ['r0', 'r1'] } })
    render(ui())
    expect(screen.getAllByTestId('quick-reply')).toHaveLength(2)
  })

  it('the host is read under its WIRE id (d1_…), not its local id; a local-id entry the pass has not moved yet is the host\'s too', () => {
    useHostStore.setState({ hosts: { [H]: { id: H, name: 'mlab', ip: '1.2.3.4', port: 7860, token: 't', order: 0, daemonId: 'abc123' } }, hostOrder: [H] })
    const wire = wireIdOfHost(useHostStore.getState().hosts[H])
    expect(wire).not.toBe(H)
    useQuickReplyPrefsStore.setState({ disabled: { [wire]: ['r0'] } })
    render(ui())
    expect(screen.getAllByTestId('quick-reply').map((b) => b.textContent)).toEqual(['run the tests'])
    cleanup()
    useQuickReplyPrefsStore.setState({ disabled: { [H]: ['r0'] } })
    render(ui())
    expect(screen.getAllByTestId('quick-reply').map((b) => b.textContent)).toEqual(['run the tests'])
    useHostStore.setState({ hosts: {}, hostOrder: [] })
  })

  it('a switch flipped while the row is shown takes effect at once', () => {
    render(ui())
    act(() => { useQuickReplyPrefsStore.getState().setItemDisabled(H, 'r1', true) })
    expect(screen.getAllByTestId('quick-reply').map((b) => b.textContent)).toEqual(['continue'])
    act(() => { useQuickReplyPrefsStore.getState().setRowEnabled(false) })
    expect(screen.queryByTestId('quick-reply')).toBeNull()
  })
})
