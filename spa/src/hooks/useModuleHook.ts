import { useReducer, useEffect, useRef, useMemo } from 'react'
import type { HookModule, HookModuleStatus } from '../lib/hook-modules'
import { useAgentStore } from '../stores/useAgentStore'

type State = {
  status: HookModuleStatus | null
  loading: boolean
  error: string | null
}

type Action =
  | { type: 'fetch_start' }
  | { type: 'fetch_success'; payload: HookModuleStatus }
  | { type: 'fetch_error'; payload: string }
  | { type: 'setup_start' }
  | { type: 'setup_success'; payload: HookModuleStatus }
  | { type: 'setup_error'; payload: string }

function reducer(state: State, action: Action): State {
  switch (action.type) {
    case 'fetch_start':
      return { ...state, loading: true, error: null }
    case 'fetch_success':
      return { status: action.payload, loading: false, error: null }
    case 'fetch_error':
      return { ...state, loading: false, error: action.payload }
    case 'setup_start':
      return { ...state, loading: true, error: null }
    case 'setup_success':
      return { status: action.payload, loading: false, error: null }
    case 'setup_error':
      return { ...state, loading: false, error: action.payload }
  }
}

export function useModuleHook(module: HookModule, hostId: string, refreshKey: number) {
  const [state, dispatch] = useReducer(reducer, { status: null, loading: true, error: null })
  const mountedRef = useRef(true)

  useEffect(() => () => { mountedRef.current = false }, [])

  useEffect(() => {
    let cancelled = false
    dispatch({ type: 'fetch_start' })
    module.fetchStatus(hostId)
      .then((data) => { if (!cancelled) dispatch({ type: 'fetch_success', payload: data }) })
      .catch((err) => { if (!cancelled) dispatch({ type: 'fetch_error', payload: err instanceof Error ? err.message : String(err) }) })
    return () => { cancelled = true }
  }, [module, hostId, refreshKey])

  const setup = async (action: 'install' | 'remove') => {
    if (!mountedRef.current) return
    dispatch({ type: 'setup_start' })
    try {
      const data = await module.setup(hostId, action)
      if (mountedRef.current) dispatch({ type: 'setup_success', payload: data })
    } catch (err) {
      if (mountedRef.current) dispatch({ type: 'setup_error', payload: err instanceof Error ? err.message : String(err) })
    }
  }

  const events = useAgentStore((s) => s.lastEvents)
  const lastTrigger = useMemo(
    () => module.getLastTrigger?.(hostId, events) ?? null,
    [module, hostId, events],
  )

  return { status: state.status, loading: state.loading, error: state.error, setup, lastTrigger }
}
