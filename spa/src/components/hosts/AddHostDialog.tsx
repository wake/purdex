import { useState } from 'react'
import { X, Plugs, ArrowsClockwise, CheckCircle, Warning } from '@phosphor-icons/react'
import { useHostStore } from '../../stores/useHostStore'
import { useI18nStore } from '../../stores/useI18nStore'

interface Props {
  onClose: () => void
}

type Stage = 'idle' | 'testing' | 'success' | 'needs-token' | 'error'

export function AddHostDialog({ onClose }: Props) {
  const t = useI18nStore((s) => s.t)
  const addHost = useHostStore((s) => s.addHost)

  const [name, setName] = useState('')
  const [ip, setIp] = useState('')
  const [port, setPort] = useState('7860')
  const [token, setToken] = useState('')
  const [stage, setStage] = useState<Stage>('idle')
  const [error, setError] = useState('')
  const [latency, setLatency] = useState<number | null>(null)

  const handleTest = async () => {
    const base = `http://${ip}:${port || '7860'}`
    setStage('testing')
    setError('')
    setLatency(null)

    const start = performance.now()
    try {
      // Step 1: health check (no auth needed)
      const healthRes = await fetch(`${base}/api/health`)
      const ms = Math.round(performance.now() - start)
      setLatency(ms)

      if (!healthRes.ok) {
        setStage('error')
        setError(`Health check failed: HTTP ${healthRes.status}`)
        return
      }

      // Step 2: try sessions to detect auth requirement
      const headers: Record<string, string> = {}
      if (token) headers['Authorization'] = `Bearer ${token}`

      const sessionsRes = await fetch(`${base}/api/sessions`, { headers })
      if (sessionsRes.ok) {
        setStage('success')
      } else if (sessionsRes.status === 401) {
        if (token) {
          setStage('error')
          setError(t('hosts.invalid_token'))
        } else {
          setStage('needs-token')
        }
      } else {
        setStage('error')
        setError(`HTTP ${sessionsRes.status}`)
      }
    } catch (err) {
      setStage('error')
      setError(err instanceof Error ? err.message : 'Connection failed')
    }
  }

  const handleSave = () => {
    addHost({
      name: name.trim() || ip,
      ip,
      port: parseInt(port, 10) || 7860,
      token: token || undefined,
    })
    onClose()
  }

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/50">
      <div className="bg-surface-primary border border-border-default rounded-lg shadow-xl w-full max-w-md">
        {/* Header */}
        <div className="flex items-center justify-between px-4 py-3 border-b border-border-subtle">
          <h2 className="text-sm font-semibold">{t('hosts.add_host')}</h2>
          <button onClick={onClose} className="text-text-muted hover:text-text-primary cursor-pointer">
            <X size={16} />
          </button>
        </div>

        {/* Body */}
        <div className="p-4 space-y-3">
          <div>
            <label className="text-xs text-text-secondary block mb-1">{t('hosts.name')} ({t('hosts.optional')})</label>
            <input
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder="My Server"
              className="w-full bg-surface-secondary border border-border-default rounded px-3 py-2 text-sm text-text-primary"
            />
          </div>

          <div className="grid grid-cols-3 gap-2">
            <div className="col-span-2">
              <label className="text-xs text-text-secondary block mb-1">{t('hosts.ip')} *</label>
              <input
                value={ip}
                onChange={(e) => { setIp(e.target.value); if (stage === 'success') setStage('idle') }}
                placeholder="100.64.0.1"
                className="w-full bg-surface-secondary border border-border-default rounded px-3 py-2 text-sm text-text-primary font-mono"
              />
            </div>
            <div>
              <label className="text-xs text-text-secondary block mb-1">{t('hosts.port')}</label>
              <input
                value={port}
                onChange={(e) => { setPort(e.target.value); if (stage === 'success') setStage('idle') }}
                placeholder="7860"
                className="w-full bg-surface-secondary border border-border-default rounded px-3 py-2 text-sm text-text-primary font-mono"
              />
            </div>
          </div>

          {/* Token field — shown after 401 or always if user wants to provide it */}
          {(stage === 'needs-token' || token) && (
            <div>
              <label className="text-xs text-text-secondary block mb-1">{t('hosts.token')} *</label>
              <input
                value={token}
                onChange={(e) => setToken(e.target.value)}
                placeholder="tbox_..."
                type="password"
                className="w-full bg-surface-secondary border border-border-default rounded px-3 py-2 text-sm text-text-primary font-mono"
                autoFocus={stage === 'needs-token'}
              />
              <p className="text-xs text-text-muted mt-1">{t('hosts.token_hint')}</p>
            </div>
          )}

          {/* Status feedback */}
          {stage === 'testing' && (
            <div className="flex items-center gap-2 text-xs text-text-muted">
              <ArrowsClockwise size={14} className="animate-spin" />
              {t('hosts.testing')}
            </div>
          )}
          {stage === 'success' && (
            <div className="flex items-center gap-2 text-xs text-green-400">
              <CheckCircle size={14} />
              {t('hosts.connected')}{latency != null && ` (${latency}ms)`}
            </div>
          )}
          {stage === 'needs-token' && !error && (
            <div className="flex items-center gap-2 text-xs text-yellow-400">
              <Warning size={14} />
              {t('hosts.requires_token')}
            </div>
          )}
          {stage === 'error' && (
            <div className="flex items-center gap-2 text-xs text-red-400">
              <Warning size={14} />
              {error}
            </div>
          )}
        </div>

        {/* Footer */}
        <div className="flex items-center justify-end gap-2 px-4 py-3 border-t border-border-subtle">
          <button
            onClick={onClose}
            className="px-4 py-2 rounded text-xs text-text-secondary hover:text-text-primary cursor-pointer"
          >
            {t('common.cancel')}
          </button>
          {stage === 'success' ? (
            <button
              onClick={handleSave}
              className="px-4 py-2 rounded text-xs bg-accent text-white cursor-pointer"
            >
              {t('hosts.save')}
            </button>
          ) : (
            <button
              onClick={handleTest}
              disabled={!ip || stage === 'testing'}
              className="flex items-center gap-1.5 px-4 py-2 rounded text-xs bg-accent text-white cursor-pointer disabled:opacity-50"
            >
              <Plugs size={14} />
              {t('hosts.test_connection')}
            </button>
          )}
        </div>
      </div>
    </div>
  )
}
