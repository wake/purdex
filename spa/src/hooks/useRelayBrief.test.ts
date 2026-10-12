// spa/src/hooks/useRelayBrief.test.ts — the report follows the session it is asked about: when the session changes under a
// mounted hook, the previous session's late answer is dropped and the old report is not shown for the new one.
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { renderHook, act } from '@testing-library/react'
import { useRelayBrief } from './useRelayBrief'
import { useWorkbookStore } from '../stores/useWorkbookStore'
import { fetchConversation, fetchTodos } from '../lib/workbook/api'

vi.mock('../lib/workbook/api', () => ({ fetchConversation: vi.fn(), fetchTodos: vi.fn() }))
const conv = vi.mocked(fetchConversation)
const todos = vi.mocked(fetchTodos)

const entry = (text: string) => ({ id: 1, state: 'ok', kind: 'turn', entry: text, thing: '' }) as never
const page = (text: string) => ({ kind: 'ok' as const, page: { convKey: 'c', status: '', statusAt: 0, entries: [entry(text)], todos: null, refreshAvailable: null } })
const deferred = <T,>() => { let resolve!: (v: T) => void; const promise = new Promise<T>((r) => { resolve = r }); return { promise, resolve } }

beforeEach(() => {
  conv.mockReset(); todos.mockReset()
  todos.mockResolvedValue({ kind: 'not_found' })
  useWorkbookStore.getState().setSupport('h', { v1: true, v2: true })
})

describe('useRelayBrief', () => {
  it('drops the previous session\'s late answer after the session changes', async () => {
    const first = deferred<ReturnType<typeof page>>()
    conv.mockReturnValueOnce(first.promise)
    conv.mockResolvedValueOnce(page('second'))
    const { result, rerender } = renderHook(({ s }) => useRelayBrief('h', s), { initialProps: { s: 'A' } })
    rerender({ s: 'B' })
    await act(async () => { await Promise.resolve(); await Promise.resolve() })
    expect(result.current.entries).toEqual(['second'])
    await act(async () => { first.resolve(page('STALE')); await Promise.resolve(); await Promise.resolve() })
    expect(result.current.entries).toEqual(['second'])
  })
  it('does not show the old session\'s report while the new one loads', async () => {
    conv.mockResolvedValueOnce(page('first'))
    conv.mockReturnValueOnce(new Promise(() => {}))
    const { result, rerender } = renderHook(({ s }) => useRelayBrief('h', s), { initialProps: { s: 'A' } })
    await act(async () => { await Promise.resolve(); await Promise.resolve() })
    expect(result.current.entries).toEqual(['first'])
    rerender({ s: 'B' })
    expect(result.current.entries).toEqual([])
  })
})
