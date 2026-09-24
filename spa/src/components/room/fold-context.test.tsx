// spa/src/components/room/fold-context.test.tsx
import { useEffect, type ReactNode } from 'react'
import { describe, it, expect } from 'vitest'
import { act, render, renderHook, screen, fireEvent } from '@testing-library/react'
import { FoldContext, TurnIndexContext, useFold, useFoldMemory, type FoldStore } from './fold-context'

/** A leaf that registers itself with the surrounding turn, like a real fold. */
function Foldable({ foldKey }: { foldKey: string }) {
  const [expanded, toggle] = useFold(foldKey)
  return (
    <button data-testid={foldKey} onClick={toggle}>
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

describe('fold memory', () => {
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

  it('setTurn expands every key registered in that turn in one update', () => {
    let renders = 0
    const { result } = renderHook(() => {
      renders += 1
      return useFoldMemory()
    })
    act(() => {
      result.current.register(0, 'op-1')
      result.current.register(0, 'op-2')
      result.current.register(0, 'op-3')
    })
    const before = renders
    act(() => result.current.setTurn(0, true))
    expect(result.current.isExpanded('op-1')).toBe(true)
    expect(result.current.isExpanded('op-2')).toBe(true)
    expect(result.current.isExpanded('op-3')).toBe(true)
    expect(renders - before).toBe(1)
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
