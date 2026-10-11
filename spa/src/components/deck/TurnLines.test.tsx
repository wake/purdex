// The live line and the closing line (c9d9c7-54 pts 3, 5): the data rules, the two components, and where the deck and the chat draw them.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { act, cleanup, render, screen } from '@testing-library/react'
import { doneLine, durationParts, formatDuration, liveLine, type LineTurn } from '../../lib/conversations/turn-line'
import { emptyDoc } from '../../lib/conversations/model'
import { forgetFolds } from '../../lib/conversations/fold-memory'
import { forgetScrollMemosWithPrefix } from '../../lib/nex/transcript-scroll-memory'
import { useI18nStore } from '../../stores/useI18nStore'
import { useConversationStore, type ConversationEntry } from '../../stores/useConversationStore'
import type { ConversationItem, StepItem, Turn } from '../../lib/conversations/types'
import type { PanelTurn } from '../../lib/conversations/panel-resolve'
import { DoneTurnLine, LiveTurnLine } from './TurnLines'
import { DeckView } from './DeckView'
import { ChatView } from './ChatView'

const step = (id: string, over: Partial<StepItem> = {}): StepItem =>
  ({ type: 'step', id, at: 1, index: 0, kind: 'execute', tool: 'Bash', status: 'running', summary: id, started_at: 1000, input: {}, ...over })
const user = (id: string): ConversationItem => ({ type: 'user', id, at: 1, index: 0, text: id, source: 'user' })
const T0 = new Date(2026, 9, 11, 2, 55, 0).getTime()

beforeEach(() => { cleanup(); useI18nStore.getState().setLocale('en') })
afterEach(() => { vi.useRealTimers() })

describe('liveLine', () => {
  const running = (items: ConversationItem[], over: Partial<LineTurn> = {}): LineTurn => ({ outcome: 'running', started_at: T0, items, ...over })
  it('only while the header says running or waiting', () => {
    expect(liveLine([running([])], 'idle')).toBeNull()
    expect(liveLine([running([])], undefined)).toBeNull()
    expect(liveLine([running([])], 'ended')).toBeNull()
    expect(liveLine([running([])], 'running')).toMatchObject({ kind: 'running', since: T0 })
    expect(liveLine([running([])], 'waiting')).toMatchObject({ kind: 'waiting' })
  })
  it('names the tool that runs now and counts the running Agent steps', () => {
    const l = liveLine([running([step('a', { status: 'done', tool: 'Read' }), step('b', { tool: 'Bash' }), step('c', { kind: 'task', tool: 'Agent' }), step('d', { kind: 'task', tool: 'Agent' })])], 'running')
    expect(l).toMatchObject({ tool: 'Bash', subagents: 2 })
  })
  it('a running turn with only subagents names no tool (they are counted); with nothing running names nothing', () => {
    expect(liveLine([running([step('c', { kind: 'task', tool: 'Agent' })])], 'running')).toMatchObject({ tool: null, subagents: 1 })
    expect(liveLine([running([step('a', { status: 'done' })])], 'running')).toMatchObject({ tool: null, subagents: 0 })
  })
  it('a start time of 0 (a zero value) is no clock', () => {
    expect(liveLine([{ outcome: 'running', started_at: 0, items: [] }], 'running')).toMatchObject({ since: null })
  })
  it('counts from the last turn only while it is running; a finished last turn has no clock', () => {
    expect(liveLine([{ outcome: 'done', started_at: T0, ended_at: T0 + 5, items: [] }], 'running')).toMatchObject({ since: null })
    expect(liveLine([], 'running')).toMatchObject({ since: null })
  })
})

describe('doneLine', () => {
  it('only for a done turn with an end time; the duration is the daemon\'s, else the two times', () => {
    expect(doneLine({ outcome: 'done', ended_at: T0 + 20_000, duration_ms: 20_000 })).toEqual({ durationMs: 20_000, endedAt: T0 + 20_000 })
    expect(doneLine({ outcome: 'done', started_at: T0, ended_at: T0 + 7_000 })).toEqual({ durationMs: 7_000, endedAt: T0 + 7_000 })
    expect(doneLine({ outcome: 'interrupted', started_at: T0, ended_at: T0 + 7_000 })).toBeNull()
    expect(doneLine({ outcome: 'failed', ended_at: T0 })).toBeNull()
    expect(doneLine({ outcome: 'done', started_at: T0 })).toBeNull()
    expect(doneLine({ outcome: 'done', ended_at: T0 })).toBeNull() // no duration, no start
  })
})

