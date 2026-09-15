// spa/src/components/settings/device-state/DeviceStateSection.tsx — P1 device
// state backup block (spec §3.7): this computer's name, where its state is
// saved, and the uploader's status line.
import { useState } from 'react'
import { useI18nStore } from '../../../stores/useI18nStore'
import { effectiveDeviceName, useDeviceStateStore } from '../../../stores/useDeviceStateStore'
import type { DeviceStateStatus } from '../../../stores/useDeviceStateStore'
import { selectDevHostId, useHostStore } from '../../../stores/useHostStore'
import { SettingItem } from '../SettingItem'

type T = ReturnType<typeof useI18nStore.getState>['t']

const STATUS_COLOR: Record<DeviceStateStatus['kind'], string> = {
  idle: 'text-text-muted',
  'no-target': 'text-text-muted',
  offline: 'text-yellow-500',
  uploading: 'text-text-secondary',
  ok: 'text-green-500',
  error: 'text-red-500',
}

function statusMessage(t: T, status: DeviceStateStatus): string {
  switch (status.kind) {
    case 'idle':
      return t('settings.device_state.status.idle')
    case 'uploading':
      return t('settings.device_state.status.uploading')
    case 'ok':
      return t('settings.device_state.status.ok', {
        time: new Date(status.at ?? Date.now()).toLocaleTimeString(),
      })
    case 'offline':
      return t('settings.device_state.status.offline')
    case 'no-target':
      return t('settings.device_state.status.no_target')
    case 'error':
      return t('settings.device_state.status.error', { message: status.message ?? '' })
  }
}

export function DeviceStateSection() {
  const t = useI18nStore((s) => s.t)
  const deviceName = useDeviceStateStore((s) => s.deviceName)
  const defaultDeviceName = useDeviceStateStore((s) => s.defaultDeviceName)
  const status = useDeviceStateStore((s) => s.status)
  const setDeviceName = useDeviceStateStore((s) => s.setDeviceName)
  const targetId = useHostStore(selectDevHostId)
  const targetName = useHostStore((s) => (targetId ? s.hosts[targetId]?.name : undefined))

  const current = effectiveDeviceName({ deviceName, defaultDeviceName })
  const [draft, setDraft] = useState(current)
  const [synced, setSynced] = useState(current)

  // Re-sync the draft when the stored name changes (render-phase adjust, no effect).
  if (synced !== current) {
    setSynced(current)
    setDraft(current)
  }

  const commit = () => {
    setDeviceName(draft)
    // The store normalizes (trim / blank → default); show what was actually kept.
    const next = effectiveDeviceName(useDeviceStateStore.getState())
    setSynced(next)
    setDraft(next)
  }

  return (
    <div data-testid="device-state-section" className="mt-8">
      <h3 className="text-sm text-text-primary">{t('settings.device_state.title')}</h3>
      <p className="text-xs text-text-secondary">{t('settings.device_state.desc')}</p>

      <SettingItem label={t('settings.device_state.device_name')}>
        <div className="flex items-center gap-2">
          <input
            type="text"
            aria-label={t('settings.device_state.device_name_aria')}
            data-testid="device-state-name"
            placeholder={defaultDeviceName}
            spellCheck={false}
            value={draft}
            onChange={(e) => setDraft(e.target.value)}
            onBlur={commit}
            onKeyDown={(e) => {
              if (e.key !== 'Enter' || e.nativeEvent.isComposing) return
              commit()
            }}
            className="bg-surface-secondary border border-border-default rounded px-2 py-1 text-xs text-text-primary w-48"
          />
          {deviceName !== null && (
            <button
              type="button"
              data-testid="device-state-name-reset"
              onClick={() => setDeviceName(null)}
              className="px-2.5 py-1 rounded-md border border-border-default text-text-secondary text-xs hover:text-text-primary hover:border-border-active"
            >
              {t('settings.device_state.device_name_reset')}
            </button>
          )}
        </div>
      </SettingItem>

      <SettingItem label={t('settings.device_state.target')}>
        <span
          data-testid="device-state-target"
          data-target={targetId ?? 'none'}
          className={`text-xs ${targetId ? 'text-text-primary' : 'text-text-muted'}`}
        >
          {targetId ? (targetName ?? targetId) : t('settings.device_state.target_none')}
        </span>
      </SettingItem>

      <p
        data-testid="device-state-status"
        data-kind={status.kind}
        className={`mt-1 text-xs ${STATUS_COLOR[status.kind]}`}
      >
        {statusMessage(t, status)}
      </p>
    </div>
  )
}
