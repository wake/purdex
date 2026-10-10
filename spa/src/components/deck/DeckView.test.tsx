import { describe, it, expect, beforeEach, vi } from 'vitest'
import { act, cleanup, fireEvent, render, screen } from '@testing-library/react'
import { forgetFolds } from '../../lib/conversations/fold-memory'
import { forgetScrollMemosWithPrefix, readScrollMemo } from '../../lib/nex/transcript-scroll-memory'
import { emptyDoc } from '../../lib/conversations/model'
import type { ConversationItem, Turn } from '../../lib/conversations/types'
import { useConversationStore, type ConversationEntry } from '../../stores/useConversationStore'
import { DeckPane } from './DeckPane'
import { DeckView } from './DeckView'

const userItem = (id: string, index: number, text = id): ConversationItem => ({ type: 'user', id, at: 1, index, text, source: 'user' })
const out = (n: number) => ({ text: Array.from({ length: n }, (_, i) => `r${i + 1}`).join('\n'), total_lines: n, total_bytes: 1, truncated: false })
const exec = (id: string, index: number): ConversationItem => ({
  type: 'step', id, at: 1, index, kind: 'execute', tool: 'Bash', status: 'done', summary: 'ls', started_at: 1, input: null,
  command: { text: 'ls' }, output: out(30),
}) as ConversationItem
const turn = (index: number, items: ConversationItem[], over: Partial<Turn> = {}): Turn =>
  ({ id: `t${index}`, index, started_at: index, outcome: 'done', items, ...over })
const entry = (turns: Turn[], over: Partial<ConversationEntry> = {}, doc: object = {}): ConversationEntry => ({
  doc: { ...emptyDoc(), turns, ...doc }, status: 'live', reason: '', paging: false, subagents: {}, ...over,
})

const props = { paneId: 'p-deck', hostId: 'h', sessionId: 's', onSwitchToTerminal: () => {} }
const loadBefore = vi.fn(async () => {})

beforeEach(() => {
  cleanup()
  forgetFolds('p-deck\0s')
  forgetScrollMemosWithPrefix('p-deck')
  loadBefore.mockClear()
  useConversationStore.setState({ loadBefore })
})

