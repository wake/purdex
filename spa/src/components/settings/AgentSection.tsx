import { useState, useEffect } from 'react'
import { useI18nStore } from '../../stores/useI18nStore'
import { useHostStore } from '../../stores/useHostStore'
import { AGENT_NAMES } from '../../lib/agent-icons'

const AGENTS = ['cc', 'codex'] as const

interface HookStatus {
  installed: boolean
  events: Record<string, { installed: boolean; command: string }>
  issues: string[]
}

export function AgentSection() {
  const t = useI18nStore((s) => s.t)

  return (
    <div>
      <h2 className="text-lg text-text-primary">{t('settings.agent.title')}</h2>
      <p className="text-xs text-text-secondary mb-6">{t('settings.agent.desc')}</p>

      <div className="space-y-4">
        {AGENTS.map((agentType) => (
          <HookToggle key={agentType} agentType={agentType} />
        ))}
      </div>
    </div>
  )
}

function HookToggle({ agentType }: { agentType: string }) {
  const [status, setStatus] = useState<HookStatus | null>(null)
  const [loading, setLoading] = useState(false)
  const activeHostId = useHostStore((s) => s.activeHostId ?? s.hostOrder[0])
  const getDaemonBase = useHostStore((s) => s.getDaemonBase)

  useEffect(() => {
    let cancelled = false
    ;(async () => {
      if (!activeHostId) return
      try {
        const base = getDaemonBase(activeHostId)
        const headers: Record<string, string> = { 'Content-Type': 'application/json' }
        const token = useHostStore.getState().hosts[activeHostId]?.token
        if (token) headers['Authorization'] = `Bearer ${token}`
        const res = await fetch(`${base}/api/hooks/${agentType}/status`, { headers })
        if (res.ok && !cancelled) setStatus(await res.json())
      } catch { /* ignore */ }
    })()
    return () => { cancelled = true }
  }, [activeHostId, agentType, getDaemonBase])

  const handleAction = async (action: 'install' | 'remove') => {
    if (!activeHostId) return
    setLoading(true)
    try {
      const base = getDaemonBase(activeHostId)
      const headers: Record<string, string> = { 'Content-Type': 'application/json' }
      const token = useHostStore.getState().hosts[activeHostId]?.token
      if (token) headers['Authorization'] = `Bearer ${token}`
      const res = await fetch(`${base}/api/hooks/${agentType}/setup`, {
        method: 'POST',
        headers,
        body: JSON.stringify({ action }),
      })
      if (res.ok) setStatus(await res.json())
    } catch { /* ignore */ }
    setLoading(false)
  }

  const name = AGENT_NAMES[agentType] ?? agentType
  const installed = status?.installed ?? false

  return (
    <div className="border border-border-subtle rounded p-3">
      <div className="flex items-center justify-between">
        <div>
          <span className="text-sm text-text-primary font-medium">{name}</span>
          <span className={`ml-2 text-xs ${installed ? 'text-green-400' : 'text-text-muted'}`}>
            {installed ? '● Installed' : '○ Not installed'}
          </span>
        </div>
        <button
          className="text-xs px-3 py-1 rounded bg-surface-secondary hover:bg-surface-tertiary text-text-secondary disabled:opacity-50"
          disabled={loading}
          onClick={() => handleAction(installed ? 'remove' : 'install')}
        >
          {loading ? '...' : installed ? 'Remove' : 'Install'}
        </button>
      </div>
      {status?.issues && status.issues.length > 0 && (
        <div className="mt-2 text-xs text-yellow-400">
          {status.issues.map((issue, i) => <div key={i}>⚠ {issue}</div>)}
        </div>
      )}
    </div>
  )
}
