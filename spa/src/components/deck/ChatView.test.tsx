// The chat (U3 spec §5, plan D9–D11). Real conversations come from the golden fixtures (testdata/conversation/v1); the few
// shapes the goldens do not hold (a running chain, an unverified peer, a two-file turn) are hand-made and marked so.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { act, cleanup, fireEvent, render, screen, within } from '@testing-library/react'
import { ChatView, type ChatViewProps } from './ChatView'
import { TabContent } from '../TabContent'
import { registerModule, clearModuleRegistry, type PaneRendererProps } from '../../lib/module-registry'
import { useUISettingsStore } from '../../stores/useUISettingsStore'
import { useShownHostsStore } from '../../stores/useShownHostsStore'
import { useHostConfigStore } from '../../stores/useHostConfigStore'
import { createTab } from '../../types/tab'
import type { Tab } from '../../types/tab'
import { chatScrollKey, conversationBinding, clearAllPanels, readPanel } from '../../lib/conversations/panel-memory'
import { forgetFolds } from '../../lib/conversations/fold-memory'
import { forgetScrollMemo, readScrollMemo } from '../../lib/nex/transcript-scroll-memory'
import { buildChat, fileSummary } from '../../lib/conversations/chat-model'
import { turnRows } from '../../lib/conversations/turn-row'
import type { ConversationItem, StepItem, UserItem } from '../../lib/conversations/types'
import type { PanelTurn } from '../../lib/conversations/panel-resolve'
import pluginSubmit from '../../../../testdata/conversation/v1/cc-transcript/plugin-submit/expected.json'
import peerMessage from '../../../../testdata/conversation/v1/cc-transcript/peer-message/expected.json'
import editWrite from '../../../../testdata/conversation/v1/cc-transcript/edit-write-multiedit/expected.json'
import denial from '../../../../testdata/conversation/v1/cc-transcript/denial-kinds/expected.json'

const PANE = 'chat-test'
const BIND = conversationBinding('h', 'session-1')
type Fx = { conversation: { turns: Array<{ id: string; index: number; items: unknown[] }> } }
const turnsOf = (f: unknown): PanelTurn[] =>
  (f as Fx).conversation.turns.map((t) => ({ id: t.id, index: t.index, items: t.items.map((it, index) => ({ ...(it as object), index }) as ConversationItem) }))

const props = (over: Partial<ChatViewProps> = {}): ChatViewProps =>
  ({ paneKey: PANE, hostId: 'h', sessionId: 'session-1', title: 'my tab', status: 'idle', turns: turnsOf(pluginSubmit), onSwitchToTerminal: vi.fn(), active: true, ...over })
const mount = (over: Partial<ChatViewProps> = {}) => render(<ChatView {...props(over)} />)

// hand-made helpers
const step = (id: string, over: Partial<StepItem> = {}): StepItem =>
  ({ type: 'step', id, at: 1, index: 0, kind: 'execute', tool: 'Bash', status: 'done', summary: id, started_at: 1000, duration_ms: 1000, input: {}, ...over })
const peer = (id: string, over: Partial<UserItem> = {}): UserItem => ({ type: 'user', id, at: 1, index: 0, text: `msg ${id}`, source: 'peer', from: { kind: 'peer', name: 'host/a' }, ...over })
const turn = (id: string, index: number, items: ConversationItem[]): PanelTurn => ({ id, index, items })

// jsdom lays nothing out; the split (docked beside the panel, or overlaid) needs a pane width. 1000 px docks.
const WIDTH = { px: 1000 }
beforeEach(() => {
  vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockImplementation(() => ({ width: WIDTH.px, height: 600, top: 0, left: 0, right: WIDTH.px, bottom: 600, x: 0, y: 0, toJSON: () => ({}) }))
  WIDTH.px = 1000
  cleanup(); clearAllPanels(); forgetFolds(PANE); forgetScrollMemo(chatScrollKey(PANE, BIND)); forgetScrollMemo(chatScrollKey(PANE, conversationBinding("h", "session-2"))) })
afterEach(() => { vi.useRealTimers(); vi.restoreAllMocks() })