describe('DeckView', () => {
  it('draws every turn in order, each marked with its index', () => {
    render(<DeckView {...props} entry={entry([turn(3, [userItem('a', 0)]), turn(4, [userItem('b', 0)])])} />)
    const turns = screen.getAllByTestId('deck-turn')
    expect(turns.map((t) => t.getAttribute('data-turn-index'))).toEqual(['3', '4'])
    expect(turns[0].className).toContain('scroll-anchor')
  })

  it('says how many items a too-long turn left out, and shows a turn error', () => {
    render(<DeckView {...props} entry={entry([turn(0, [userItem('a', 5)], { omitted_items: 5, error: { kind: 'api', message: 'boom' } })])} />)
    expect(screen.getByTestId('deck-omitted')).toHaveTextContent('5 earlier step(s) not shown')
    expect(screen.getByTestId('deck-turn-error')).toHaveTextContent('boom')
  })

  it('says 還沒有內容 for a conversation without items, and loading while it loads', () => {
    render(<DeckView {...props} entry={entry([turn(0, [])])} />)
    expect(screen.getByTestId('deck-empty')).toHaveTextContent('Nothing here yet')
    cleanup()
    render(<DeckView {...props} entry={entry([], { status: 'loading' })} />)
    expect(screen.getByTestId('deck-empty')).toHaveTextContent('Loading')
  })

  it('reads the next older page when the reader reaches the top, once while one is in flight', () => {
    const e = entry([turn(5, [userItem('a', 0)])], {}, { hasMoreBefore: true })
    const { rerender } = render(<DeckView {...props} entry={e} />)
    const box = screen.getByTestId('deck-scroll')
    box.scrollTop = 0
    fireEvent.scroll(box)
    expect(loadBefore).toHaveBeenCalledWith('h', 's')
    loadBefore.mockClear()
    rerender(<DeckView {...props} entry={{ ...e, paging: true }} />)
    fireEvent.scroll(screen.getByTestId('deck-scroll'))
    expect(loadBefore).not.toHaveBeenCalled()
    expect(screen.getByTestId('deck-paging')).toBeInTheDocument()
  })

  it('asks for the next page by itself when the content does not fill the box (no scroll event can come)', () => {
    const e = entry([turn(5, [userItem('a', 0)])], {}, { hasMoreBefore: true })
    const { rerender } = render(<DeckView {...props} entry={e} />)
    expect(loadBefore).toHaveBeenCalledTimes(1)
    // the page lands (first turn 3): still short, ask again
    rerender(<DeckView {...props} entry={entry([turn(3, [userItem('b', 0)]), turn(5, [userItem('a', 0)])], {}, { hasMoreBefore: true })} />)
    expect(loadBefore).toHaveBeenCalledTimes(2)
    // nothing older any more: stop
    rerender(<DeckView {...props} entry={entry([turn(1, [userItem('c', 0)]), turn(3, [userItem('b', 0)])], {}, { hasMoreBefore: false })} />)
    expect(loadBefore).toHaveBeenCalledTimes(2)
  })

  it('asks again after a page that failed or came empty — with a growing wait, three tries, no busy loop', () => {
    vi.useFakeTimers()
    const e = entry([turn(5, [userItem('a', 0)])], {}, { hasMoreBefore: true })
    const { rerender } = render(<DeckView {...props} entry={e} />)
    const settle = () => {
      rerender(<DeckView {...props} entry={{ ...e, paging: true }} />)
      rerender(<DeckView {...props} entry={{ ...e, paging: false }} />)
    }
    settle()
    expect(loadBefore).toHaveBeenCalledTimes(1) // not at once
    act(() => { vi.advanceTimersByTime(999) })
    expect(loadBefore).toHaveBeenCalledTimes(1)
    act(() => { vi.advanceTimersByTime(1) })
    expect(loadBefore).toHaveBeenCalledTimes(2)
    settle()
    act(() => { vi.advanceTimersByTime(2000) })
    expect(loadBefore).toHaveBeenCalledTimes(3)
    settle()
    act(() => { vi.advanceTimersByTime(60_000) })
    expect(loadBefore).toHaveBeenCalledTimes(3) // given up until the reader scrolls to the top
    vi.useRealTimers()
  })

  it('a page that lands starts the tries over', () => {
    vi.useFakeTimers()
    const e = entry([turn(5, [userItem('a', 0)])], {}, { hasMoreBefore: true })
    const { rerender } = render(<DeckView {...props} entry={e} />)
    rerender(<DeckView {...props} entry={{ ...e, paging: true }} />)
    rerender(<DeckView {...props} entry={{ ...e, paging: false }} />)
    act(() => { vi.advanceTimersByTime(1000) })
    expect(loadBefore).toHaveBeenCalledTimes(2)
    rerender(<DeckView {...props} entry={entry([turn(3, [userItem('b', 0)]), turn(5, [userItem('a', 0)])], {}, { hasMoreBefore: true })} />)
    expect(loadBefore).toHaveBeenCalledTimes(3) // at once: the first turn moved
    vi.useRealTimers()
  })

  it('does not ask on its own when the content already overflows the box', () => {
    const spy = vi.spyOn(HTMLElement.prototype, 'scrollHeight', 'get').mockReturnValue(5000)
    vi.spyOn(HTMLElement.prototype, 'clientHeight', 'get').mockReturnValue(600)
    render(<DeckView {...props} entry={entry([turn(5, [userItem('a', 0)])], {}, { hasMoreBefore: true })} />)
    expect(loadBefore).not.toHaveBeenCalled()
    spy.mockRestore()
    vi.restoreAllMocks()
  })

  it('does not page when nothing older exists', () => {
    render(<DeckView {...props} entry={entry([turn(0, [userItem('a', 0)])])} />)
    fireEvent.scroll(screen.getByTestId('deck-scroll'))
    expect(loadBefore).not.toHaveBeenCalled()
  })

  it('offers the way back to the live end while detached, and says when it is reconnecting', () => {
    const returnToLive = vi.fn(async () => {})
    useConversationStore.setState({ returnToLive })
    render(<DeckView {...props} entry={entry([turn(0, [userItem('a', 0)])], { status: 'reconnecting' }, { detached: true })} />)
    expect(screen.getByTestId('deck-reconnecting')).toBeInTheDocument()
    fireEvent.click(screen.getByTestId('deck-back-live'))
    expect(returnToLive).toHaveBeenCalledWith('h', 's')
  })

  it('hands the footer what an input needs', () => {
    const footer = vi.fn(() => <div data-testid="the-footer" />)
    const e = entry([turn(0, [userItem('a', 0), userItem('b', 1)])], {}, { capabilities: { send: 'prompt' }, header: { title: 't', status: 'idle', backend: 'terminal', live: true } })
    render(<DeckView {...props} entry={e} footer={footer} />)
    expect(screen.getByTestId('the-footer')).toBeInTheDocument()
    const ctx = (footer.mock.calls as unknown as Array<[Record<string, unknown>]>)[0][0]
    expect(ctx).toMatchObject({ paneKey: 'p-deck', hostId: 'h', sessionId: 's', idle: true, capabilities: { send: 'prompt' } })
    expect((ctx.items as unknown[]).length).toBe(2)
    expect(typeof ctx.onSwitchToTerminal).toBe('function')
  })

  it('is not idle while the header says running or has not arrived', () => {
    const footer = vi.fn(() => null)
    render(<DeckView {...props} entry={entry([turn(0, [userItem('a', 0)])])} footer={footer} />)
    expect((footer.mock.calls as unknown as Array<[{ idle: boolean }]>)[0][0].idle).toBe(false)
  })

  it('keeps an opened output and the scroll memo across an unmount (a tab switch)', () => {
    const e = entry([turn(0, [exec('x1', 0)])])
    const first = render(<DeckView {...props} entry={e} />)
    fireEvent.click(screen.getByTestId('output-toggle'))
    expect(screen.getByTestId('output-body')).toBeInTheDocument()
    fireEvent.scroll(screen.getByTestId('deck-scroll'))
    first.unmount()
    expect(readScrollMemo('p-deck\0s')?.view).toBe('deck')
    render(<DeckView {...props} entry={e} />)
    expect(screen.getByTestId('output-body')).toBeInTheDocument()
  })
})

