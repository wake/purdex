import { useState, useEffect } from 'react'
import { CheckCircle, XCircle, ArrowClockwise } from '@phosphor-icons/react'
import { hostFetch } from '../../lib/host-api'
import { useHostStore } from '../../stores/useHostStore'
import { useI18nStore } from '../../stores/useI18nStore'
import { AGENT_NAMES } from '../../lib/agent-metadata'

interface TitleCapability {
  state: string
  note: string
}

interface AgentInfo {
  installed: boolean
  path?: string
  version?: string
  dynamic_title?: TitleCapability
}

type DetectResult = Record<string, AgentInfo>

interface TitleStatus {
  allow_set_title: boolean
  installed: boolean
  runtime_applied: boolean
  managed_config_path: string
  error: string
}

interface Props {
  hostId: string
}

export function AgentsSection({ hostId }: Props) {
  const t = useI18nStore((s) => s.t)
  const runtime = useHostStore((s) => s.runtime[hostId])
  const isOffline = !runtime || runtime.status !== 'connected'
  const [result, setResult] = useState<DetectResult | null>(null)
  const [titleStatus, setTitleStatus] = useState<TitleStatus | null>(null)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')

  const detect = async () => {
    setLoading(true)
    setError('')
    setTitleStatus(null)
    try {
      const [detectRes, titleRes] = await Promise.all([
        hostFetch(hostId, '/api/agents/detect'),
        hostFetch(hostId, '/api/agent/title/status'),
      ])
      if (!detectRes.ok) throw new Error(`${detectRes.status}`)
      setResult(await detectRes.json())
      if (titleRes.ok) {
        setTitleStatus(await titleRes.json())
      } else {
        setTitleStatus({ allow_set_title: false, installed: false, runtime_applied: false, managed_config_path: '', error: `${titleRes.status}` })
      }
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed')
    } finally {
      setLoading(false)
    }
  }

  const setupTitleIntegration = async (action: 'install' | 'remove') => {
    setLoading(true)
    setError('')
    try {
      const res = await hostFetch(hostId, '/api/agent/title/setup', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ action }),
      })
      if (!res.ok) throw new Error(`${res.status}`)
      setTitleStatus(await res.json())
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
          <div data-testid="agent-title-block" className="border border-border-subtle rounded-lg p-4 bg-surface-secondary/30">
            <div className="flex items-center justify-between gap-3">
              <div>
                <h3 className="text-sm font-medium text-text-primary">{t('hosts.agent_title.title')}</h3>
                <p className="text-xs text-text-muted mt-1">{t('hosts.agent_title.desc')}</p>
              </div>
              <span className={`text-xs ${titleStatus?.installed ? 'text-green-400' : 'text-text-muted'}`}>
                {titleStatus?.installed ? t('hosts.installed') : t('hosts.not_installed')}
              </span>
            </div>
            <div className="mt-3 text-xs text-text-muted space-y-1">
              <div>
                <span className="text-text-secondary">allow-set-title</span>: {' '}
                {titleStatus?.allow_set_title ? t('hosts.agent_title.enabled') : t('hosts.agent_title.not_enabled')}
              </div>
              <div>
                <span className="text-text-secondary">{t('hosts.agent_title.runtime')}:</span>{' '}
                {titleStatus?.runtime_applied ? t('hosts.agent_title.applied') : t('hosts.agent_title.not_applied')}
              </div>
              {titleStatus?.managed_config_path && (
                <div><span className="text-text-secondary">{t('hosts.agent_title.config')}:</span> <code className="font-mono">{titleStatus.managed_config_path}</code></div>
              )}
              {titleStatus?.error && <div className="text-red-400">{titleStatus.error}</div>}
            </div>
            <div className="mt-3 flex items-center gap-2">
              {titleStatus?.installed ? (
                <button
                  type="button"
                  onClick={() => setupTitleIntegration('remove')}
                  disabled={loading || isOffline}
                  className="px-3 py-1.5 rounded text-xs bg-surface-secondary hover:bg-surface-tertiary text-text-secondary cursor-pointer disabled:opacity-50"
                >
                  {t('hosts.remove')}
                </button>
              ) : (
                <button
                  type="button"
                  onClick={() => setupTitleIntegration('install')}
                  disabled={loading || isOffline}
                  className="px-3 py-1.5 rounded text-xs bg-accent/20 hover:bg-accent/30 text-accent cursor-pointer disabled:opacity-50"
                >
                  {t('hosts.install')}
                </button>
              )}
            </div>
          </div>
          {Object.entries(result).map(([agentType, info]) => (
            <div key={agentType} data-testid={`agent-card-${agentType}`} className="border border-border-subtle rounded-lg p-4">
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
                  <div className="pt-2 mt-2 border-t border-border-subtle">
                    <span className="text-text-secondary">{t('hosts.agent_title.capability_label')}</span>: {' '}
                    {capabilityFor(agentType, info.dynamic_title).note}
                  </div>
                </div>
              )}
            </div>
          ))}
        </div>
      )}
    </div>
  )
}

function capabilityFor(agentType: string, capability?: TitleCapability): TitleCapability {
  if (capability?.note) return capability
  switch (agentType) {
    case 'cc':
      return { state: 'enabled', note: 'Claude terminal titles are likely enabled; session-local environment overrides may differ.' }
    case 'codex':
      return { state: 'missing', note: 'Codex terminal title uses its default behavior; no config file was found.' }
    case 'opencode':
      return { state: 'unknown', note: 'OpenCode has no documented persistent title toggle.' }
    default:
      return { state: 'unknown', note: 'Unknown' }
  }
}
