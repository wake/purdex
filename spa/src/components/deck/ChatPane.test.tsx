// U3 mount: the chat inside the session pane. Every conversation state has its picture (D11); the footer is the same one the
// deck gets; the pane takes focus on its input.
import { describe, it, expect, beforeEach, vi } from 'vitest'
import { act, cleanup, fireEvent, render, screen } from '@testing-library/react'
import { emptyDoc } from '../../lib/conversations/model'
import { clearAllPanels } from '../../lib/conversations/panel-memory'
import type { ConversationItem, Turn } from '../../lib/conversations/types'
import type { ConversationEntry } from '../../stores/useConversationStore'
import type { PaneConversation } from '../../hooks/useConversationOfPane'
import { ChatPane } from './ChatPane'

const userItem = (id: string, index: number, text = id): ConversationItem => ({ type: 'user', id, at: 1, index, text, source: 'user' })
const turn = (index: number, items: ConversationItem[]): Turn => ({ id: `t${index}`, index, started_at: index, outcome: 'done', items })
const entry = (turns: Turn[], over: Partial<ConversationEntry> = {}, doc: object = {}): ConversationEntry => ({
  doc: { ...emptyDoc(), turns, ...doc }, status: 'live', reason: '', paging: false, subagents: {}, ...over,
})
const ready = (e: ConversationEntry | undefined): PaneConversation => ({ state: 'ready', hostId: 'h', sessionId: 's', entry: e })

const base = { paneId: 'p-chat', title: 'my tab', isActive: true, isFocusTarget: false, onSwitchToTerminal: vi.fn() }

beforeEach(() => { cleanup(); clearAllPanels(); base.onSwitchToTerminal.mockClear() })

describe('ChatPane', () => {
  it('draws the chat once the conversation is ready, titled by the conversation, with the status', () => {
    render(<ChatPane {...base} conversation={ready(entry([turn(0, [userItem('a', 0)])], {}, { header: { title: 'conv title', status: 'running', backend: 'terminal', live: true } }))} />)
    expect(screen.getByTestId('session-view-chat')).toBeInTheDocument()
    expect(screen.getByTestId('chat-user')).toBeInTheDocument()
    expect(screen.getByTestId('chat-header-title')).toHaveTextContent('conv title')
    expect(screen.getByTestId('chat-header-status')).toHaveAttribute('data-status', 'running')
  })

  it('falls back to the tab name until the conversation has a header', () => {
    render(<ChatPane {...base} conversation={ready(entry([turn(0, [userItem('a', 0)])]))} />)
    expect(screen.getByTestId('chat-header-title')).toHaveTextContent('my tab')
  })

  it('off (not a Claude Code session) says no_session and offers the terminal, which only a click uses', () => {
    render(<ChatPane {...base} conversation={{ state: 'off' }} />)
    expect(screen.getByTestId('unreadable')).toHaveAttribute('data-reason', 'no_session')
    expect(base.onSwitchToTerminal).not.toHaveBeenCalled()
    fireEvent.click(screen.getByTestId('unreadable-terminal'))
    expect(base.onSwitchToTerminal).toHaveBeenCalledTimes(1)
  })

  it.each([
    ['no_session', 'no_session'], ['not_found', 'not_found'], ['provider_unsupported', 'unsupported'], ['unreachable', 'offline'],
  ] as const)('unreadable %s is the chat\'s %s', (reason, shown) => {
    render(<ChatPane {...base} conversation={{ state: 'unreadable', reason, retry: vi.fn() }} />)
    expect(screen.getByTestId('unreadable')).toHaveAttribute('data-reason', shown)
    expect(screen.getByTestId('unreadable-terminal')).toBeInTheDocument()
  })

  it('an unreachable host can be retried', () => {
    const retry = vi.fn()
    render(<ChatPane {...base} conversation={{ state: 'unreadable', reason: 'unreachable', retry }} />)
    fireEvent.click(screen.getByTestId('unreadable-retry'))
    expect(retry).toHaveBeenCalledTimes(1)
  })

  it('resolving, no entry yet, and an entry still loading with nothing held all read as loading', () => {
    const { rerender } = render(<ChatPane {...base} conversation={{ state: 'resolving' }} />)
    expect(screen.getByTestId('chat-loading')).toBeInTheDocument()
    rerender(<ChatPane {...base} conversation={ready(undefined)} />)
    expect(screen.getByTestId('chat-loading')).toBeInTheDocument()
    rerender(<ChatPane {...base} conversation={ready(entry([], { status: 'loading' }))} />)
    expect(screen.getByTestId('chat-loading')).toBeInTheDocument()
  })

  it('an errored conversation with nothing held is offline; with turns held it keeps showing them', () => {
    const { rerender } = render(<ChatPane {...base} conversation={ready(entry([], { status: 'error' }))} />)
    expect(screen.getByTestId('unreadable')).toHaveAttribute('data-reason', 'offline')
    rerender(<ChatPane {...base} conversation={ready(entry([turn(0, [userItem('a', 0)])], { status: 'error' }))} />)
    expect(screen.getByTestId('chat-user')).toBeInTheDocument()
  })

  it('a first turn with no items is 「還沒有內容」 and becomes the chat when an item lands', () => {
    const { rerender } = render(<ChatPane {...base} conversation={ready(entry([turn(0, [])]))} />)
    expect(screen.getByTestId('unreadable')).toHaveAttribute('data-reason', 'empty')
    rerender(<ChatPane {...base} conversation={ready(entry([turn(0, [userItem('a', 0)])]))} />)
    expect(screen.queryByTestId('unreadable')).toBeNull()
    expect(screen.getByTestId('chat-user')).toBeInTheDocument()
  })

  it('hands its footer the deck\'s context and draws it as the chat\'s input', () => {
    const footer = vi.fn(() => <textarea data-testid="chat-input" />)
    const doc = { header: { title: 't', status: 'idle', backend: 'terminal', live: true } }
    render(<ChatPane {...base} footer={footer} conversation={ready(entry([turn(0, [userItem('a', 0)])], {}, doc))} />)
    expect(screen.getByTestId('chat-input')).toBeInTheDocument()
    const ctx = (footer.mock.calls[0] as unknown as [Record<string, unknown>])[0]
    expect(ctx).toMatchObject({ paneKey: 'p-chat', hostId: 'h', sessionId: 's', idle: true })
    expect((ctx.items as unknown[]).length).toBe(1)
  })

  it('takes focus on the input when the pane becomes the focus target, else the frame', () => {
    const footer = () => <textarea data-testid="chat-input" />
    vi.useFakeTimers()
    render(<ChatPane {...base} isFocusTarget footer={footer} conversation={ready(entry([turn(0, [userItem('a', 0)])]))} />)
    act(() => { vi.runAllTimers() })
    vi.useRealTimers()
    expect(document.activeElement).toBe(screen.getByTestId('chat-input'))
    cleanup()
    vi.useFakeTimers()
    render(<ChatPane {...base} isFocusTarget conversation={{ state: 'off' }} />)
    act(() => { vi.runAllTimers() })
    vi.useRealTimers()
    expect(document.activeElement).toBe(screen.getByTestId('session-view-chat'))
  })
})
