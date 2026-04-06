import { useState } from 'react'
import { ArrowsClockwise } from '@phosphor-icons/react'
import { useHostStore } from '../../stores/useHostStore'
import { useI18nStore } from '../../stores/useI18nStore'
import { HOOK_MODULES } from '../../lib/hook-modules'
import { HookModuleCard } from './HookModuleCard'

interface Props {
  hostId: string
}

export function HooksSection({ hostId }: Props) {
  const t = useI18nStore((s) => s.t)
  const host = useHostStore((s) => s.hosts[hostId])
  const isOffline = useHostStore((s) => {
    const rt = s.runtime[hostId]
    return rt != null && rt.status !== 'connected'
  })
  const [refreshKey, setRefreshKey] = useState(0)

  if (!host) return null

  return (
    <div className="max-w-2xl">
      <div className="flex items-center justify-between mb-4">
        <h2 className="text-lg font-semibold">{t('hosts.hooks')}</h2>
        <button
          onClick={() => setRefreshKey((k) => k + 1)}
          disabled={isOffline}
          className="flex items-center gap-1.5 px-3 py-1.5 rounded text-xs bg-surface-secondary hover:bg-surface-tertiary border border-border-default text-text-secondary cursor-pointer disabled:opacity-50"
        >
          <ArrowsClockwise size={14} />
          {t('hosts.check_status')}
        </button>
      </div>

      <div className="space-y-4">
        {HOOK_MODULES.map((mod) => (
          <HookModuleCard key={mod.id} module={mod} hostId={hostId} refreshKey={refreshKey} />
        ))}
      </div>
    </div>
  )
}