describe('DeckPane', () => {
  const base = { paneId: 'p-deck', isActive: true, isFocusTarget: false, onSwitchToTerminal: vi.fn() }

  it('an off or unreadable pane says why, offers the terminal and never switches by itself', () => {
    const onSwitchToTerminal = vi.fn()
    render(<DeckPane {...base} onSwitchToTerminal={onSwitchToTerminal} conversation={{ state: 'off' }} />)
    expect(screen.getByTestId('deck-unreadable')).toHaveAttribute('data-reason', 'no_session')
    expect(screen.getByTestId('deck-unreadable')).toHaveTextContent('No Claude Code conversation found for this tab')
    expect(onSwitchToTerminal).not.toHaveBeenCalled()
    fireEvent.click(screen.getByTestId('deck-to-terminal'))
    expect(onSwitchToTerminal).toHaveBeenCalledTimes(1)
  })

  it.each([
    ['no_session', 'No Claude Code conversation found for this tab', false],
    ['not_found', 'The conversation transcript has not appeared yet', true],
    ['provider_unsupported', 'This agent is not supported', false],
    ['unreachable', 'Cannot reach the host', true],
  ] as const)('%s reads %s, retry offered: %s', (reason, text, retry) => {
    const doRetry = vi.fn()
    render(<DeckPane {...base} conversation={{ state: 'unreadable', reason, retry: doRetry }} />)
    expect(screen.getByTestId('deck-unreadable')).toHaveTextContent(text)
    expect(screen.queryByTestId('deck-retry') !== null).toBe(retry)
    if (retry) {
      fireEvent.click(screen.getByTestId('deck-retry'))
      expect(doRetry).toHaveBeenCalled()
    }
  })

  it('shows loading while resolving or before the entry exists', () => {
    render(<DeckPane {...base} conversation={{ state: 'resolving' }} />)
    expect(screen.getByTestId('deck-loading')).toBeInTheDocument()
    cleanup()
    render(<DeckPane {...base} conversation={{ state: 'ready', hostId: 'h', sessionId: 's', entry: undefined }} />)
    expect(screen.getByTestId('deck-loading')).toBeInTheDocument()
  })

  it('a failed first read says the host cannot be reached; with turns held it keeps drawing them', () => {
    render(<DeckPane {...base} conversation={{ state: 'ready', hostId: 'h', sessionId: 's', entry: entry([], { status: 'error' }) }} />)
    expect(screen.getByTestId('deck-unreadable')).toHaveAttribute('data-reason', 'unreachable')
    cleanup()
    render(<DeckPane {...base} conversation={{ state: 'ready', hostId: 'h', sessionId: 's', entry: entry([turn(0, [userItem('a', 0)])], { status: 'error' }) }} />)
    expect(screen.getByTestId('deck-turn')).toBeInTheDocument()
  })

  it('a first turn with no items is unreadable (還沒有內容) with the way to the terminal, and becomes the deck when an item lands', () => {
    const conv = (turns: Turn[]) => ({ state: 'ready' as const, hostId: 'h', sessionId: 's', entry: entry(turns) })
    const { rerender } = render(<DeckPane {...base} conversation={conv([turn(0, [])])} />)
    expect(screen.getByTestId('deck-unreadable')).toHaveAttribute('data-reason', 'empty')
    expect(screen.getByTestId('deck-unreadable')).toHaveTextContent('Nothing here yet')
    expect(screen.getByTestId('deck-to-terminal')).toBeInTheDocument()
    expect(screen.queryByTestId('deck-retry')).toBeNull()
    rerender(<DeckPane {...base} conversation={conv([turn(0, [userItem('a', 0)])])} />)
    expect(screen.queryByTestId('deck-unreadable')).toBeNull()
    expect(screen.getByTestId('deck-turn')).toBeInTheDocument()
  })

  it('keeps the scroll place per session: another session of the same pane starts fresh', () => {
    const a = entry([turn(0, [userItem('a', 0)])])
    const first = render(<DeckPane {...base} conversation={{ state: 'ready', hostId: 'h', sessionId: 's', entry: a }} />)
    fireEvent.scroll(screen.getByTestId('deck-scroll'))
    expect(readScrollMemo('p-deck\0s')).toBeDefined()
    first.unmount()
    render(<DeckPane {...base} conversation={{ state: 'ready', hostId: 'h', sessionId: 's2', entry: a }} />)
    fireEvent.scroll(screen.getByTestId('deck-scroll'))
    expect(readScrollMemo('p-deck\0s2')).toBeDefined()
    expect(readScrollMemo('p-deck\0s')).toBeDefined()
  })

  it('takes focus on the input when the pane becomes the focus target, else the frame', () => {
    const footer = () => <textarea data-testid="deck-input" />
    const conv = { state: 'ready' as const, hostId: 'h', sessionId: 's', entry: entry([turn(0, [userItem('a', 0)])]) }
    vi.useFakeTimers()
    render(<DeckPane {...base} isFocusTarget conversation={conv} footer={footer} />)
    act(() => { vi.runAllTimers() })
    vi.useRealTimers()
    expect(document.activeElement).toBe(screen.getByTestId('deck-input'))
    cleanup()
    vi.useFakeTimers()
    render(<DeckPane {...base} isFocusTarget conversation={{ state: 'off' }} />)
    act(() => { vi.runAllTimers() })
    vi.useRealTimers()
    expect(document.activeElement).toBe(screen.getByTestId('session-view-deck'))
  })

  it('draws the deck once the conversation is ready', () => {
    render(<DeckPane {...base} conversation={{ state: 'ready', hostId: 'h', sessionId: 's', entry: entry([turn(0, [userItem('a', 0)])]) }} />)
    expect(screen.getByTestId('session-view-deck')).toBeInTheDocument()
    expect(screen.getByTestId('deck-turn')).toBeInTheDocument()
  })
})
