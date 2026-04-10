import { useState, useEffect } from 'react'
import { CheckCircle, XCircle, ArrowClockwise } from '@phosphor-icons/react'
import { hostFetch } from '../../lib/host-api'
import { useHostStore } from '../../stores/useHostStore'
import { useI18nStore } from '../../stores/useI18nStore'
import { AGENT_NAMES } from '../../lib/agent-icons'

interface AgentInfo {
  installed: boolean
  path?: string
  version?: string
}

type DetectResult = Record<string, AgentInfo>

interface Props {
  hostId: string
}

export function AgentsSection({ hostId }: Props) {
  const t = useI18nStore((s) => s.t)
  const runtime = useHostStore((s) => s.runtime[hostId])
  const isOffline = !runtime || runtime.status !== 'connected'
  const [result, setResult] = useState<DetectResult | null>(null)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')

  const detect = async () => {
    setLoading(true)
    setError('')
    try {
      const res = await hostFetch(hostId, '/api/agents/detect')
      if (!res.ok) throw new Error(`${res.status}`)
      setResult(await res.json())
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed')
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    if (!isOffline) detect()
  }, [hostId, isOffline]) // eslint-disable-line react-hooks/exhaustive-deps

  return (
    <div className="max-w-2xl">
      <div className="flex items-center justify-between mb-4">
        <div>
          <h2 className="text-lg font-semibold">{t('hosts.agents')}</h2>
          <p className="text-xs text-text-muted mt-1">{t('hosts.agents_desc')}</p>
        </div>
        <button
          onClick={detect}
          disabled={isOffline || loading}
          className="flex items-center gap-1.5 px-3 py-1.5 rounded text-xs bg-surface-secondary hover:bg-surface-tertiary text-text-secondary cursor-pointer disabled:opacity-50"
        >
          <ArrowClockwise size={14} className={loading ? 'animate-spin' : ''} />
          {t('hosts.check_status')}
        </button>
      </div>

      {error && <p className="text-xs text-red-400 mb-4">{error}</p>}

      {loading && !result && (
        <p className="text-sm text-text-muted">{t('hosts.checking')}</p>
      )}

      {result && (
        <div className="space-y-3">
          {Object.entries(result).map(([agentType, info]) => (
            <div key={agentType} className="border border-border-subtle rounded-lg p-4">
              <div className="flex items-center justify-between">
                <div className="flex items-center gap-2">
                  {info.installed ? (
                    <CheckCircle size={18} weight="fill" className="text-green-400" />
                  ) : (
                    <XCircle size={18} weight="fill" className="text-text-muted" />
                  )}
                  <span className="text-sm font-medium text-text-primary">
                    {AGENT_NAMES[agentType] ?? agentType}
                  </span>
                </div>
                <span className={`text-xs ${info.installed ? 'text-green-400' : 'text-text-muted'}`}>
                  {info.installed ? t('hosts.agent_installed') : t('hosts.agent_not_found')}
                </span>
              </div>
              {info.installed && (
                <div className="mt-2 text-xs text-text-muted space-y-1">
                  {info.version && (
                    <div><span className="text-text-secondary">{t('hosts.agent_version')}:</span> {info.version}</div>
                  )}
                  {info.path && (
                    <div><span className="text-text-secondary">{t('hosts.agent_path')}:</span> <code className="font-mono">{info.path}</code></div>
                  )}
                </div>
              )}
            </div>
          ))}
        </div>
      )}
    </div>
  )
}
