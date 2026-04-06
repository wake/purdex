import { useState, useEffect } from 'react'
import type { HookModule, HookModuleStatus } from '../lib/hook-modules'

export function useModuleHook(module: HookModule, hostId: string, refreshKey: number) {
  const [status, setStatus] = useState<HookModuleStatus | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

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
    setLoading(true)
    setError(null)
    try {
      const data = await module.setup(hostId, action)
      setStatus(data)
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    }
    setLoading(false)
  }

  const lastTrigger = module.getLastTrigger?.(hostId) ?? null

  return { status, loading, error, setup, lastTrigger }
}
