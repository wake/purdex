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

beforeEach(() => { cleanup(); clearAllPanels(); forgetFolds(PANE); forgetScrollMemo(chatScrollKey(PANE, BIND)); forgetScrollMemo(chatScrollKey(PANE, conversationBinding("h", "session-2"))) })
afterEach(() => { vi.useRealTimers() })

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

describe('peer messages', () => {
  it('a peer message is one line, not a bubble, and says who it is from (golden)', () => {
    mount({ turns: turnsOf(peerMessage) })
    const line = screen.getByTestId('chat-peer')
    expect(line).toHaveTextContent('From host/fixture-peer')
    expect(line).toHaveTextContent('fixture ping: reply with the single word pong')
    expect(screen.queryByTestId('chat-peer-unverified')).toBeNull()
    expect(screen.getByTestId('chat-peer-text')).toHaveClass('truncate')
  })

  it('unverified senders are marked 「未驗證」 (hand-made, the goldens carry no such flag)', () => {
    mount({ turns: [turn('t', 0, [peer('p1', { from: { kind: 'peer', name: 'host/b', unverified: true } })])] })
    expect(screen.getByTestId('chat-peer-unverified')).toHaveTextContent('Unverified')
  })

  it('consecutive peer messages merge into one line, across turns; a user message in between splits them', () => {
    const turns = [
      turn('t0', 0, [peer('p1')]),
      turn('t1', 1, [peer('p2', { from: { kind: 'peer', name: 'host/b', unverified: true } })]),
      turn('t2', 2, [{ type: 'user', id: 'u', at: 1, index: 0, text: 'me', source: 'user' } as UserItem]),
      turn('t3', 3, [peer('p3')]),
    ]
    mount({ turns })
    const lines = screen.getAllByTestId('chat-peer')
    expect(lines).toHaveLength(2)
    expect(lines[0]).toHaveAttribute('data-count', '2')
    expect(lines[0]).toHaveTextContent('host/a、host/b')
    expect(within(lines[0]).getByTestId('chat-peer-unverified')).toBeInTheDocument()
    expect(lines[1]).toHaveAttribute('data-count', '1')
    expect(buildChat(turns).filter((e) => e.kind === 'peer')).toHaveLength(2)
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
      expect(screen.getByTestId('unreadable')).toHaveTextContent(text)
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
