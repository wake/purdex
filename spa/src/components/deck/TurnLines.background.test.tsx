// c9d9c7-67 PR2 (#2592): the background work Claude Code still runs (header.background: shell / subagent / other counts) — on the running line
// ("· 1 background command") and on the closing line of the conversation's LAST turn ("· 1 background command still running"). Nothing from an older
// daemon, nothing for a bad value, nothing on earlier turns.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { cleanup, render, screen } from '@testing-library/react'
import { backgroundCounts, backgroundText } from '../../lib/conversations/turn-line'
import { emptyDoc } from '../../lib/conversations/model'
import { useI18nStore } from '../../stores/useI18nStore'
import { useHostConfigStore } from '../../stores/useHostConfigStore'
import type { ConversationEntry } from '../../stores/useConversationStore'
import type { Header, HeaderBackground, Turn } from '../../lib/conversations/types'
import { DoneTurnLine, LiveTurnLine } from './TurnLines'
import { DeckView } from './DeckView'
import { ChatView } from './ChatView'

HTMLElement.prototype.scrollTo = function () {} as never
const T0 = new Date(2026, 9, 11, 2, 55, 0).getTime()
const t = (k: string, p?: Record<string, string | number>) => useI18nStore.getState().t(k, p)

beforeEach(() => { cleanup(); useI18nStore.getState().setLocale('en'); useHostConfigStore.setState({ byHost: {}, ensureLoaded: async () => {} }) })
afterEach(() => { vi.useRealTimers() })

describe('the data', () => {
  it('counts by kind in a fixed order; absent, zero, negative, fractional and non-number are none', () => {
    expect(backgroundCounts(undefined)).toEqual([])
    expect(backgroundCounts({})).toEqual([])
    expect(backgroundCounts({ other: 2, subagent: 1, shell: 3 })).toEqual([['shell', 3], ['subagent', 1], ['other', 2]])
    expect(backgroundCounts({ shell: 0, subagent: -1, other: Number.NaN })).toEqual([])
    expect(backgroundCounts({ shell: '2' as unknown as number })).toEqual([])
    expect(backgroundCounts({ shell: 1.9 })).toEqual([['shell', 1]])
    expect(backgroundCounts(null as unknown as HeaderBackground)).toEqual([])
    expect(backgroundCounts('x' as unknown as HeaderBackground)).toEqual([])
  })
  it('the text: one kind, several kinds joined by the locale separator (、 in Chinese)', () => {
    expect(backgroundText({ shell: 1 }, t)).toBe('1 background command(s)')
    expect(backgroundText({ shell: 1, subagent: 2, other: 3 }, t)).toBe('1 background command(s), 2 background subagent(s), 3 background task(s)')
    expect(backgroundText(undefined, t)).toBe('')
    useI18nStore.getState().setLocale('zh-TW')
    expect(backgroundText({ shell: 1, subagent: 2 }, t)).toBe('1 個背景指令、2 個背景 subagent')
    expect(backgroundText({ other: 4 }, t)).toBe('4 個背景工作')
  })
})

describe('the running line', () => {
  it('says what runs in the background after the tokens, before the clock', () => {
    vi.useFakeTimers({ now: T0 + 183_000 })
    render(<LiveTurnLine status="running" background={{ shell: 1 }} turns={[{ outcome: 'running', started_at: T0, output_tokens: 9200, items: [] }]} />)
    expect(screen.getByTestId('turn-live').textContent).toBe('✻Thinking… · ↓ 9.2k · 1 background command(s) · 3m 03s')
  })
  it('in Chinese, and without background work the line is what it was', () => {
    vi.useFakeTimers({ now: T0 + 12_000 })
    useI18nStore.getState().setLocale('zh-TW')
    render(<LiveTurnLine status="running" background={{ shell: 1, subagent: 2 }} turns={[{ outcome: 'running', started_at: T0, items: [] }]} />)
    expect(screen.getByTestId('turn-live').textContent).toBe('✻思考中… · 1 個背景指令、2 個背景 subagent · 12 秒')
    cleanup()
    render(<LiveTurnLine status="running" turns={[{ outcome: 'running', started_at: T0, items: [] }]} />)
    expect(screen.getByTestId('turn-live').textContent).toBe('✻思考中… · 12 秒')
  })
  it('the waiting line stays as it is', () => {
    render(<LiveTurnLine status="waiting" background={{ shell: 1 }} turns={[{ outcome: 'running', started_at: T0, items: [] }]} />)
    expect(screen.getByTestId('turn-live').textContent).toBe('✻Waiting for you')
  })
})