describe('bubbles and work rows', () => {
  it('draws the user as a bubble, the agent as a bubble, and each chain of work as one row with the iOS text', () => {
    mount()
    expect(screen.getAllByTestId('chat-user').length).toBeGreaterThan(0)
    const rows = turnRows(turnsOf(pluginSubmit).find((t) => t.id === 'f3236e8d-7531-41bf-ac48-9950b41aa539')!).runs
    const texts = screen.getAllByTestId('chat-work-text').map((e) => e.textContent)
    for (const r of rows) expect(texts).toContain(r.text)
  })

  it('one row per chain: a turn with two chains has two rows, not one', () => {
    mount()
    const t = turnsOf(pluginSubmit).find((x) => x.id === 'f3236e8d-7531-41bf-ac48-9950b41aa539')!
    const n = turnRows(t).runs.length
    expect(n).toBeGreaterThanOrEqual(2)
    const all = turnsOf(pluginSubmit).reduce((s, x) => s + turnRows(x).runs.length, 0)
    expect(screen.getAllByTestId('chat-work')).toHaveLength(all)
  })

  it('failures read red and denials grey inside the row', () => {
    mount({ turns: turnsOf(denial) })
    const failed = screen.getAllByTestId('chat-work-text').find((e) => /失敗/.test(e.textContent ?? ''))!
    expect(within(failed).getByText(/失敗/)).toHaveClass('text-status-error')
    const denied = screen.getAllByTestId('chat-work-text').find((e) => /已拒絕/.test(e.textContent ?? ''))!
    expect(within(denied).getByText(/已拒絕/)).toHaveClass('text-text-muted')
  })
})

describe('the right panel from a work row', () => {
  it('a click opens the panel on THAT chain (not the turn) and names the turn and the position', () => {
    mount()
    const t = turnsOf(pluginSubmit).find((x) => x.id === 'f3236e8d-7531-41bf-ac48-9950b41aa539')!
    const runs = turnRows(t).runs
    const rowsEls = screen.getAllByTestId('chat-work')
    // the rows of this turn are consecutive in the DOM; find the second one by its text
    const second = rowsEls.find((el) => el.textContent === runs[1].text && !el.isEqualNode(rowsEls.find((x) => x.textContent === runs[0].text) ?? null))!
    fireEvent.click(second)
    expect(readPanel(PANE)?.content).toEqual({ kind: 'chain', turnId: t.id, firstStepId: runs[1].stepIds[0] })
    expect(screen.getByTestId('panel-title')).toHaveTextContent(`work 2 of ${runs.length}`)
    expect(within(screen.getByTestId('panel-chain')).getAllByTestId(/deck-step/)).toHaveLength(runs[1].steps.length)
  })

  it('Esc closes it', () => {
    mount()
    fireEvent.click(screen.getAllByTestId('chat-work')[0])
    expect(screen.getByTestId('session-right-panel')).toBeInTheDocument()
    fireEvent.keyDown(document.body, { key: 'Escape' })
    expect(screen.queryByTestId('session-right-panel')).toBeNull()
  })
})

describe('a different session in the same pane', () => {
  it('/clear or relay (a new session id) closes an open panel; the old one does not come back', () => {
    const { rerender } = mount()
    fireEvent.click(screen.getAllByTestId('chat-work')[0])
    expect(screen.getByTestId('session-right-panel')).toBeInTheDocument()
    rerender(<ChatView {...props({ sessionId: 'session-2' })} />)
    expect(screen.queryByTestId('session-right-panel')).toBeNull()
    rerender(<ChatView {...props({ sessionId: 'session-1' })} />)
    expect(screen.queryByTestId('session-right-panel')).toBeNull()
  })
})

