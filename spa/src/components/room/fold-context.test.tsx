// spa/src/components/room/fold-context.test.tsx
import { useEffect, type ReactNode } from 'react'
import { describe, it, expect, vi } from 'vitest'
import { act, render, renderHook, screen, fireEvent } from '@testing-library/react'
import {
  FoldContext,
  TurnIndexContext,
  useFold,
  useFoldMemory,
  useFoldStore,
  type FoldStore,
} from './fold-context'

/**
 * A leaf that registers itself with the surrounding turn, like a real fold.
 * `testId` defaults to the key and is only passed when two leaves deliberately
 * share one key — nothing in the pane guarantees fold keys are unique.
 */
function Foldable({ foldKey, testId = foldKey }: { foldKey: string; testId?: string }) {
  const [expanded, toggle] = useFold(foldKey)
  return (
    <button data-testid={testId} onClick={toggle}>
      {expanded ? 'open' : 'closed'}
    </button>
  )
}

/** Owns the memory and publishes a turn index, the way RoomTurnGroup will. */
function Turn({
  index,
  onStore,
  children,
}: {
  index: number
  onStore: (store: FoldStore) => void
  children: ReactNode
}) {
  const store = useFoldMemory()
  useEffect(() => {
    onStore(store)
  }, [store, onStore])
  return (
    <FoldContext.Provider value={store}>
      <TurnIndexContext.Provider value={index}>{children}</TurnIndexContext.Provider>
    </FoldContext.Provider>
  )
}

/**
 * One memory shared by several turns, the way the transcript will own it. The
 * `Turn` harness above makes a store per turn, which cannot show a key living
 * in two turns of the *same* pane.
 */
function Pane({
  onStore,
  children,
}: {
  onStore: (store: FoldStore) => void
  children: ReactNode
}) {
  const store = useFoldMemory()
  useEffect(() => {
    onStore(store)
  }, [store, onStore])
  return <FoldContext.Provider value={store}>{children}</FoldContext.Provider>
}