describe('durations', () => {
  it('seconds, minutes with seconds, hours with minutes', () => {
    expect(durationParts(20_400)).toEqual({ kind: 's', s: 20 })
    expect(durationParts(183_000)).toEqual({ kind: 'ms', m: 3, s: 3 })
    expect(durationParts(3_720_000)).toEqual({ kind: 'hm', h: 1, m: 2 })
    expect(durationParts(-5)).toEqual({ kind: 's', s: 0 })
    const t = useI18nStore.getState().t
    expect(formatDuration(183_000, t)).toBe('3m 03s')
    expect(formatDuration(20_000, t)).toBe('20s')
    useI18nStore.getState().setLocale('zh-TW')
    expect(formatDuration(183_000, useI18nStore.getState().t)).toBe('3 分 03 秒')
    expect(formatDuration(3_720_000, useI18nStore.getState().t)).toBe('1 小時 02 分')
  })
})

describe('DoneTurnLine', () => {
  it('✻ Done · 20s · HH:mm, in the system grey', () => {
    render(<DoneTurnLine turn={{ outcome: 'done', started_at: T0, ended_at: T0 + 20_000, items: [] }} />)
    const el = screen.getByTestId('turn-done')
    expect(el).toHaveClass('text-text-system', 'text-left', 'text-xs')
    expect(el.textContent).toBe('✻Done · 20s · 02:55')
  })
  it('in Chinese', () => {
    useI18nStore.getState().setLocale('zh-TW')
    render(<DoneTurnLine turn={{ outcome: 'done', started_at: T0, ended_at: T0 + 20_000, items: [] }} />)
    expect(screen.getByTestId('turn-done').textContent).toBe('✻完成 · 20 秒 · 02:55')
  })
  it('nothing for a turn that is not done', () => {
    render(<DoneTurnLine turn={{ outcome: 'interrupted', started_at: T0, ended_at: T0 + 1, items: [] }} />)
    expect(screen.queryByTestId('turn-done')).toBeNull()
  })
})

describe('LiveTurnLine', () => {
  it('ticks the elapsed time every second, with the running tool and the subagent count', () => {
    vi.useFakeTimers({ now: T0 + 3_000 })
    render(<LiveTurnLine status="running" turns={[{ outcome: 'running', started_at: T0, items: [step('b'), step('c', { kind: 'task', tool: 'Agent' })] }]} />)
    expect(screen.getByTestId('turn-live').textContent).toBe('✻Running · Bash · 1 subagent(s) · 3s')
    act(() => { vi.advanceTimersByTime(2_000) })
    expect(screen.getByTestId('turn-live').textContent).toContain('5s')
  })
  describe('the words follow what runs', () => {
    const text = (items: unknown[]) => {
      vi.useFakeTimers({ now: T0 + 183_000 })
      render(<LiveTurnLine status="running" turns={[{ outcome: 'running', started_at: T0, items: items as LineTurn['items'] }]} />)
      return screen.getByTestId('turn-live').textContent
    }
    it('a tool: Running · tool · time (no ellipsis)', () => { expect(text([step('b')])).toBe('✻Running · Bash · 3m 03s') })
    it('only subagents: Running · n subagents · time, no tool name', () => {
      expect(text([step('c', { kind: 'task', tool: 'Agent' }), step('d', { kind: 'task', tool: 'Agent' })])).toBe('✻Running · 2 subagent(s) · 3m 03s')
    })
    it('a tool and subagents', () => { expect(text([step('b'), step('c', { kind: 'task', tool: 'Agent' })])).toBe('✻Running · Bash · 1 subagent(s) · 3m 03s') })
    it('nothing running (the model thinks or writes): Thinking… · time', () => { expect(text([step('a', { status: 'done' })])).toBe('✻Thinking… · 3m 03s') })
    it('a turn with no steps yet thinks too, in Chinese as well', () => {
      useI18nStore.getState().setLocale('zh-TW')
      expect(text([])).toBe('✻思考中… · 3 分 03 秒')
    })
  })
  it('a tool that ends puts the line back to thinking', () => {
    vi.useFakeTimers({ now: T0 + 3_000 })
    const turn = (status: string) => [{ outcome: 'running', started_at: T0, items: [step('b', { status })] }] as LineTurn[]
    const { rerender } = render(<LiveTurnLine status="running" turns={turn('running')} />)
    expect(screen.getByTestId('turn-live').textContent).toContain('Running · Bash')
    rerender(<LiveTurnLine status="running" turns={turn('done')} />)
    expect(screen.getByTestId('turn-live').textContent).toBe('✻Thinking… · 3s')
  })
  it('a line that comes on after a long idle counts from the present at once, not from when it mounted', () => {
    vi.useFakeTimers({ now: T0 })
    const idle: LineTurn[] = [{ outcome: 'done', started_at: T0 - 5_000, ended_at: T0, items: [] }]
    const { rerender } = render(<LiveTurnLine status="idle" turns={idle} />)
    act(() => { vi.setSystemTime(T0 + 600_000) }) // ten minutes of idle
    rerender(<LiveTurnLine status="running" turns={[{ outcome: 'running', started_at: T0 + 599_000, items: [] }]} />)
    act(() => { vi.advanceTimersByTime(1) }) // the first refresh, long before the first interval tick
    expect(screen.getByTestId('turn-live').textContent).toContain('1s')
  })
  it('the running line is not a live region (it changes every second); the waiting one is', () => {
    render(<LiveTurnLine status="running" turns={[{ outcome: 'running', started_at: T0, items: [] }]} />)
    expect(screen.getByTestId('turn-live')).not.toHaveAttribute('role')
    cleanup()
    render(<LiveTurnLine status="waiting" turns={[{ outcome: 'running', started_at: T0, items: [] }]} />)
    expect(screen.getByTestId('turn-live')).toHaveAttribute('role', 'status')
  })
  it('waiting for the person: the existing waiting words in the warning colour, no clock', () => {
    render(<LiveTurnLine status="waiting" turns={[{ outcome: 'running', started_at: T0, items: [] }]} />)
    const el = screen.getByTestId('turn-live')
    expect(el.dataset.kind).toBe('waiting')
    expect(el).toHaveClass('text-status-warning-text')
    expect(el.textContent).toContain('Waiting for you')
  })
  it('nothing while idle; stops its timer when it goes', () => {
    vi.useFakeTimers({ now: T0 })
    const { rerender } = render(<LiveTurnLine status="running" turns={[{ outcome: 'running', started_at: T0, items: [] }]} />)
    act(() => { vi.advanceTimersByTime(0) }) // the first refresh has run: only the one-second interval is left
    expect(vi.getTimerCount()).toBe(1)
    rerender(<LiveTurnLine status="idle" turns={[{ outcome: 'done', started_at: T0, ended_at: T0 + 1, items: [] }]} />)
    expect(screen.queryByTestId('turn-live')).toBeNull()
    expect(vi.getTimerCount()).toBe(0)
  })
})