describe('no session id (plan D3)', () => {
  for (const missing of [null, ''] as const) {
    it(`sessionId ${JSON.stringify(missing)} with old turns still in hand is 「no_session」: no transcript, no panel, no memory`, () => {
      const { rerender } = mount()
      fireEvent.click(screen.getAllByTestId('chat-work')[0]) // a panel under session-1
      // provenance clears the id during /clear, relay or rebuild while the old turns are still held
      rerender(<ChatView {...props({ sessionId: missing })} />)
      expect(screen.getByTestId('unreadable')).toHaveAttribute('data-reason', 'no_session')
      expect(screen.queryByTestId('chat-scroll')).toBeNull()
      expect(screen.queryByTestId('chat-work')).toBeNull()
      expect(screen.queryByTestId('session-right-panel')).toBeNull()
      expect(readScrollMemo(chatScrollKey(PANE, conversationBinding('h', missing)))).toBeUndefined()
      expect(readPanel(PANE)?.binding).not.toBe(conversationBinding('h', missing)) // nothing was opened under the empty binding
    })
  }

  it('an explicit unreadable reason does not outrank a missing session', () => {
    mount({ sessionId: null, unreadable: 'offline' })
    expect(screen.getByTestId('unreadable')).toHaveAttribute('data-reason', 'no_session')
  })

  it('the terminal button is still offered', () => {
    const onSwitchToTerminal = vi.fn()
    mount({ sessionId: null, onSwitchToTerminal })
    fireEvent.click(screen.getByTestId('unreadable-terminal'))
    expect(onSwitchToTerminal).toHaveBeenCalledTimes(1)
  })
})

describe('scroll memory belongs to the conversation', () => {
  const scrollTo = vi.fn()
  beforeEach(() => {
    scrollTo.mockClear()
    Element.prototype.scrollTo = scrollTo as unknown as Element['scrollTo']
    Object.defineProperty(HTMLElement.prototype, 'scrollHeight', { configurable: true, get: () => 1000 })
    Object.defineProperty(HTMLElement.prototype, 'clientHeight', { configurable: true, get: () => 200 })
  })
  afterEach(() => {
    delete (Element.prototype as { scrollTo?: unknown }).scrollTo
    delete (HTMLElement.prototype as { scrollHeight?: unknown }).scrollHeight
    delete (HTMLElement.prototype as { clientHeight?: unknown }).clientHeight
  })
  const lastTop = () => (scrollTo.mock.calls[scrollTo.mock.calls.length - 1][0] as { top: number }).top
  const readerAt300 = () => {
    const first = mount()
    const sc = screen.getByTestId('chat-scroll')
    Object.defineProperty(sc, 'scrollTop', { configurable: true, writable: true, value: 300 })
    fireEvent.scroll(sc)
    first.unmount()
    scrollTo.mockClear()
  }

  it('the same session comes back where it was left (the control)', () => {
    readerAt300()
    mount()
    expect(lastTop()).not.toBe(1000)
  })

  it('a new session id in the same pane (/clear, relay, rebuild) starts at the bottom, not at the old position', () => {
    readerAt300()
    mount({ sessionId: 'session-2' })
    expect(lastTop()).toBe(1000)
  })

  it('a session replaced while mounted starts afresh too', () => {
    const { rerender } = mount()
    const sc = screen.getByTestId('chat-scroll')
    Object.defineProperty(sc, 'scrollTop', { configurable: true, writable: true, value: 300 })
    fireEvent.scroll(sc)
    scrollTo.mockClear()
    rerender(<ChatView {...props({ sessionId: 'session-2' })} />)
    expect(lastTop()).toBe(1000)
  })
})

