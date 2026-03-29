import { useI18nStore } from '../../stores/useI18nStore'

export function AgentSection() {
  const t = useI18nStore((s) => s.t)

  return (
    <div>
      <h2 className="text-lg text-text-primary">{t('settings.agent.title')}</h2>
      <p className="text-xs text-text-secondary mb-6">{t('settings.agent.desc')}</p>

      <p className="text-xs text-text-secondary border border-border-subtle rounded px-3 py-2">
        {t('settings.agent.host_note')}
      </p>
    </div>
  )
}
