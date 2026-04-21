import { useI18nStore } from '../../../../stores/useI18nStore'

export interface HistoryTabsProps {
  active: 'local' | 'remote'
  remoteAvailable: boolean
  onChange: (tab: 'local' | 'remote') => void
}

export function HistoryTabs(props: HistoryTabsProps) {
  const t = useI18nStore((s) => s.t)
  return (
    <div role="tablist" className="inline-flex rounded-md border border-border-default bg-surface-secondary p-1">
      <button
        role="tab"
        aria-selected={props.active === 'local'}
        onClick={() => props.onChange('local')}
        className={[
          'rounded-md px-4 py-1.5 text-xs font-medium transition-colors',
          props.active === 'local'
            ? 'bg-surface-elevated text-text-primary'
            : 'text-text-muted hover:text-text-primary',
        ].join(' ')}
      >
        {t('settings.sync.history.tabs.local')}
      </button>
      <button
        role="tab"
        aria-selected={props.active === 'remote'}
        disabled={!props.remoteAvailable}
        onClick={() => props.onChange('remote')}
        title={!props.remoteAvailable ? t('settings.sync.history.tabs.remoteDaemonOnly') : undefined}
        className={[
          'rounded-md px-4 py-1.5 text-xs font-medium transition-colors',
          props.active === 'remote'
            ? 'bg-surface-elevated text-text-primary'
            : 'text-text-muted hover:text-text-primary',
          !props.remoteAvailable ? 'cursor-not-allowed opacity-50' : '',
        ].join(' ')}
      >
        {t('settings.sync.history.tabs.remote')}
      </button>
    </div>
  )
}
