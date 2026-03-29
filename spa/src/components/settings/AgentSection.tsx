import { useI18nStore } from '../../stores/useI18nStore'
import { useAgentStore } from '../../stores/useAgentStore'
import { useNotificationSettingsStore } from '../../stores/useNotificationSettingsStore'
import { SettingItem } from './SettingItem'

const KNOWN_EVENTS = ['Notification', 'PermissionRequest', 'Stop']

export function AgentSection() {
  const t = useI18nStore((s) => s.t)
  const events = useAgentStore((s) => s.events)
  const getSettings = useNotificationSettingsStore((s) => s.getSettingsForAgent)
  const setAgentEnabled = useNotificationSettingsStore((s) => s.setAgentEnabled)
  const setEventEnabled = useNotificationSettingsStore((s) => s.setEventEnabled)
  const setNotifyWithoutTab = useNotificationSettingsStore((s) => s.setNotifyWithoutTab)
  const setReopenTabOnClick = useNotificationSettingsStore((s) => s.setReopenTabOnClick)

  // Always include 'cc' as default, plus any other agent types from events
  const agentTypes = [...new Set([
    'cc',
    ...Object.values(events)
      .map((e) => e.agent_type)
      .filter((type): type is string => !!type),
  ])]

  return (
    <div className="space-y-4">
      <div>
        <h3 className="text-sm font-medium text-text-primary">{t('settings.agent.title')}</h3>
        <p className="text-xs text-text-muted mt-1">{t('settings.agent.desc')}</p>
      </div>

      <p className="text-xs text-text-muted border border-border-subtle rounded px-3 py-2">
        {t('settings.agent.host_note')}
      </p>

      {agentTypes.map((agentType) => {
        const settings = getSettings(agentType)
        const label = agentType === 'cc' ? t('settings.agent.cc.name') : agentType

        return (
          <div key={agentType} className="border border-border-default rounded-md p-3 space-y-3">
            <h4 className="text-xs font-medium text-text-primary">{label}</h4>

            <SettingItem
              label={t('settings.agent.notifications.enabled')}
              description={t('settings.agent.notifications.enabled_desc')}
            >
              <input
                type="checkbox"
                checked={settings.enabled}
                onChange={(e) => setAgentEnabled(agentType, e.target.checked)}
                className="rounded"
              />
            </SettingItem>

            {settings.enabled && KNOWN_EVENTS.map((eventName) => (
              <SettingItem
                key={eventName}
                label={t(`settings.agent.event.${eventName}`)}
                description=""
              >
                <input
                  type="checkbox"
                  checked={settings.events[eventName] !== false}
                  onChange={(e) => setEventEnabled(agentType, eventName, e.target.checked)}
                  className="rounded"
                />
              </SettingItem>
            ))}

            {settings.enabled && (
              <SettingItem
                label={t('settings.agent.notifications.notify_without_tab')}
                description={t('settings.agent.notifications.notify_without_tab_desc')}
              >
                <input
                  type="checkbox"
                  checked={settings.notifyWithoutTab}
                  onChange={(e) => setNotifyWithoutTab(agentType, e.target.checked)}
                  className="rounded"
                />
              </SettingItem>
            )}

            {settings.enabled && settings.notifyWithoutTab && (
              <SettingItem
                label={t('settings.agent.notifications.reopen_tab')}
                description={t('settings.agent.notifications.reopen_tab_desc')}
              >
                <input
                  type="checkbox"
                  checked={settings.reopenTabOnClick}
                  onChange={(e) => setReopenTabOnClick(agentType, e.target.checked)}
                  className="rounded"
                />
              </SettingItem>
            )}
          </div>
        )
      })}
    </div>
  )
}
