import { useModuleEnabledStore } from '../../stores/useModuleEnabledStore'
import { useI18nStore } from '../../stores/useI18nStore'

interface Props {
  moduleId: string
  paneKind: string
}

/**
 * Generic fallback rendered by PaneLayoutRenderer when a pane belongs to a
 * disabled module and the module hasn't declared a custom `disabledComponent`.
 * Shows the module/pane identity, a one-click enable affordance, and a hint
 * that a reload is required to re-instantiate the underlying pane component.
 */
export function DisabledModulePlaceholder({ moduleId, paneKind }: Props) {
  const t = useI18nStore((s) => s.t)
  const handleEnable = () => {
    useModuleEnabledStore.getState().setEnabled(moduleId, true)
  }
  return (
    <div className="flex h-full flex-col items-center justify-center gap-3 p-6 text-sm text-text-secondary">
      <h3 className="text-base text-text-primary">
        {t('module.disabled.title', { module: moduleId })}
      </h3>
      <p>{t('module.disabled.body', { paneKind })}</p>
      <button
        type="button"
        onClick={handleEnable}
        className="rounded bg-accent px-3 py-1 text-text-primary hover:opacity-90"
        aria-label={t('module.disabled.enable_aria', { module: moduleId })}
      >
        {t('module.disabled.enable')}
      </button>
      <p className="text-xs text-text-muted">{t('module.disabled.reload_hint')}</p>
    </div>
  )
}
