// The dock (U3 spec §7, plan D8): one card per open hook_ask, bound to its approval.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { act, cleanup, fireEvent, render, screen } from '@testing-library/react'
import { forgetDockDraft } from '../../lib/conversations/dock-memory'
import type { ConversationApproval, ConversationItem } from '../../lib/conversations/types'
import { useNexHostStore } from '../../stores/useNexHostStore'
import type { DeckFooterContext } from '../deck/footer-context'
import { QuestionDock } from './QuestionDock'

const asks = vi.hoisted(() => ({ answerAsk: vi.fn(), replyToAsk: vi.fn() }))
vi.mock('../../lib/conversations/asks', async (orig) => ({ ...(await orig<typeof import('../../lib/conversations/asks')>()), answerAsk: asks.answerAsk, replyToAsk: asks.replyToAsk }))

const approval = (id: string, over: Record<string, unknown> = {}, payload: Record<string, unknown> = {}): ConversationApproval => ({
  id, kind: 'hook_ask', state: 'open',
  payload: {
    tool_use_id: `toolu_${id}`,
    questions: [
      { question: '先做哪個方案？', header: '方案', multiSelect: false, options: [{ label: '甲案', description: '先做甲' }, { label: '乙案' }] },
      { question: '要哪些顏色？', multiSelect: true, options: [{ label: '紅' }, { label: '綠' }] },
    ],
    ...payload,
  },
  ...over,
})
const ctxOf = (approvals: ConversationApproval[], over: Partial<DeckFooterContext> = {}): DeckFooterContext => ({
  paneKey: 'p1', hostId: 'h', sessionId: 's', capabilities: undefined, items: [], idle: false, status: 'waiting', usage: undefined,
  onSwitchToTerminal: vi.fn(), approvals, asking: approvals.length > 0, ...over,
})
const answered = (id: string, answers: string[][]): ConversationItem =>
  ({ type: 'step', id: `toolu_${id}`, at: 1, index: 0, kind: 'other', tool: 'AskUserQuestion', status: 'done', summary: 'q', started_at: 1, input: null, question: { questions: [], answers } }) as unknown as ConversationItem

const pickAll = () => {
  fireEvent.click(screen.getAllByTestId('dock-option')[0]) // 甲案
  const colours = screen.getAllByTestId('dock-option').slice(2) // 紅 綠
  fireEvent.click(colours[0])
  fireEvent.click(colours[1])
}

beforeEach(() => {
  cleanup()
  asks.answerAsk.mockReset()
  asks.replyToAsk.mockReset()
  for (const id of ['a1', 'a2']) forgetDockDraft(`p1\0${id}`)
  useNexHostStore.setState({ byHost: { h: { daemonCapabilities: ['team.ask_chat.v1'] } } } as never)
})
afterEach(() => { vi.useRealTimers() })

