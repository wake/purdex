// spa/src/components/room/fold-context.tsx — spec §3.2: one fold memory per
// pane. A component-local `useState` dies when the block unmounts (a virtualised
// list, a re-keyed turn), so the expansion lives above the blocks and is read
// through a context.
import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
} from 'react'

export interface FoldStore {
  isExpanded(key: string): boolean
  toggle(key: string): void
  /** Every foldable thing announces itself, so expand-all knows what "all" is. */
  register(turnIndex: number, key: string): void
  unregister(key: string): void
  setTurn(turnIndex: number, expanded: boolean): void
}

export const FoldContext = createContext<FoldStore | null>(null)

/** Provided by RoomTurnGroup so a nested block does not have to be told its turn. */
export const TurnIndexContext = createContext<number>(-1)

export function useFoldStore(): FoldStore {
  const store = useContext(FoldContext)
  if (!store) throw new Error('useFoldStore must be used inside a FoldContext.Provider')
  return store
}

/**
 * The provider's implementation hook. Expansion is plain state; the set of keys
 * belonging to each turn is a ref, because registering must never re-render the
 * tree that is registering.
 */
export function useFoldMemory(): FoldStore {
  const [expanded, setExpanded] = useState<Record<string, boolean>>({})
  const turns = useRef<Map<number, Set<string>>>(new Map())

  // Own-key lookup: a key like `constructor` must not read off Object.prototype.
  const isExpanded = useCallback(
    (key: string) => (Object.hasOwn(expanded, key) ? expanded[key] : false),
    [expanded],
  )

  const toggle = useCallback((key: string) => {
    setExpanded(prev => ({ ...prev, [key]: !(Object.hasOwn(prev, key) && prev[key]) }))
  }, [])

  const register = useCallback((turnIndex: number, key: string) => {
    const keys = turns.current.get(turnIndex)
    if (keys) keys.add(key)
    else turns.current.set(turnIndex, new Set([key]))
  }, [])

  const unregister = useCallback((key: string) => {
    for (const [turnIndex, keys] of turns.current) {
      if (keys.delete(key) && keys.size === 0) turns.current.delete(turnIndex)
    }
  }, [])

  // Every key of the turn moves in a single state update, so expand-all is one
  // render rather than one per block.
  const setTurn = useCallback((turnIndex: number, value: boolean) => {
    const keys = turns.current.get(turnIndex)
    if (!keys || keys.size === 0) return
    setExpanded(prev => {
      const next = { ...prev }
      for (const key of keys) next[key] = value
      return next
    })
  }, [])

  return useMemo(
    () => ({ isExpanded, toggle, register, unregister, setTurn }),
    [isExpanded, toggle, register, unregister, setTurn],
  )
}

/**
 * Registers `key` under the surrounding turn for the component's lifetime and
 * returns [expanded, toggle]. Registration rather than a key list handed down
 * from the turn: a turn holds more foldable things than its operations (a raw
 * input, a diff at `${foldKey}:diff`, a thinking block, later a subagent), and a
 * list maintained by the caller would silently miss whatever is added next.
 */
export function useFold(key: string): [boolean, () => void] {
  const store = useFoldStore()
  const turnIndex = useContext(TurnIndexContext)
  const { register, unregister } = store

  useEffect(() => {
    register(turnIndex, key)
    return () => unregister(key)
  }, [register, unregister, turnIndex, key])

  const toggle = useCallback(() => store.toggle(key), [store, key])
  return [store.isExpanded(key), toggle]
}
