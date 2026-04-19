import { useI18nStore } from '../../../../stores/useI18nStore'

export interface HistoryTabsProps {
  active: 'local' | 'remote'
  remoteAvailable: boolean
  onChange: (tab: 'local' | 'remote') => void
}

export function HistoryTabs(props: HistoryTabsProps) {
  const t = useI18nStore((s) => s.t)
  return (
    <div role="tablist" className="flex border-b border-text-subtle/20">
      <button
        role="tab"
        aria-selected={props.active === 'local'}
        onClick={() => props.onChange('local')}
        className={[
          'px-4 py-2 text-sm',
          props.active === 'local' ? 'border-b-2 border-accent-base text-accent-base' : 'text-text-muted',
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
          'px-4 py-2 text-sm',
          props.active === 'remote' ? 'border-b-2 border-accent-base text-accent-base' : 'text-text-muted',
          !props.remoteAvailable ? 'opacity-50 cursor-not-allowed' : '',
        ].join(' ')}
      >
        {t('settings.sync.history.tabs.remote')}
      </button>
    </div>
  )
}