describe('the card', () => {
  it('shows nothing without an open question, and ignores other kinds', () => {
    const { container } = render(<QuestionDock ctx={ctxOf([approval('l', { kind: 'lead' })])} />)
    expect(container).toBeEmptyDOMElement()
  })

  it('draws the question, its options, a 其他 field, and a 終端機 button', () => {
    render(<QuestionDock ctx={ctxOf([approval('a1')])} />)
    expect(screen.getAllByTestId('dock-question')).toHaveLength(2)
    expect(screen.getByTestId('dock-card')).toHaveTextContent('先做哪個方案？')
    expect(screen.getAllByTestId('dock-option')).toHaveLength(4)
    expect(screen.getAllByTestId('dock-other')).toHaveLength(2)
    expect(screen.getByTestId('dock-terminal')).toBeInTheDocument()
  })

  it('send stays off until every question has an answer, then sends approve + answers', async () => {
    asks.answerAsk.mockResolvedValue({ ok: true })
    render(<QuestionDock ctx={ctxOf([approval('a1')])} />)
    expect(screen.getByTestId('dock-submit')).toBeDisabled()
    fireEvent.click(screen.getAllByTestId('dock-option')[0])
    expect(screen.getByTestId('dock-submit')).toBeDisabled() // the second question is still open
    const colours = screen.getAllByTestId('dock-option').slice(2)
    fireEvent.click(colours[0])
    fireEvent.click(colours[1])
    expect(screen.getByTestId('dock-submit')).toBeEnabled()
    await act(async () => { fireEvent.click(screen.getByTestId('dock-submit')) })
    expect(asks.answerAsk).toHaveBeenCalledWith('h', 'a1', { '先做哪個方案？': '甲案', '要哪些顏色？': '紅, 綠' })
  })

  it('a free-text 其他 answers a question, and picking an option drops it', () => {
    render(<QuestionDock ctx={ctxOf([approval('a1')])} />)
    const others = screen.getAllByTestId('dock-other')
    fireEvent.change(others[0], { target: { value: '兩個都不要' } })
    fireEvent.click(screen.getAllByTestId('dock-option')[1])
    expect((screen.getAllByTestId('dock-other')[0] as HTMLInputElement).value).toBe('')
    expect(screen.getAllByTestId('dock-option')[1]).toHaveAttribute('data-chosen', 'true')
  })

  it('the card has a way to the terminal that does not answer', () => {
    const ctx = ctxOf([approval('a1')])
    render(<QuestionDock ctx={ctx} />)
    fireEvent.click(screen.getByTestId('dock-terminal'))
    expect(ctx.onSwitchToTerminal).toHaveBeenCalledTimes(1)
    expect(asks.answerAsk).not.toHaveBeenCalled()
  })

  it('a network failure says so, sends nothing twice by itself, and lets the person try again', async () => {
    asks.answerAsk.mockResolvedValueOnce({ ok: false, reason: 'network' }).mockResolvedValueOnce({ ok: true })
    render(<QuestionDock ctx={ctxOf([approval('a1')])} />)
    pickAll()
    await act(async () => { fireEvent.click(screen.getByTestId('dock-submit')) })
    expect(screen.getByTestId('dock-failure')).toHaveTextContent('Connection lost')
    expect(asks.answerAsk).toHaveBeenCalledTimes(1)
    await act(async () => { fireEvent.click(screen.getByTestId('dock-submit')) })
    expect(asks.answerAsk).toHaveBeenCalledTimes(2)
  })

  it('a second press while sending does nothing', async () => {
    let finish: (v: unknown) => void = () => {}
    asks.answerAsk.mockReturnValue(new Promise((r) => { finish = r }))
    render(<QuestionDock ctx={ctxOf([approval('a1')])} />)
    pickAll()
    fireEvent.click(screen.getByTestId('dock-submit'))
    fireEvent.click(screen.getByTestId('dock-submit'))
    expect(asks.answerAsk).toHaveBeenCalledTimes(1)
    await act(async () => { finish({ ok: true }) })
  })
})

