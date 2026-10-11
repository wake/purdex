// An Agent/Task step in the deck (c9d9c7-54 pt 4): type, description, time and state on the line; the line opens the panel, the chevron
// unfolds the agent's own steps in place (read on demand, kept by the conversation).
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { act, cleanup, fireEvent, render, screen } from '@testing-library/react'
import type { ReactNode } from 'react'
import { FoldContext } from '../room/fold-context'
import { usePaneFoldStore, forgetFolds } from '../../lib/conversations/fold-memory'
import { useI18nStore } from '../../stores/useI18nStore'
import type { ConversationItem, StepItem } from '../../lib/conversations/types'
import { DeckItem } from './DeckItem'
import type { StepActions } from './StepViews'

const NOW = new Date(2026, 9, 11, 3, 0, 0).getTime()
const task = (over: Partial<StepItem> = {}): StepItem => ({
  type: 'step', id: 'task-1', at: 1, index: 0, kind: 'task', tool: 'Agent', status: 'done', summary: 'explore', started_at: NOW - 183_000, duration_ms: 183_000, input: {},
  subagent: { agent_id: 'ag1', description: 'Explore the repo', type: 'Explore' }, ...over,
})
const child = (id: string, over: Partial<StepItem> = {}): ConversationItem => ({ type: 'step', id, at: 1, index: 0, kind: 'read', tool: 'Read', status: 'done', summary: `child ${id}`, started_at: 1, input: {}, ...over }) as ConversationItem

function Folds({ children }: { children: ReactNode }) {
  return <FoldContext.Provider value={usePaneFoldStore('p-task')}>{children}</FoldContext.Provider>
}
const show = (step: StepItem, actions?: StepActions) => render(<Folds><DeckItem item={step} actions={actions} /></Folds>)

beforeEach(() => { cleanup(); forgetFolds('p-task'); useI18nStore.getState().setLocale('en') })
afterEach(() => { vi.useRealTimers() })

describe('the Agent step line', () => {
  it('names the type and the description, and how long a finished one took', () => {
    show(task())
    const line = screen.getByTestId('deck-step-task')
    expect(line).toHaveTextContent('Explore')
    expect(line).toHaveTextContent('Explore the repo')
    expect(screen.getByTestId('subagent-time').textContent).toBe('3m 03s')
  })

  it('a running one counts up from its start, once a second, with the running dot', () => {
    vi.useFakeTimers({ now: NOW })
    show(task({ status: 'running', duration_ms: undefined, started_at: NOW - 5_000 }))
    expect(screen.getByTestId('step-dot')).toHaveAttribute('data-state', 'running')
    expect(screen.queryByTestId('step-running')).toBeNull()
    expect(screen.getByTestId('subagent-time').textContent).toBe('5s')
    act(() => { vi.advanceTimersByTime(3_000) })
    expect(screen.getByTestId('subagent-time').textContent).toBe('8s')
  })

  it('no time when the conversation does not say', () => {
    show(task({ duration_ms: undefined }))
    expect(screen.queryByTestId('subagent-time')).toBeNull()
  })

  it('the line still opens the right panel', () => {
    const onOpenSubagent = vi.fn()
    show(task(), { onOpenSubagent })
    fireEvent.click(screen.getByTestId('deck-step-task'))
    expect(onOpenSubagent).toHaveBeenCalledTimes(1)
  })
})

describe('unfolding the agent\'s steps', () => {
  const ready = (items: ConversationItem[]) => ({ ag1: { state: 'ready' as const, answer: { items, partial: false } } })
  const render1 = (items: ConversationItem[]) => items.map((it) => <DeckItem key={it.id} item={it} />)

  it('has no chevron when the deck cannot load an agent', () => {
    show(task())
    expect(screen.queryByTestId('deck-step-task-toggle')).toBeNull()
  })

  it('opens on the chevron: asks for the agent once, then draws its steps one level in', () => {
    const loadSubagent = vi.fn()
    const base: StepActions = { loadSubagent, renderSubagent: render1 }
    const { rerender } = show(task(), { ...base, subagents: {} })
    expect(screen.queryByTestId('subagent-steps')).toBeNull()
    expect(loadSubagent).not.toHaveBeenCalled()
    fireEvent.click(screen.getByTestId('deck-step-task-toggle'))
    expect(screen.getByTestId('deck-step-task-toggle')).toHaveAttribute('aria-expanded', 'true')
    expect(loadSubagent).toHaveBeenCalledWith('ag1')
    expect(screen.getByTestId('subagent-loading')).toBeInTheDocument()
    rerender(<Folds><DeckItem item={task()} actions={{ ...base, subagents: ready([child('c1'), child('c2')]) }} /></Folds>)
    const box = screen.getByTestId('subagent-steps')
    expect(box).toHaveClass('pl-3', 'border-l')
    expect(box.textContent).toContain('child c1')
    expect(box.textContent).toContain('child c2')
    fireEvent.click(screen.getByTestId('deck-step-task-toggle'))
    expect(screen.queryByTestId('subagent-steps')).toBeNull()
  })

  it('says so when the agent has no steps yet, or could not be read', () => {
    const base: StepActions = { loadSubagent: vi.fn(), renderSubagent: render1 }
    const { rerender } = show(task(), { ...base, subagents: ready([]) })
    fireEvent.click(screen.getByTestId('deck-step-task-toggle'))
    expect(screen.getByTestId('subagent-empty')).toBeInTheDocument()
    rerender(<Folds><DeckItem item={task()} actions={{ ...base, subagents: { ag1: { state: 'error' } } }} /></Folds>)
    expect(screen.getByTestId('subagent-error')).toBeInTheDocument()
  })

  it('the unfolded state survives the deck unmounting with its tab (it lives in the fold memory)', () => {
    const actions: StepActions = { loadSubagent: vi.fn(), renderSubagent: render1, subagents: ready([child('c1')]) }
    const first = show(task(), actions)
    fireEvent.click(screen.getByTestId('deck-step-task-toggle'))
    expect(screen.getByTestId('subagent-steps')).toBeInTheDocument()
    first.unmount()
    show(task(), actions)
    expect(screen.getByTestId('subagent-steps')).toBeInTheDocument()
  })
})
