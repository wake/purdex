import { useState, useEffect } from 'react'
import { useAgentStore } from '../stores/useAgentStore'
import { fetchAgentHookStatus, setupAgentHook } from '../lib/host-api'

interface HookEventStatus {
  installed: boolean
  command: string | null
}

export interface HookStatus {
  agent_type: string
  installed: boolean
  events: Record<string, HookEventStatus>
  issues: string[]
}

export function useHookStatus(hostId = 'local') {
  const [hookStatus, setHookStatus] = useState<HookStatus | null>(null)
  const [hookLoading, setHookLoading] = useState(false)

  useEffect(() => {
    fetchAgentHookStatus(hostId)
      .then((r) => r.json())
      .then((data) => {
        const status = data as HookStatus
        setHookStatus(status)
        useAgentStore.getState().setHooksInstalled(!!status.installed)
      })
      .catch(() => setHookStatus(null))
  }, [hostId])

  const runAction = async (action: 'install' | 'remove') => {
    setHookLoading(true)
    try {
      const res = await setupAgentHook(hostId, 'cc', action)
      if (!res.ok) {
        setHookLoading(false)
        return
      }
      const data = await res.json() as HookStatus
      setHookStatus(data)
      useAgentStore.getState().setHooksInstalled(!!data.installed)
    } catch { /* ignore */ }
    setHookLoading(false)
  }

  return { hookStatus, hookLoading, runAction }
}