describe('bound to its question', () => {
  it('when the approval closes under it the card locks at once (題目已變更), with no options left to tap, then goes', () => {
    vi.useFakeTimers()
    const { rerender } = render(<QuestionDock ctx={ctxOf([approval('a1')])} />)
    rerender(<QuestionDock ctx={ctxOf([])} />)
    expect(screen.getByTestId('dock-locked')).toHaveTextContent('The question has changed')
    expect(screen.queryByTestId('dock-option')).toBeNull()
    expect(screen.queryByTestId('dock-submit')).toBeNull()
    act(() => { vi.advanceTimersByTime(1499) })
    expect(screen.getByTestId('dock-locked')).toBeInTheDocument()
    act(() => { vi.advanceTimersByTime(2) })
    expect(screen.queryByTestId('dock-card')).toBeNull()
  })

  it('when the terminal answered first the card says what it answered, for 3 s', () => {
    vi.useFakeTimers()
    const items = [answered('a1', [['甲案'], ['紅', '綠']])]
    const { rerender } = render(<QuestionDock ctx={ctxOf([approval('a1')], { items })} />)
    rerender(<QuestionDock ctx={ctxOf([], { items })} />)
    expect(screen.getByTestId('dock-answered-terminal')).toHaveTextContent('Answered in the terminal: 甲案 / 紅, 綠')
    act(() => { vi.advanceTimersByTime(2999) })
    expect(screen.getByTestId('dock-answered-terminal')).toBeInTheDocument()
    act(() => { vi.advanceTimersByTime(2) })
    expect(screen.queryByTestId('dock-card')).toBeNull()
  })

  it('a card this dock answered itself goes without the lock', async () => {
    asks.answerAsk.mockResolvedValue({ ok: true })
    const { rerender } = render(<QuestionDock ctx={ctxOf([approval('a1')])} />)
    pickAll()
    await act(async () => { fireEvent.click(screen.getByTestId('dock-submit')) })
    rerender(<QuestionDock ctx={ctxOf([])} />)
    expect(screen.queryByTestId('dock-locked')).toBeNull()
    expect(screen.queryByTestId('dock-card')).toBeNull()
  })

  it('a send that fails because the question closed locks the card and offers no retry', async () => {
    asks.answerAsk.mockResolvedValue({ ok: false, reason: 'changed' })
    render(<QuestionDock ctx={ctxOf([approval('a1')])} />)
    pickAll()
    await act(async () => { fireEvent.click(screen.getByTestId('dock-submit')) })
    expect(asks.answerAsk).toHaveBeenCalledTimes(1)
    expect(screen.queryByTestId('dock-failure')).toBeNull() // the close lands through the stream, not as an error to retry
  })

  it('a different approval is a different card: nothing carries over', () => {
    const { rerender } = render(<QuestionDock ctx={ctxOf([approval('a1')])} />)
    fireEvent.click(screen.getAllByTestId('dock-option')[0])
    rerender(<QuestionDock ctx={ctxOf([approval('a2')])} />)
    // a1 closed (locked card first), a2 open behind it; once the lock is gone a2 has no picks
    expect(screen.getByTestId('dock-more')).toBeInTheDocument()
  })

  it('shows the next question after the first, and says how many wait', () => {
    render(<QuestionDock ctx={ctxOf([approval('a1'), approval('a2')])} />)
    expect(screen.getByTestId('dock-more')).toHaveTextContent('1 more')
  })
})