describe('the running chain', () => {
  const running = (latestSummary = 'npm test'): PanelTurn[] => [turn('t0', 0, [
    { type: 'user', id: 'u', at: 1, index: 0, text: 'go', source: 'user' } as UserItem,
    step('s1', { started_at: Date.now() - 5000 }),
    step('s2', { status: 'running', summary: latestSummary, started_at: Date.now() - 3000, duration_ms: undefined }),
  ])]

  it('shows 「正在：<latest>」 with a clock, in the row and in the header (hand-made turn)', () => {
    mount({ turns: running(), status: 'running' })
    expect(screen.getByTestId('chat-work-text')).toHaveTextContent('Now: 指令 npm test')
    expect(screen.getByTestId('chat-header-status')).toHaveTextContent('Now: 指令 npm test')
    expect(screen.getByTestId('chat-work-clock')).toHaveTextContent(/^\d+ 秒$/)
  })

  it('the clock ticks', () => {
    vi.useFakeTimers()
    vi.setSystemTime(100_000)
    const turns: PanelTurn[] = [turn('t0', 0, [step('s', { status: 'running', started_at: 100_000, duration_ms: undefined })])]
    mount({ turns })
    expect(screen.getByTestId('chat-work-clock')).toHaveTextContent('0 秒')
    act(() => { vi.advanceTimersByTime(3000) })
    expect(screen.getByTestId('chat-work-clock')).toHaveTextContent('3 秒')
  })

  it('updates in place: the same element carries the new step, and then the finished text', () => {
    const { rerender } = mount({ turns: running('first') })
    const el = screen.getByTestId('chat-work')
    expect(el).toHaveTextContent('first')
    rerender(<ChatView {...props({ turns: running('second') })} />)
    expect(screen.getByTestId('chat-work')).toBe(el)
    expect(el).toHaveTextContent('second')
    const done: PanelTurn[] = [turn('t0', 0, [
      { type: 'user', id: 'u', at: 1, index: 0, text: 'go', source: 'user' } as UserItem,
      step('s1'), step('s2', { status: 'done', started_at: 1500 }),
    ])]
    rerender(<ChatView {...props({ turns: done })} />)
    expect(screen.getByTestId('chat-work')).toBe(el)
    expect(el.getAttribute('data-running')).toBe('false')
    expect(el).toHaveTextContent('處理了')
  })

  it('without a running chain the header says the status word', () => {
    mount({ status: 'waiting' })
    expect(screen.getByTestId('chat-header-status')).toHaveTextContent('Waiting for you')
    expect(screen.getByTestId('chat-header-title')).toHaveTextContent('my tab')
  })
})

describe('file chip', () => {
  it('a turn that edited a file ends with 「N 個檔案 +a −r」 (golden turn 2: app.py twice = 1 file +5 −5)', () => {
    mount({ turns: turnsOf(editWrite) })
    const chips = screen.getAllByTestId('chat-files').map((c) => c.textContent)
    expect(chips).toContain('1 file(s) +5 −5')
  })

  it('counts distinct files, adds up lines, skips failed and denied edits (hand-made)', () => {
    const e = (id: string, path: string, a: number, r: number, status = 'done') => step(id, { kind: 'edit', status, diff: { path, added: a, removed: r, exact: true } })
    expect(fileSummary([e('1', 'a', 1, 2), e('2', 'b', 3, 0), e('3', 'a', 4, 1), e('4', 'c', 9, 9, 'failed'), e('5', 'd', 9, 9, 'denied')])).toEqual({ files: 2, added: 8, removed: 3 })
    expect(fileSummary([step('x')])).toBeNull()
  })

  it('a click opens the chain that holds the edit', () => {
    mount({ turns: turnsOf(editWrite) })
    fireEvent.click(screen.getAllByTestId('chat-files')[0])
    expect(readPanel(PANE)?.content.kind).toBe('chain')
  })

  it('a turn without edits has no chip', () => {
    mount({ turns: turnsOf(pluginSubmit) })
    expect(screen.queryByTestId('chat-files')).toBeNull()
  })
})

