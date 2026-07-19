import { useState } from 'react'
import { Bell, X } from '@phosphor-icons/react'
import { getPlatformCapabilities } from '../lib/platform'
import {
  webNotificationPermission, requestWebNotificationPermission,
  shouldOfferWebNotifications, getWebNotifyDismissed, setWebNotifyDismissed,
} from '../lib/web-notifications'
import { useI18nStore } from '../stores/useI18nStore'

export function WebNotificationPrompt() {
  const t = useI18nStore((s) => s.t)
  const [dismissed, setDismissed] = useState(getWebNotifyDismissed)
  const [permission, setPermission] = useState(webNotificationPermission)

  if (!shouldOfferWebNotifications({
    isElectron: getPlatformCapabilities().isElectron, permission, dismissed,
  })) return null

  const enable = async () => setPermission(await requestWebNotificationPermission())
  const dismiss = () => { setWebNotifyDismissed(); setDismissed(true) }

  return (
    <div className="fixed bottom-3 right-3 z-50 max-w-xs rounded border border-border-default bg-surface-primary shadow-lg px-3 py-2 text-sm flex items-start gap-2">
      <Bell size={16} className="mt-0.5 shrink-0" />
      <div className="flex-1">
        <div className="mb-1">{t('notifications.enable_prompt')}</div>
        <button className="px-2 py-1 rounded bg-accent text-white text-xs" onClick={enable}>{t('notifications.enable')}</button>
      </div>
      <button aria-label={t('common.dismiss')} onClick={dismiss} className="shrink-0 text-text-muted hover:text-text-primary"><X size={14} /></button>
    </div>
  )
}
