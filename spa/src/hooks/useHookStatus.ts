import { useState, useEffect } from 'react'
import { useHostStore } from '../stores/useHostStore'

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

export function useHookStatus() {
  const getDaemonBase = useHostStore((s) => s.getDaemonBase)
  const daemonBase = getDaemonBase('local')

  const [hookStatus, setHookStatus] = useState<HookStatus | null>(null)
  const [hookLoading, setHookLoading] = useState(false)

  useEffect(() => {
    fetch(`${daemonBase}/api/agent/hook-status`)
      .then((r) => r.json())
      .then((data) => setHookStatus(data as HookStatus))
      .catch(() => setHookStatus(null))
  }, [daemonBase])

  const runAction = async (action: 'install' | 'remove') => {
    setHookLoading(true)
    try {
      const res = await fetch(`${daemonBase}/api/agent/hook-setup`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ agent_type: 'cc', action }),
      })
      if (!res.ok) {
        setHookLoading(false)
        return
      }
      const data = await res.json()
      setHookStatus(data as HookStatus)
    } catch { /* ignore */ }
    setHookLoading(false)
  }

  return { hookStatus, hookLoading, runAction }
}