describe('the user on the right, the agent on the left (spec §5)', () => {
  const userTurn = (source: string, text = 'hello there'): PanelTurn[] => [turn('t0', 0, [
    { type: 'user', id: 'u', at: new Date(2026, 9, 10, 1, 56).getTime(), index: 0, text, source } as UserItem,
    { type: 'agent_text', id: 'a', at: 2, index: 1, markdown: 'hi' } as ConversationItem,
  ])]

  it('an ordinary message is a right-aligned accent bubble, not the deck\'s framed block', () => {
    mount({ turns: userTurn('user') })
    const wrap = screen.getByTestId('chat-user')
    expect(wrap).toHaveClass('items-end')
    const bubble = within(wrap).getByTestId('chat-user-bubble')
    expect(bubble).toHaveClass('bg-accent')
    expect(bubble).toHaveTextContent('hello there')
    expect(screen.queryByTestId('deck-user')).toBeNull()
    expect(screen.queryByTestId('deck-user-caption')).toBeNull()
  })

  it('the time sits below the bubble in small text', () => {
    mount({ turns: userTurn('user') })
    const wrap = screen.getByTestId('chat-user')
    const time = within(wrap).getByTestId('chat-user-time')
    expect(time).toHaveTextContent('01:56')
    expect(time).toHaveClass('text-xs')
    expect(wrap.children[0]).toBe(within(wrap).getByTestId('chat-user-bubble'))
    expect(wrap.children[1]).toBe(time)
  })

  it('a queued message says so under the bubble instead of the time', () => {
    mount({ turns: userTurn('queued') })
    expect(screen.getByTestId('chat-user-time')).toHaveTextContent('You · queued')
  })

  it('a source this build does not know still reads as the person (a bubble)', () => {
    mount({ turns: userTurn('prompt') })
    expect(screen.getByTestId('chat-user')).toBeInTheDocument()
  })

  it('the agent stays on the left', () => {
    mount({ turns: userTurn('user') })
    expect(screen.getByTestId('chat-agent')).toHaveClass('justify-start')
  })

  it('bash / schedule / background stay the deck\'s block', () => {
    for (const source of ['bash', 'scheduled', 'task', 'background']) {
      mount({ turns: userTurn(source) })
      expect(screen.queryByTestId('chat-user')).toBeNull()
      expect(screen.getByTestId('deck-user')).toBeInTheDocument()
      cleanup()
    }
  })
})

