import { useState, useEffect, useRef, useMemo } from 'react'
import type { HookModule, HookModuleStatus } from '../lib/hook-modules'
import { useAgentStore } from '../stores/useAgentStore'

export function useModuleHook(module: HookModule, hostId: string, refreshKey: number) {
  const [status, setStatus] = useState<HookModuleStatus | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const mountedRef = useRef(true)

  useEffect(() => () => { mountedRef.current = false }, [])

  useEffect(() => {
    let cancelled = false
    setLoading(true)
    setError(null)
    module.fetchStatus(hostId)
      .then((data) => { if (!cancelled) setStatus(data) })
      .catch((err) => { if (!cancelled) setError(err instanceof Error ? err.message : String(err)) })
      .finally(() => { if (!cancelled) setLoading(false) })
    return () => { cancelled = true }
  }, [module, hostId, refreshKey])

  const setup = async (action: 'install' | 'remove') => {
    if (!mountedRef.current) return
    setLoading(true)
    setError(null)
    try {
      const data = await module.setup(hostId, action)
      if (mountedRef.current) setStatus(data)
    } catch (err) {
      if (mountedRef.current) setError(err instanceof Error ? err.message : String(err))
    }
    if (mountedRef.current) setLoading(false)
  }

  // Subscribe to agent events for lastTrigger computation.
  // events ref changes on any hook event; useMemo keeps lastTrigger stable
  // when the computed result is unchanged for this hostId.
  const events = useAgentStore((s) => s.events)
  const lastTrigger = useMemo(
    () => module.getLastTrigger?.(hostId, events) ?? null,
    [module, hostId, events],
  )

  return { status, loading, error, setup, lastTrigger }
}