describe('the note 「已在終端機回答」 gets its own 3 s from when it first shows', () => {
  const said = () => [answered('a1', [['甲案'], ['紅', '綠']])]

  it('the approval closes first, the transcript answer lands 0.6 s later: the lock, then the note for a full 3 s', () => {
    vi.useFakeTimers()
    const { rerender } = render(<QuestionDock ctx={ctxOf([approval('a1')])} />)
    rerender(<QuestionDock ctx={ctxOf([])} />) // closed, no answer in the transcript yet
    expect(screen.getByTestId('dock-locked')).toBeInTheDocument()
    act(() => { vi.advanceTimersByTime(600) })
    rerender(<QuestionDock ctx={ctxOf([], { items: said() })} />) // the answer arrives
    expect(screen.getByTestId('dock-answered-terminal')).toHaveTextContent('甲案 / 紅, 綠')
    act(() => { vi.advanceTimersByTime(2999) })
    expect(screen.getByTestId('dock-answered-terminal')).toBeInTheDocument()
    act(() => { vi.advanceTimersByTime(2) })
    expect(screen.queryByTestId('dock-card')).toBeNull()
  })

  it('the answer already there when the approval closes: the note for 3 s', () => {
    vi.useFakeTimers()
    const { rerender } = render(<QuestionDock ctx={ctxOf([approval('a1')], { items: said() })} />)
    rerender(<QuestionDock ctx={ctxOf([], { items: said() })} />)
    act(() => { vi.advanceTimersByTime(2999) })
    expect(screen.getByTestId('dock-answered-terminal')).toBeInTheDocument()
    act(() => { vi.advanceTimersByTime(2) })
    expect(screen.queryByTestId('dock-card')).toBeNull()
  })

  it('the answer lands after the lock is gone (1.5 s): the card is gone and does not come back', () => {
    vi.useFakeTimers()
    const { rerender } = render(<QuestionDock ctx={ctxOf([approval('a1')])} />)
    rerender(<QuestionDock ctx={ctxOf([])} />)
    act(() => { vi.advanceTimersByTime(1600) })
    expect(screen.queryByTestId('dock-card')).toBeNull()
    rerender(<QuestionDock ctx={ctxOf([], { items: said() })} />)
    expect(screen.queryByTestId('dock-card')).toBeNull()
    act(() => { vi.advanceTimersByTime(5000) })
    expect(screen.queryByTestId('dock-card')).toBeNull()
  })

  it('the 3 s is counted once: later items updates do not extend it', () => {
    vi.useFakeTimers()
    const { rerender } = render(<QuestionDock ctx={ctxOf([approval('a1')])} />)
    rerender(<QuestionDock ctx={ctxOf([])} />)
    rerender(<QuestionDock ctx={ctxOf([], { items: said() })} />)
    act(() => { vi.advanceTimersByTime(2000) })
    rerender(<QuestionDock ctx={ctxOf([], { items: said() })} />) // a new items array, the same answer
    act(() => { vi.advanceTimersByTime(1001) })
    expect(screen.queryByTestId('dock-card')).toBeNull()
  })

  it('a send that lost to the terminal (hidden entry) shows the note for 3 s from when it is shown', async () => {
    vi.useFakeTimers()
    let finish: (v: unknown) => void = () => {}
    asks.answerAsk.mockReturnValue(new Promise((r) => { finish = r }))
    const { rerender } = render(<QuestionDock ctx={ctxOf([approval('a1')])} />)
    pickAll()
    fireEvent.click(screen.getByTestId('dock-submit'))
    rerender(<QuestionDock ctx={ctxOf([])} />) // closed under our send
    act(() => { vi.advanceTimersByTime(4000) }) // the send is slow; nothing shows meanwhile
    expect(screen.queryByTestId('dock-card')).toBeNull()
    rerender(<QuestionDock ctx={ctxOf([], { items: said() })} />)
    await act(async () => { finish({ ok: false, reason: 'changed' }) })
    expect(screen.getByTestId('dock-answered-terminal')).toBeInTheDocument()
    act(() => { vi.advanceTimersByTime(2999) })
    expect(screen.getByTestId('dock-answered-terminal')).toBeInTheDocument()
    act(() => { vi.advanceTimersByTime(2) })
    expect(screen.queryByTestId('dock-card')).toBeNull()
  })
})

describe('races and re-announcements', () => {
  it('our send loses to another client: the approval closes, the send says changed — the lock still shows', async () => {
    let finish: (v: unknown) => void = () => {}
    asks.answerAsk.mockReturnValue(new Promise((r) => { finish = r }))
    const { rerender } = render(<QuestionDock ctx={ctxOf([approval('a1')])} />)
    pickAll()
    fireEvent.click(screen.getByTestId('dock-submit')) // in flight
    rerender(<QuestionDock ctx={ctxOf([])} />) // the terminal answered first: the approval closes
    expect(screen.queryByTestId('dock-locked')).toBeNull() // not yet: our own answer may be the cause
    await act(async () => { finish({ ok: false, reason: 'changed' }) })
    expect(screen.getByTestId('dock-locked')).toHaveTextContent('The question has changed')
  })

  it('our send wins and the approval had already closed: no lock appears', async () => {
    let finish: (v: unknown) => void = () => {}
    asks.answerAsk.mockReturnValue(new Promise((r) => { finish = r }))
    const { rerender } = render(<QuestionDock ctx={ctxOf([approval('a1')])} />)
    pickAll()
    fireEvent.click(screen.getByTestId('dock-submit'))
    rerender(<QuestionDock ctx={ctxOf([])} />)
    await act(async () => { finish({ ok: true }) })
    expect(screen.queryByTestId('dock-locked')).toBeNull()
    expect(screen.queryByTestId('dock-card')).toBeNull()
  })

  it('the same id announced again with other questions is a fresh card, not a crash', () => {
    const one = approval('a1', {}, { questions: [{ question: '只有一題？', options: [{ label: '是' }] }] })
    const { rerender } = render(<QuestionDock ctx={ctxOf([one])} />)
    fireEvent.click(screen.getAllByTestId('dock-option')[0])
    rerender(<QuestionDock ctx={ctxOf([approval('a1')])} />) // two questions now
    expect(screen.getAllByTestId('dock-question')).toHaveLength(2)
    expect(screen.getAllByTestId('dock-option').every((el) => el.getAttribute('data-chosen') === 'false')).toBe(true)
  })

  it('another conversation (/clear): the old one\'s approvals leaving make no lock', () => {
    const { rerender } = render(<QuestionDock ctx={ctxOf([approval('a1')])} />)
    rerender(<QuestionDock ctx={ctxOf([], { sessionId: 's-new' })} />)
    expect(screen.queryByTestId('dock-locked')).toBeNull()
    expect(screen.queryByTestId('dock-card')).toBeNull()
  })

  it('options with the same label are one option', () => {
    const dup = approval('a1', {}, { questions: [{ question: 'q?', options: [{ label: '甲' }, { label: '甲' }, { label: '乙' }] }] })
    render(<QuestionDock ctx={ctxOf([dup])} />)
    expect(screen.getAllByTestId('dock-option')).toHaveLength(2)
  })
})

