// spa/src/components/settings/device-state/DeviceStateSection.tsx — P1 device
// state backup block (spec §3.7): this computer's name, where its state is
// saved, and the uploader's status line.
import { useI18nStore } from '../../../stores/useI18nStore'
import { useDeviceStateStore } from '../../../stores/useDeviceStateStore'
import type { DeviceStateStatus } from '../../../stores/useDeviceStateStore'
import { selectDevHostId, useHostStore } from '../../../stores/useHostStore'
import { SettingItem } from '../SettingItem'
import { DeviceNameField } from './DeviceNameField'

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
  const status = useDeviceStateStore((s) => s.status)
  const targetId = useHostStore(selectDevHostId)
  const targetName = useHostStore((s) => (targetId ? s.hosts[targetId]?.name : undefined))

  return (
    <div data-testid="device-state-section" className="mt-8">
      <h3 className="text-sm text-text-primary">{t('settings.device_state.title')}</h3>
      <p className="text-xs text-text-secondary">{t('settings.device_state.desc')}</p>

      <DeviceNameField />

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
