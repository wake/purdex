// spa/src/components/settings/device-state/DeviceStateSection.tsx — device
// state backup (spec §3.7 + §4.1/§4.3): this computer's name, where its state
// is saved, the uploader's status line, and the list of every computer's
// latest state with Replace / Merge / Delete.
import { useState } from 'react'
import { ArrowsClockwise } from '@phosphor-icons/react'
import { useI18nStore } from '../../../stores/useI18nStore'
import { useDeviceStateStore } from '../../../stores/useDeviceStateStore'
import type { DeviceStateStatus } from '../../../stores/useDeviceStateStore'
import { selectDevHostId, useHostStore } from '../../../stores/useHostStore'
import { getClientId } from '../../../lib/client-identity'
import { deleteDeviceState } from '../../../lib/device-state/api'
import { SettingItem } from '../SettingItem'
import { DeviceNameField } from './DeviceNameField'
import { DeviceStateRow } from './DeviceStateRow'
import { useDeviceStateList } from './useDeviceStateList'
import { errMessage, useDeviceStateActions } from './useDeviceStateActions'
import type { Tone } from './useDeviceStateActions'

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

const TONE_COLOR: Record<Tone, string> = {
  busy: 'text-text-secondary',
  success: 'text-green-500',
  warn: 'text-yellow-500',
  error: 'text-red-500',
}

export function DeviceStateSection({ onRestored }: { onRestored?: () => void } = {}) {
  const t = useI18nStore((s) => s.t)
  const status = useDeviceStateStore((s) => s.status)
  const targetId = useHostStore(selectDevHostId)
  const targetName = useHostStore((s) => (targetId ? s.hosts[targetId]?.name : undefined))
  // getClientId() creates and persists an id on a fresh profile, so the own
  // row is recognised (badge, Delete disabled) even before sync ever ran.
  const [ownClientId] = useState(() => getClientId())
  const { view, reload } = useDeviceStateList(targetId)
  const { busy, actionStatus, setActionStatus, exclusive, replaceLocked, mergeLocked, replace, merge } =
    useDeviceStateActions(onRestored)

  const handleDelete = (clientId: string) => {
    if (!targetId) return
    void exclusive(async () => {
      try {
        await deleteDeviceState(targetId, clientId)
        setActionStatus(null)
        reload()
      } catch (e) {
        setActionStatus({ tone: 'error', message: t('settings.device_state.action.delete_failed', { message: errMessage(e) }) })
      }
    })
  }

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

      {targetId && view && (
        <div data-testid="device-state-list" data-state={view.kind === 'rows' && view.rows.length === 0 ? 'empty' : view.kind} className="mt-6">
          <div className="mb-2 flex items-center justify-between">
            <h3 className="text-sm text-text-primary">{t('settings.device_state.list.title')}</h3>
            <button
              type="button"
              data-testid="device-state-list-refresh"
              onClick={reload}
              className="flex items-center gap-1.5 rounded-md border border-border-default px-3 py-1.5 text-xs text-text-secondary hover:text-text-primary hover:border-border-active"
            >
              <ArrowsClockwise size={14} />
              {t('settings.device_state.list.refresh')}
            </button>
          </div>

          {view.kind === 'error' && (
            <p data-testid="device-state-list-error" className="flex flex-wrap items-center gap-2 text-xs text-red-500">
              <span>{t('settings.device_state.list.load_failed', { message: view.message })}</span>
              <button
                type="button"
                data-testid="device-state-list-retry"
                onClick={reload}
                className="rounded-md border border-border-default px-2.5 py-1 text-xs text-text-secondary hover:text-text-primary"
              >
                {t('settings.device_state.list.refresh')}
              </button>
            </p>
          )}

          {view.kind === 'rows' && view.rows.length === 0 && (
            <p data-testid="device-state-list-empty" className="text-xs text-text-muted">
              {t('settings.device_state.list.empty')}
            </p>
          )}

          {view.kind === 'rows' && view.rows.length > 0 && (
            <ul className="flex flex-col">
              {view.rows.map((row) => (
                <DeviceStateRow
                  // updatedAt in the key: a newer upload remounts the row, dropping its cached record.
                  key={`${targetId}:${row.clientId}:${row.updatedAt}`}
                  hostId={targetId}
                  summary={row}
                  isOwn={row.clientId === ownClientId}
                  busy={busy}
                  replaceLocked={replaceLocked}
                  mergeLocked={mergeLocked}
                  onReplace={replace}
                  onMerge={merge}
                  onDelete={handleDelete}
                />
              ))}
            </ul>
          )}
        </div>
      )}

      {actionStatus && (
        <p
          data-testid="device-state-action-status"
          data-tone={actionStatus.tone}
          {...actionStatus.attrs}
          className={`mt-2 text-xs ${TONE_COLOR[actionStatus.tone]}`}
        >
          {actionStatus.message}
        </p>
      )}
    </div>
  )
}
