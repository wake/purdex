// c9d9c7-67 (#2592): the output tokens of a turn (turn.output_tokens) on the live line (`↓ 9.2k`, the running turn's) and on the closing line of every
// finished turn, history included. Absent from an older daemon, or while the turn has none: the lines are what they were.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { cleanup, render, screen } from '@testing-library/react'
import { doneLine, formatTokens, liveLine, type LineTurn } from '../../lib/conversations/turn-line'
import { useI18nStore } from '../../stores/useI18nStore'
import { DoneTurnLine, LiveTurnLine } from './TurnLines'
import type { StepItem } from '../../lib/conversations/types'

const T0 = new Date(2026, 9, 11, 2, 55, 0).getTime()
const step = (id: string, over: Partial<StepItem> = {}): StepItem =>
  ({ type: 'step', id, at: 1, index: 0, kind: 'execute', tool: 'Bash', status: 'running', summary: id, started_at: 1000, input: {}, ...over })

beforeEach(() => { cleanup(); useI18nStore.getState().setLocale('en') })
afterEach(() => { vi.useRealTimers() })

describe('formatTokens', () => {
  it('the number itself below a thousand, thousands with one decimal above', () => {
    expect([0, 7, 842, 999, 1000, 1049, 1050, 9149, 9200, 12_400, 103_000].map(formatTokens))
      .toEqual(['0', '7', '842', '999', '1.0k', '1.0k', '1.1k', '9.1k', '9.2k', '12.4k', '103.0k'])
  })
})

describe('the data rules', () => {
  const running = (over: Partial<LineTurn> = {}): LineTurn => ({ outcome: 'running', started_at: T0, items: [], ...over })
  it('the live line takes the running turn\'s count; none, zero, a bad value and an older daemon are null', () => {
    expect(liveLine([running({ output_tokens: 9200 })], 'running')?.outputTokens).toBe(9200)
    expect(liveLine([running()], 'running')?.outputTokens).toBeNull()
    expect(liveLine([running({ output_tokens: 0 })], 'running')?.outputTokens).toBeNull()
    expect(liveLine([running({ output_tokens: -5 })], 'running')?.outputTokens).toBeNull()
    expect(liveLine([running({ output_tokens: '12' as unknown as number })], 'running')?.outputTokens).toBeNull()
  })
  it('only the running (last) turn counts for the live line, not an earlier finished one', () => {
    const done: LineTurn = { outcome: 'done', started_at: T0 - 100_000, ended_at: T0 - 1, output_tokens: 5000, items: [] }
    expect(liveLine([done, running()], 'running')?.outputTokens).toBeNull()
  })
  it('the closing line carries the finished turn\'s count', () => {
    const turn = { outcome: 'done', started_at: T0, ended_at: T0 + 20_000 }
    expect(doneLine({ ...turn, output_tokens: 3100 })?.outputTokens).toBe(3100)
    expect(doneLine(turn)?.outputTokens).toBeNull()
  })
})

describe('the live line', () => {
  it('shows ↓ between the tool / subagents and the clock', () => {
    vi.useFakeTimers({ now: T0 + 183_000 })
    render(<LiveTurnLine status="running" turns={[{ outcome: 'running', started_at: T0, output_tokens: 9200, items: [step('b'), step('c', { kind: 'task', tool: 'Agent' })] }]} />)
    expect(screen.getByTestId('turn-live').textContent).toBe('✻Running · Bash · 1 subagent(s) · ↓ 9.2k · 3m 03s')
  })
  it('thinking with tokens: Thinking… · ↓ 842 · clock', () => {
    vi.useFakeTimers({ now: T0 + 12_000 })
    render(<LiveTurnLine status="running" turns={[{ outcome: 'running', started_at: T0, output_tokens: 842, items: [] }]} />)
    expect(screen.getByTestId('turn-live').textContent).toBe('✻Thinking… · ↓ 842 · 12s')
  })
  it('without a count the line is what it was', () => {
    vi.useFakeTimers({ now: T0 + 12_000 })
    render(<LiveTurnLine status="running" turns={[{ outcome: 'running', started_at: T0, items: [] }]} />)
    expect(screen.getByTestId('turn-live').textContent).toBe('✻Thinking… · 12s')
  })
  it('the waiting line stays as it is (no count)', () => {
    render(<LiveTurnLine status="waiting" turns={[{ outcome: 'running', started_at: T0, output_tokens: 9200, items: [] }]} />)
    expect(screen.getByTestId('turn-live').textContent).toBe('✻Waiting for you')
  })
})

describe('the closing line', () => {
  it('✻ Done · 20s · ↓ 3.1k · HH:mm, for any finished turn', () => {
    render(<DoneTurnLine turn={{ outcome: 'done', started_at: T0, ended_at: T0 + 20_000, output_tokens: 3100, items: [] }} />)
    expect(screen.getByTestId('turn-done').textContent).toBe('✻Done · 20s · ↓ 3.1k · 02:55')
  })
  it('without a count: unchanged', () => {
    render(<DoneTurnLine turn={{ outcome: 'done', started_at: T0, ended_at: T0 + 20_000, items: [] }} />)
    expect(screen.getByTestId('turn-done').textContent).toBe('✻Done · 20s · 02:55')
  })
  it('in Chinese', () => {
    useI18nStore.getState().setLocale('zh-TW')
    render(<DoneTurnLine turn={{ outcome: 'done', started_at: T0, ended_at: T0 + 20_000, output_tokens: 842, items: [] }} />)
    expect(screen.getByTestId('turn-done').textContent).toBe('✻完成 · 20 秒 · ↓ 842 · 02:55')
  })
})