describe('where the lines sit', () => {
  const doneTurn = (index: number, items: ConversationItem[]): Turn => ({ id: `t${index}`, index, started_at: T0, ended_at: T0 + 20_000, duration_ms: 20_000, outcome: 'done', items })
  const runTurn = (index: number, items: ConversationItem[]): Turn => ({ id: `t${index}`, index, started_at: T0, outcome: 'running', items })
  const entry = (turns: Turn[], status: string): ConversationEntry => ({
    doc: { ...emptyDoc(), turns, header: { title: 'x', status, backend: 'terminal', live: true } }, status: 'live', reason: '', paging: false, subagents: {},
  })
  const props = { paneId: 'p-lines', hostId: 'h', sessionId: 's', onSwitchToTerminal: () => {} }
  beforeEach(() => {
    forgetFolds('p-lines\0s')
    forgetScrollMemosWithPrefix('p-lines')
    useConversationStore.setState({ loadBefore: vi.fn(async () => {}) })
  })

  it('deck: a closing line under every done turn, the live line after the last turn, inside the scroll box', () => {
    render(<DeckView {...props} entry={entry([doneTurn(0, [user('a')]), doneTurn(1, [user('b')]), runTurn(2, [user('c'), step('s1')])], 'running')} />)
    const box = screen.getByTestId('deck-scroll')
    expect(screen.getAllByTestId('turn-done')).toHaveLength(2)
    const live = screen.getByTestId('turn-live')
    expect(box).toContainElement(live)
    expect(box.lastElementChild).toBe(live) // the end of the stream
    // each closing line is the last child of its own turn
    for (const sec of screen.getAllByTestId('deck-turn').slice(0, 2)) expect(sec.lastElementChild).toBe(sec.querySelector('[data-testid="turn-done"]'))
  })

  it('deck: idle shows no live line, and a turn that is not done shows no closing line', () => {
    render(<DeckView {...props} entry={entry([doneTurn(0, [user('a')]), { ...runTurn(1, [user('b')]), outcome: 'interrupted', ended_at: T0 + 1 }], 'idle')} />)
    expect(screen.queryByTestId('turn-live')).toBeNull()
    expect(screen.getAllByTestId('turn-done')).toHaveLength(1)
  })

  it('chat: the same two lines', () => {
    const turns: PanelTurn[] = [doneTurn(0, [user('a')]), runTurn(1, [user('b'), step('s1')])]
    render(<ChatView paneKey="p-lines" hostId="h" sessionId="s" title="t" status="running" turns={turns} onSwitchToTerminal={() => {}} active />)
    const box = screen.getByTestId('chat-scroll')
    expect(screen.getAllByTestId('turn-done')).toHaveLength(1)
    expect(box).toContainElement(screen.getByTestId('turn-live'))
    expect(box.lastElementChild).toBe(screen.getByTestId('turn-live'))
  })
})
