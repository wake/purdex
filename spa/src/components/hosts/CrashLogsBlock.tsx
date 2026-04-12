import { useCallback, useEffect, useState } from 'react'
import { ArrowsClockwise } from '@phosphor-icons/react'
import { useHostStore } from '../../stores/useHostStore'
import { useI18nStore } from '../../stores/useI18nStore'
import { hostFetch } from '../../lib/host-api'

interface Props {
  hostId: string
}

export function CrashLogsBlock({ hostId }: Props) {
  const t = useI18nStore((s) => s.t)
  const runtime = useHostStore((s) => s.runtime[hostId])
  const [content, setContent] = useState<string | null>(null)
  const [loading, setLoading] = useState(false)
  const isOffline = runtime?.status !== 'connected'

  const fetchCrash = useCallback(async () => {
    if (isOffline) return
    setLoading(true)
    try {
      const res = await hostFetch(hostId, '/api/logs/crash')
      if (res.status === 204) {
        setContent(null)
      } else if (res.ok) {
        const text = await res.text()
        setContent(text || null)
      }
    } catch {
      /* ignore */
    } finally {
      setLoading(false)
    }
  }, [hostId, isOffline])

  useEffect(() => {
    fetchCrash()
  }, [fetchCrash])

  return (
    <div>
      <div className="flex items-center justify-between mb-2">
        <h3 className="text-sm font-semibold text-text-primary">{t('hosts.logs_crash')}</h3>
        <button
          onClick={fetchCrash}
          disabled={loading || isOffline}
          className="flex items-center gap-1 px-2 py-1 rounded text-xs bg-surface-secondary hover:bg-surface-tertiary border border-border-default text-text-secondary cursor-pointer disabled:opacity-50"
        >
          <ArrowsClockwise size={12} className={loading ? 'animate-spin' : ''} />
          {t('hosts.logs_refresh')}
        </button>
      </div>
      {isOffline ? (
        <p className="text-xs text-text-muted">{t('hosts.logs_offline')}</p>
      ) : content ? (
        <pre className="text-xs font-mono bg-red-500/5 border border-red-500/20 rounded p-3 overflow-auto max-h-96 whitespace-pre-wrap text-text-secondary">
          {content}
        </pre>
      ) : (
        <p className="text-xs text-text-muted">{t('hosts.logs_no_crash')}</p>
      )}
    </div>
  )
}