describe('single and multiple choice look different', () => {
  it('a single choice is a round radio in a radiogroup; several are square checkboxes in a group', () => {
    render(<QuestionDock ctx={ctxOf([approval('a1')])} />)
    const groups = screen.getAllByTestId('dock-question')
    expect(groups[0]).toHaveAttribute('role', 'radiogroup')
    expect(groups[1]).toHaveAttribute('role', 'group')
    const marks = screen.getAllByTestId('dock-mark')
    expect(marks.slice(0, 2).every((m) => m.getAttribute('data-shape') === 'radio' && m.className.includes('rounded-full'))).toBe(true)
    expect(marks.slice(2).every((m) => m.getAttribute('data-shape') === 'check' && !m.className.includes('rounded-full'))).toBe(true)
    const options = screen.getAllByTestId('dock-option')
    expect(options[0]).toHaveAttribute('role', 'radio')
    expect(options[2]).toHaveAttribute('role', 'checkbox')
  })

  it('choosing one radio clears the other; checkboxes add up', () => {
    render(<QuestionDock ctx={ctxOf([approval('a1')])} />)
    const options = screen.getAllByTestId('dock-option')
    fireEvent.click(options[0])
    fireEvent.click(screen.getAllByTestId('dock-option')[1])
    expect(screen.getAllByTestId('dock-option')[0]).toHaveAttribute('aria-checked', 'false')
    expect(screen.getAllByTestId('dock-option')[1]).toHaveAttribute('aria-checked', 'true')
    fireEvent.click(screen.getAllByTestId('dock-option')[2])
    fireEvent.click(screen.getAllByTestId('dock-option')[3])
    expect(screen.getAllByTestId('dock-option')[2]).toHaveAttribute('aria-checked', 'true')
    expect(screen.getAllByTestId('dock-option')[3]).toHaveAttribute('aria-checked', 'true')
  })
})

describe('the terminal-only card', () => {
  it('is read-only, says why, and offers 開終端機 instead of answering', () => {
    const ctx = ctxOf([approval('a1', {}, { terminal_only: true })])
    render(<QuestionDock ctx={ctx} />)
    expect(screen.queryByTestId('dock-submit')).toBeNull()
    expect(screen.queryByTestId('dock-option')).toBeNull()
    expect(screen.getByTestId('dock-terminal-only')).toHaveTextContent('only be answered in the terminal')
    fireEvent.click(screen.getByTestId('dock-open-terminal'))
    expect(ctx.onSwitchToTerminal).toHaveBeenCalled()
  })

  it('a card that turns out terminal_only at send time becomes read-only', async () => {
    asks.answerAsk.mockResolvedValue({ ok: false, reason: 'terminal_only' })
    render(<QuestionDock ctx={ctxOf([approval('a1')])} />)
    pickAll()
    await act(async () => { fireEvent.click(screen.getByTestId('dock-submit')) })
    expect(screen.getByTestId('dock-open-terminal')).toBeInTheDocument()
    expect(screen.queryByTestId('dock-submit')).toBeNull()
  })
})