describe('peer messages (iOS 0.6.44)', () => {
  const me = (id = 'u'): UserItem => ({ type: 'user', id, at: 1, index: 0, text: 'me', source: 'user' })
  const reply = (id = 'a'): ConversationItem => ({ type: 'agent_text', id, at: 1, index: 0, markdown: 'ok' }) as ConversationItem
  const unv = (name: string) => ({ kind: 'peer', name, unverified: true })

  it('a single message collapses to 「↪ sender：first 40 chars…」 (golden)', () => {
    mount({ turns: turnsOf(peerMessage) })
    const line = screen.getByTestId('chat-peer')
    expect(line).toHaveTextContent('↪ host/fixture-peer: fixture ping: reply with the single word…')
    expect(screen.getByTestId('chat-peer-head')).toHaveClass('truncate')
  })

  it('only the first line, only its first 40 characters', () => {
    mount({ turns: [turn('t', 0, [peer('p', { text: `${'x'.repeat(60)}\nsecond line` })])] })
    expect(screen.getByTestId('chat-peer-head').textContent).toBe(`↪ host/a: ${'x'.repeat(40)}…`)
  })

  describe('the ellipsis only says something was cut', () => {
    const head = (text: string) => {
      mount({ turns: [turn('t', 0, [peer('p', { text })])] })
      const h = screen.getByTestId('chat-peer-head').textContent
      cleanup()
      return h
    }
    it('a short single line has none', () => { expect(head('short ping')).toBe('↪ host/a: short ping') })
    it('a single line of exactly 40 characters has none', () => { expect(head('y'.repeat(40))).toBe(`↪ host/a: ${'y'.repeat(40)}`) })
    it('a single line of 41 characters is cut and has it', () => { expect(head('y'.repeat(41))).toBe(`↪ host/a: ${'y'.repeat(40)}…`) })
    it('a short first line with more lines after it has it', () => { expect(head('first\nsecond')).toBe('↪ host/a: first…') })
  })

  it('a single unverified message says （未驗證） on the collapsed line too; a verified one does not', () => {
    mount({ turns: [turn('t', 0, [peer('p', { text: 'hi', from: unv('host/b') })])] })
    expect(screen.getByTestId('chat-peer-head').textContent).toBe('↪ host/b (unverified): hi')
    cleanup()
    mount({ turns: [turn('t', 0, [peer('p', { text: 'hi' })])] })
    expect(screen.getByTestId('chat-peer-head').textContent).toBe('↪ host/a: hi')
  })

  it('consecutive peer messages of one turn are one line, "↪ N peer messages", with no sender list', () => {
    mount({ turns: [turn('t', 0, [peer('p1'), peer('p2', { from: { kind: 'peer', name: 'host/b' } }), peer('p3')])] })
    const lines = screen.getAllByTestId('chat-peer')
    expect(lines).toHaveLength(1)
    expect(lines[0]).toHaveAttribute('data-count', '3')
    expect(screen.getByTestId('chat-peer-head').textContent).toBe('↪ 3 peer messages')
    expect(screen.queryByText(/host\/b/)).toBeNull()
  })

  it('anything between them splits them: an agent reply, a work row, a user message', () => {
    const work = step('s1')
    for (const between of [reply(), work as ConversationItem, me() as ConversationItem]) {
      mount({ turns: [turn('t', 0, [peer('p1'), between, peer('p2')])] })
      expect(screen.getAllByTestId('chat-peer')).toHaveLength(2)
      cleanup()
    }
  })

  it('never merges across turns', () => {
    const turns = [turn('t0', 0, [peer('p1')]), turn('t1', 1, [peer('p2')])]
    mount({ turns })
    expect(screen.getAllByTestId('chat-peer')).toHaveLength(2)
    expect(buildChat(turns).filter((e) => e.kind === 'peer')).toHaveLength(2)
  })

  it('a click expands one line per message with its sender; unverified senders say （未驗證）', () => {
    mount({ turns: [turn('t', 0, [peer('p1', { text: 'first' }), peer('p2', { text: 'second', from: unv('host/b') })])] })
    expect(screen.queryAllByTestId('chat-peer-item')).toHaveLength(0)
    fireEvent.click(screen.getByTestId('chat-peer-head'))
    const rows = screen.getAllByTestId('chat-peer-item')
    expect(rows.map((r) => r.textContent)).toEqual(['host/a: first', 'host/b (unverified): second'])
    fireEvent.click(screen.getByTestId('chat-peer-head'))
    expect(screen.queryAllByTestId('chat-peer-item')).toHaveLength(0)
  })

  it('the expansion is kept when the chat remounts (tab-hosted)', () => {
    const turns = [turn('t', 0, [peer('p1', { text: 'first' })])]
    const first = mount({ turns })
    fireEvent.click(screen.getByTestId('chat-peer-head'))
    first.unmount()
    mount({ turns })
    expect(screen.getAllByTestId('chat-peer-item')).toHaveLength(1)
  })

  it('the line is dimmed only when EVERY message is unverified', () => {
    mount({ turns: [turn('t', 0, [peer('p1', { from: unv('host/a') }), peer('p2', { from: unv('host/b') })])] })
    expect(screen.getByTestId('chat-peer')).toHaveClass('opacity-60')
    cleanup()
    mount({ turns: [turn('t', 0, [peer('p1', { from: unv('host/a') }), peer('p2')])] })
    expect(screen.getByTestId('chat-peer')).not.toHaveClass('opacity-60')
    cleanup()
    mount({ turns: [turn('t', 0, [peer('p1')])] })
    expect(screen.getByTestId('chat-peer')).not.toHaveClass('opacity-60')
  })

  it('a plugin sender reads 「<name> plugin」 (golden plugin-submit)', () => {
    mount({ turns: turnsOf(pluginSubmit) })
    const first = screen.getAllByTestId('chat-peer')[0]
    expect(first.textContent).toContain('↪ prompt-probe plugin:')
    expect(first.textContent).not.toContain('plugin:  ')
  })
})