describe('the closing line', () => {
  const turn = { outcome: 'done', started_at: T0, ended_at: T0 + 20_000, items: [] }
  it('ends with what still runs, when the caller gives it (the last turn)', () => {
    render(<DoneTurnLine turn={turn} background={{ shell: 1 }} />)
    expect(screen.getByTestId('turn-done').textContent).toBe('✻Done · 20s · 02:55 · 1 background command(s) still running')
  })
  it('Chinese: 「· 1 個背景指令仍在執行」, several kinds with 、', () => {
    useI18nStore.getState().setLocale('zh-TW')
    render(<DoneTurnLine turn={turn} background={{ shell: 1 }} />)
    expect(screen.getByTestId('turn-done').textContent).toBe('✻完成 · 20 秒 · 02:55 · 1 個背景指令仍在執行')
    cleanup()
    render(<DoneTurnLine turn={{ ...turn, output_tokens: 3100 }} background={{ subagent: 2, other: 1 }} />)
    expect(screen.getByTestId('turn-done').textContent).toBe('✻完成 · 20 秒 · ↓ 3.1k · 02:55 · 2 個背景 subagent、1 個背景工作仍在執行')
  })
  it('without it the line is what it was', () => {
    render(<DoneTurnLine turn={turn} />)
    expect(screen.getByTestId('turn-done').textContent).toBe('✻Done · 20s · 02:55')
  })
})

describe('where the deck and the chat draw it', () => {
  const doneTurn = (i: number): Turn => ({ id: `t${i}`, index: i, started_at: T0 + i * 100_000, ended_at: T0 + i * 100_000 + 20_000, outcome: 'done', items: [{ type: 'user', id: `u${i}`, at: 1, index: 0, text: `q${i}`, source: 'user' }] })
  const header = (status: string, background?: HeaderBackground): Header => ({ title: 't', status, backend: 'terminal', live: true, ...(background ? { background } : {}) })
  const entry = (h: Header): ConversationEntry => ({ doc: { ...emptyDoc(), turns: [doneTurn(0), doneTurn(1)], header: h, capabilities: null }, status: 'live', reason: '', paging: false, subagents: {} })
  const props = { paneId: 'p-bg', hostId: 'h', sessionId: 's', onSwitchToTerminal: () => {} }

  it('the deck says it on the LAST turn\'s closing line only', () => {
    render(<DeckView {...props} entry={entry(header('idle', { shell: 1 }))} />)
    const lines = screen.getAllByTestId('turn-done').map((e) => e.textContent)
    expect(lines).toHaveLength(2)
    expect(lines[0]).not.toMatch(/background/)
    expect(lines[1]).toMatch(/1 background command\(s\) still running$/)
  })
  it('the deck without the field: no line says it', () => {
    render(<DeckView {...props} entry={entry(header('idle'))} />)
    expect(screen.getAllByTestId('turn-done').map((e) => e.textContent).join('|')).not.toMatch(/background/)
  })
  it('the chat says it on the last turn only', () => {
    render(<ChatView paneKey="p-bg" hostId="h" sessionId="s" title="t" status="idle" background={{ subagent: 1 }} turns={[doneTurn(0), doneTurn(1)]} onSwitchToTerminal={() => {}} active />)
    const lines = screen.getAllByTestId('turn-done').map((e) => e.textContent)
    expect(lines[0]).not.toMatch(/background/)
    expect(lines[1]).toMatch(/1 background subagent\(s\) still running$/)
  })
})
