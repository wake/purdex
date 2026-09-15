// spa/src/components/hosts/nex/NexHostSection.tsx — Host → "Nex" sub-page
// (spec §4.4.3). Composes NexEngineStatus, NexConfigForm and
// NexExecutionsTable over the data useNexHostData loads for the host.
import { useI18nStore } from '../../../stores/useI18nStore'
import NexEngineStatus from './NexEngineStatus'
import NexConfigForm from './NexConfigForm'
import NexExecutionsTable from './NexExecutionsTable'
import { isNexReady } from './nex-ready'
import { useNexHostData } from './useNexHostData'

interface Props {
  hostId: string
}

export function NexHostSection({ hostId }: Props) {
  const t = useI18nStore((s) => s.t)
  const { phase, info, config, refreshError, retry, refresh, onConfigSaved } = useNexHostData(hostId)

  if (phase === 'offline') {
    return (
      <div className="max-w-2xl">
        <p className="text-xs text-text-muted">{t('hosts.load_failed')}</p>
      </div>
    )
  }

  if (phase === 'failed') {
    return (
      <div className="max-w-2xl">
        <p className="text-xs text-text-muted">{t('hosts.load_failed')}</p>
        <button
          type="button"
          data-testid="nex-retry"
          onClick={retry}
          className="mt-2 px-3 py-1.5 rounded-md bg-surface-secondary hover:bg-surface-tertiary border border-border-default text-xs text-text-secondary cursor-pointer"
        >
          {t('hosts.nex.retry')}
        </button>
      </div>
    )
  }

  if (phase === 'loading') {
    return (
      <div className="max-w-2xl">
        <p className="text-xs text-text-muted">{t('hosts.loading')}</p>
      </div>
    )
  }

  return (
    <div className="max-w-2xl space-y-6">
      {refreshError && (
        <p data-testid="nex-refresh-error" className="text-xs text-red-400">{t('hosts.load_failed')}</p>
      )}
      <NexEngineStatus hostId={hostId} info={info} onRefresh={refresh} />
      <NexConfigForm hostId={hostId} config={config} info={info} onSaved={onConfigSaved} />
      <NexExecutionsTable hostId={hostId} enabled={isNexReady(info)} />
    </div>
  )
}