describe('改成跟 agent 聊聊', () => {
  it('is offered only when the host lists team.ask_chat.v1', () => {
    useNexHostStore.setState({ byHost: { h: { daemonCapabilities: [] } } } as never)
    render(<QuestionDock ctx={ctxOf([approval('a1')])} />)
    expect(screen.queryByTestId('dock-chat')).toBeNull()
  })

  it('swaps the options for a reply box, validates it, and sends deny + message', async () => {
    asks.replyToAsk.mockResolvedValue({ ok: true })
    render(<QuestionDock ctx={ctxOf([approval('a1')])} />)
    fireEvent.click(screen.getByTestId('dock-chat'))
    expect(screen.queryByTestId('dock-option')).toBeNull()
    fireEvent.click(screen.getByTestId('dock-send-reply'))
    expect(screen.getByTestId('dock-failure')).toHaveTextContent('Nothing to send')
    expect(asks.replyToAsk).not.toHaveBeenCalled()
    fireEvent.change(screen.getByTestId('dock-reply'), { target: { value: '  先別動，我想想  ' } })
    await act(async () => { fireEvent.click(screen.getByTestId('dock-send-reply')) })
    expect(asks.replyToAsk).toHaveBeenCalledWith('h', 'a1', '先別動，我想想')
  })

  it('refuses a reply with characters the daemon would refuse', () => {
    render(<QuestionDock ctx={ctxOf([approval('a1')])} />)
    fireEvent.click(screen.getByTestId('dock-chat'))
    fireEvent.change(screen.getByTestId('dock-reply'), { target: { value: 'a‮b' } })
    fireEvent.click(screen.getByTestId('dock-send-reply'))
    expect(screen.getByTestId('dock-failure')).toHaveTextContent('cannot be sent')
    expect(asks.replyToAsk).not.toHaveBeenCalled()
  })

  it('goes back to the options with what was picked kept', () => {
    render(<QuestionDock ctx={ctxOf([approval('a1')])} />)
    fireEvent.click(screen.getAllByTestId('dock-option')[0])
    fireEvent.click(screen.getByTestId('dock-chat'))
    fireEvent.click(screen.getByTestId('dock-back'))
    expect(screen.getAllByTestId('dock-option')[0]).toHaveAttribute('data-chosen', 'true')
  })
})

describe('across an unmount (the tab switch)', () => {
  it('the picks, the 其他 text and the reply come back with the card', () => {
    const approvals = [approval('a1')]
    const first = render(<QuestionDock ctx={ctxOf(approvals)} />)
    fireEvent.click(screen.getAllByTestId('dock-option')[1])
    fireEvent.change(screen.getAllByTestId('dock-other')[1], { target: { value: '紫' } })
    first.unmount()
    render(<QuestionDock ctx={ctxOf(approvals)} />)
    expect(screen.getAllByTestId('dock-option')[1]).toHaveAttribute('data-chosen', 'true')
    expect((screen.getAllByTestId('dock-other')[1] as HTMLInputElement).value).toBe('紫')
  })

  it('a reply being written comes back too', () => {
    const approvals = [approval('a1')]
    const first = render(<QuestionDock ctx={ctxOf(approvals)} />)
    fireEvent.click(screen.getByTestId('dock-chat'))
    fireEvent.change(screen.getByTestId('dock-reply'), { target: { value: '寫到一半' } })
    first.unmount()
    render(<QuestionDock ctx={ctxOf(approvals)} />)
    expect((screen.getByTestId('dock-reply') as HTMLTextAreaElement).value).toBe('寫到一半')
  })
})
