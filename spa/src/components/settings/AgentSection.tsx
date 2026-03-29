import { useState, useEffect } from 'react'
import { useI18nStore } from '../../stores/useI18nStore'
import { useAgentStore } from '../../stores/useAgentStore'
import { useNotificationSettingsStore } from '../../stores/useNotificationSettingsStore'
import { useHostStore } from '../../stores/useHostStore'
import { SettingItem } from './SettingItem'

const KNOWN_EVENTS = ['Notification', 'PermissionRequest', 'Stop']

interface HookEventStatus {
  installed: boolean
  command: string | null
}

interface HookStatus {
  agent_type: string
  installed: boolean
  events: Record<string, HookEventStatus>
  issues: string[]
}

export function AgentSection() {
  const t = useI18nStore((s) => s.t)
  const events = useAgentStore((s) => s.events)
  const getSettings = useNotificationSettingsStore((s) => s.getSettingsForAgent)
  const setAgentEnabled = useNotificationSettingsStore((s) => s.setAgentEnabled)
  const setEventEnabled = useNotificationSettingsStore((s) => s.setEventEnabled)
  const setNotifyWithoutTab = useNotificationSettingsStore((s) => s.setNotifyWithoutTab)
  const setReopenTabOnClick = useNotificationSettingsStore((s) => s.setReopenTabOnClick)

  const getDaemonBase = useHostStore((s) => s.getDaemonBase)
  const daemonBase = getDaemonBase('local')

  const [hookStatus, setHookStatus] = useState<HookStatus | null>(null)
  const [hookLoading, setHookLoading] = useState(false)

  useEffect(() => {
    fetch(`${daemonBase}/api/agent/hook-status`)
      .then((r) => r.json())
      .then((data) => setHookStatus(data as HookStatus))
      .catch(() => setHookStatus(null))
  }, [daemonBase])

  const handleHookAction = async (action: 'install' | 'remove') => {
    setHookLoading(true)
    try {
      const res = await fetch(`${daemonBase}/api/agent/hook-setup`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ agent_type: 'cc', action }),
      })
      const data = await res.json()
      setHookStatus(data as HookStatus)
    } catch { /* ignore */ }
    setHookLoading(false)
  }

  // Collect known agent types from events
  const agentTypes = [...new Set(
    Object.values(events)
      .map((e) => e.agent_type)
      .filter((type): type is string => !!type),
  )]

  if (agentTypes.length === 0) {
    return (
      <div className="space-y-4">
        <div>
          <h3 className="text-sm font-medium text-text-primary">{t('settings.agent.title')}</h3>
          <p className="text-xs text-text-muted mt-1">{t('settings.agent.desc')}</p>
        </div>
        <p className="text-xs text-text-muted">{t('settings.agent.no_agents')}</p>
      </div>
    )
  }

  return (
    <div className="space-y-4">
      <div>
        <h3 className="text-sm font-medium text-text-primary">{t('settings.agent.title')}</h3>
        <p className="text-xs text-text-muted mt-1">{t('settings.agent.desc')}</p>
      </div>

      {agentTypes.map((agentType) => {
        const settings = getSettings(agentType)
        const label = agentType === 'cc' ? 'Claude Code' : agentType

        return (
          <div key={agentType} className="border border-border-default rounded-md p-3 space-y-3">
            <h4 className="text-xs font-medium text-text-primary">{label}</h4>

            {hookStatus && agentType === 'cc' && (
              <SettingItem
                label={t('settings.agent.hook.status')}
                description=""
              >
                <div className="flex items-center gap-2">
                  <span className={`text-xs ${hookStatus.installed ? 'text-green-400' : 'text-yellow-400'}`}>
                    {hookStatus.installed
                      ? t('settings.agent.hook.installed')
                      : t('settings.agent.hook.not_installed')}
                  </span>
                  <button
                    onClick={() => handleHookAction(hookStatus.installed ? 'remove' : 'install')}
                    disabled={hookLoading}
                    className="text-xs px-2 py-0.5 rounded border border-border-default hover:bg-surface-hover text-text-secondary"
                  >
                    {hookLoading ? '...' : hookStatus.installed
                      ? t('settings.agent.hook.remove')
                      : t('settings.agent.hook.install')}
                  </button>
                </div>
              </SettingItem>
            )}

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