describe('fold memory', () => {
  it('throws outside a provider', () => {
    // A block that reads the store without a provider above it has no pane
    // memory: it would silently fall back to collapsing forever. That has to
    // be a crash at the first render, not a quiet loss of state.
    const quiet = vi.spyOn(console, 'error').mockImplementation(() => {})
    try {
      expect(() => renderHook(() => useFoldStore())).toThrow(
        /useFoldStore must be used inside a FoldContext\.Provider/,
      )
    } finally {
      quiet.mockRestore()
    }
  })

  it('defaults to collapsed', () => {
    const { result } = renderHook(() => useFoldMemory())
    expect(result.current.isExpanded('op-1')).toBe(false)
    expect(result.current.isExpanded('never-seen')).toBe(false)
  })

  it('toggles one key', () => {
    const { result } = renderHook(() => useFoldMemory())
    act(() => result.current.toggle('op-1'))
    expect(result.current.isExpanded('op-1')).toBe(true)
    act(() => result.current.toggle('op-1'))
    expect(result.current.isExpanded('op-1')).toBe(false)
  })

  // No render-count assertion here: React 19 batches the updates inside one
  // `act`, so a per-key `setExpanded` loop renders once too. Measured — the
  // assertion this test used to carry passed against that mutation, so it
  // guaranteed nothing. What is worth asserting is the outcome: every key of
  // the turn moves.
  it('setTurn expands every key registered in that turn', () => {
    const { result } = renderHook(() => useFoldMemory())
    act(() => {
      result.current.register(0, 'op-1')
      result.current.register(0, 'op-2')
      result.current.register(0, 'op-3')
    })
    act(() => result.current.setTurn(0, true))
    expect(result.current.isExpanded('op-1')).toBe(true)
    expect(result.current.isExpanded('op-2')).toBe(true)
    expect(result.current.isExpanded('op-3')).toBe(true)
  })

  it('setTurn leaves another turn alone', () => {
    const { result } = renderHook(() => useFoldMemory())
    act(() => {
      result.current.register(0, 'turn-0-op')
      result.current.register(1, 'turn-1-op')
    })
    act(() => result.current.setTurn(0, true))
    expect(result.current.isExpanded('turn-0-op')).toBe(true)
    expect(result.current.isExpanded('turn-1-op')).toBe(false)
  })

  it('expands a diff key and a thinking key registered by nested components', () => {
    const seen: { store: FoldStore | null } = { store: null }
    const capture = (store: FoldStore) => {
      seen.store = store
    }
    render(
      <Turn index={0} onStore={capture}>
        <Foldable foldKey="op-1" />
        <Foldable foldKey="op-1:diff" />
        <Foldable foldKey="thinking-0" />
      </Turn>,
    )
    expect(screen.getByTestId('op-1')).toHaveTextContent('closed')
    expect(screen.getByTestId('op-1:diff')).toHaveTextContent('closed')
    expect(screen.getByTestId('thinking-0')).toHaveTextContent('closed')

    act(() => seen.store!.setTurn(0, true))

    expect(screen.getByTestId('op-1')).toHaveTextContent('open')
    expect(screen.getByTestId('op-1:diff')).toHaveTextContent('open')
    expect(screen.getByTestId('thinking-0')).toHaveTextContent('open')
  })

  it('unregisters a key when its component unmounts', () => {
    const seen: { store: FoldStore | null } = { store: null }
    const capture = (store: FoldStore) => {
      seen.store = store
    }
    const tree = (withDiff: boolean) => (
      <Turn index={0} onStore={capture}>
        <Foldable foldKey="op-1" />
        {withDiff ? <Foldable foldKey="op-1:diff" /> : null}
      </Turn>
    )
    const { rerender } = render(tree(true))
    rerender(tree(false))
    expect(screen.queryByTestId('op-1:diff')).toBeNull()

    act(() => seen.store!.setTurn(0, true))

    expect(screen.getByTestId('op-1')).toHaveTextContent('open')
    expect(seen.store!.isExpanded('op-1:diff')).toBe(false)
  })

  // A fold key is a tool_use id or something derived from one: foreign data,
  // with nothing in the interface or the effect enforcing uniqueness across the
  // pane. So an unregister that swept every turn let turn 0's unmount strip a
  // key turn 1 still had mounted, and expand-all silently skipped that block
  // from then on (attack A4).
  it('keeps a key registered in one turn when the same key unmounts in another', () => {
    const seen: { store: FoldStore | null } = { store: null }
    const capture = (store: FoldStore) => {
      seen.store = store
    }
    const tree = (withTurn0: boolean) => (
      <Pane onStore={capture}>
        <TurnIndexContext.Provider value={0}>
          {withTurn0 ? <Foldable foldKey="op-1" testId="turn-0" /> : null}
        </TurnIndexContext.Provider>
        <TurnIndexContext.Provider value={1}>
          <Foldable foldKey="op-1" testId="turn-1" />
        </TurnIndexContext.Provider>
      </Pane>
    )
    const { rerender } = render(tree(true))
    rerender(tree(false))
    expect(screen.queryByTestId('turn-0')).toBeNull()

    act(() => seen.store!.setTurn(1, true))

    expect(screen.getByTestId('turn-1')).toHaveTextContent('open')
  })

  // Same key, same turn, two mounted components: the count is what makes the
  // first unmount a decrement rather than a removal. Without it the A4 test
  // above still passes (a per-turn Set is enough for that), so this is the only
  // guard on the reference count.
  it('keeps a key registered while another component in the same turn still holds it', () => {
    const seen: { store: FoldStore | null } = { store: null }
    const capture = (store: FoldStore) => {
      seen.store = store
    }
    const tree = (withFirst: boolean) => (
      <Turn index={0} onStore={capture}>
        {withFirst ? <Foldable foldKey="op-1" testId="first" /> : null}
        <Foldable foldKey="op-1" testId="second" />
      </Turn>
    )
    const { rerender } = render(tree(true))
    rerender(tree(false))
    expect(screen.queryByTestId('first')).toBeNull()

    act(() => seen.store!.setTurn(0, true))

    expect(screen.getByTestId('second')).toHaveTextContent('open')
  })

  it('keeps state across a child remount', () => {
    const seen: { store: FoldStore | null } = { store: null }
    const capture = (store: FoldStore) => {
      seen.store = store
    }
    const tree = (show: boolean) => (
      <Turn index={0} onStore={capture}>
        {show ? <Foldable foldKey="op-1" /> : null}
      </Turn>
    )
    const { rerender } = render(tree(true))
    fireEvent.click(screen.getByTestId('op-1'))
    expect(screen.getByTestId('op-1')).toHaveTextContent('open')

    rerender(tree(false))
    expect(screen.queryByTestId('op-1')).toBeNull()
    rerender(tree(true))

    expect(screen.getByTestId('op-1')).toHaveTextContent('open')
  })
})