describe('unreadable (D11)', () => {
  const cases: Array<[NonNullable<ChatViewProps['unreadable']>, string]> = [
    ['no_session', '找不到這個分頁的 Claude Code 對話'], ['not_found', '對話紀錄還沒出現'], ['unsupported', '這個 agent 不支援'],
    ['empty', '還沒有內容'], ['offline', '連不上主機'],
  ]
  beforeEach(async () => {
    const { useI18nStore } = await import('../../stores/useI18nStore')
    useI18nStore.getState().setLocale('zh-TW')
  })
  afterEach(async () => {
    const { useI18nStore } = await import('../../stores/useI18nStore')
    useI18nStore.getState().setLocale('en')
  })

  for (const [reason, text] of cases) {
    it(`${reason}: says 「${text}」 and always offers the terminal, which only a click uses`, () => {
      const onSwitchToTerminal = vi.fn()
      mount({ unreadable: reason, turns: [], onSwitchToTerminal })
      expect(screen.getByTestId('unreadable-title')).toHaveTextContent('無法正確讀取這個對話') // spec "Unreadable": the one main message, then the reason
      expect(screen.getByTestId('unreadable-reason')).toHaveTextContent(text)
      expect(onSwitchToTerminal).not.toHaveBeenCalled() // never automatic
      fireEvent.click(screen.getByTestId('unreadable-terminal'))
      expect(onSwitchToTerminal).toHaveBeenCalledTimes(1)
    })
  }

  it('only the offline state has 「重試」, and it calls back', () => {
    const onRetry = vi.fn()
    mount({ unreadable: 'offline', turns: [], onRetry })
    fireEvent.click(screen.getByTestId('unreadable-retry'))
    expect(onRetry).toHaveBeenCalledTimes(1)
    cleanup()
    mount({ unreadable: 'not_found', turns: [], onRetry })
    expect(screen.queryByTestId('unreadable-retry')).toBeNull()
  })

  it('a conversation whose turns hold no items is 「還沒有內容」 without being told', () => {
    mount({ turns: [turn('t0', 0, [])] })
    expect(screen.getByTestId('unreadable')).toHaveAttribute('data-reason', 'empty')
  })

  it('an unreadable conversation draws no panel and no transcript', () => {
    mount({ unreadable: 'unsupported', turns: turnsOf(pluginSubmit) })
    expect(screen.queryByTestId('chat-scroll')).toBeNull()
  })
})

// The real TabContent: the alive pool keeps nothing (keepAliveCount 0), so the chat unmounts when the tab is left.
const H = 'h'
function ChatPane({ pane }: PaneRendererProps) {
  return <ChatView {...props({ paneKey: pane.id, turns: turnsOf(pluginSubmit) })} />
}
const Other = () => <div data-testid="other-tab" />
const chatTab: Tab = { ...createTab({ kind: 'execution', executionId: 'exc_1', host: H }), id: 't-chat' }
const dashTab: Tab = { ...createTab({ kind: 'dashboard' }), id: 't-dash' }
const paneIdOf = (chatTab.layout as { pane: { id: string } }).pane.id

describe('chat across tab switches', () => {
  beforeEach(() => {
    clearModuleRegistry()
    registerModule({ id: 'nex', name: 'Nex', panes: [{ kind: 'execution', component: ChatPane }] })
    registerModule({ id: 'dashboard', name: 'Dashboard', panes: [{ kind: 'dashboard', component: Other }] })
    useUISettingsStore.setState({ keepAliveCount: 0 })
    useShownHostsStore.setState({ ids: [H] })
    useHostConfigStore.setState({ byHost: {}, ensureLoaded: async () => {} })
    forgetScrollMemo(chatScrollKey(paneIdOf, BIND))
    clearAllPanels()
  })

  it('the open panel (on its chain) and the scroll position are back when the reader returns', () => {
    const all = [chatTab, dashTab]
    const { rerender } = render(<TabContent activeTab={chatTab} allTabs={all} />)
    fireEvent.click(screen.getAllByTestId('chat-work')[1])
    const title = screen.getByTestId('panel-title').textContent
    const scroller = screen.getByTestId('chat-scroll')
    Object.defineProperty(scroller, 'scrollHeight', { configurable: true, value: 1000 })
    Object.defineProperty(scroller, 'clientHeight', { configurable: true, value: 200 })
    Object.defineProperty(scroller, 'scrollTop', { configurable: true, writable: true, value: 300 })
    fireEvent.scroll(scroller)
    expect(readScrollMemo(chatScrollKey(paneIdOf, BIND))).toMatchObject({ scrollTop: 300, atBottom: false })

    rerender(<TabContent activeTab={dashTab} allTabs={all} />)
    expect(screen.queryByTestId('chat-view')).toBeNull() // really unmounted
    expect(screen.getByTestId('other-tab')).toBeInTheDocument()

    rerender(<TabContent activeTab={chatTab} allTabs={all} />)
    expect(screen.getByTestId('panel-title').textContent).toBe(title)
    expect(readScrollMemo(chatScrollKey(paneIdOf, BIND))).toMatchObject({ scrollTop: 300, atBottom: false })
  })
})
