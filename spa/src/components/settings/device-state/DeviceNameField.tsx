// spa/src/components/settings/device-state/DeviceNameField.tsx — this
// computer's name editor for the device state block (spec §3.7).
//
// While the user has an uncommitted edit (`dirty`), the input shows the local
// draft and ignores store updates — e.g. the uploader resolving
// `defaultDeviceName` asynchronously at startup, or a sync from another
// window — so typing is never overwritten. When not dirty, the input simply
// mirrors the stored (effective) name; no render-phase or effect resync needed.
import { useState } from 'react'
import { useI18nStore } from '../../../stores/useI18nStore'
import { effectiveDeviceName } from '../../../lib/device-name'
import { useDeviceNameStore } from '../../../stores/useDeviceNameStore'
import { SettingItem } from '../SettingItem'

export function DeviceNameField() {
  const t = useI18nStore((s) => s.t)
  const deviceName = useDeviceNameStore((s) => s.deviceName)
  const defaultDeviceName = useDeviceNameStore((s) => s.defaultDeviceName)
  const setDeviceName = useDeviceNameStore((s) => s.setDeviceName)

  const current = effectiveDeviceName({ deviceName, defaultDeviceName })
  const [draft, setDraft] = useState('')
  const [dirty, setDirty] = useState(false)

  const commit = () => {
    if (!dirty) return
    // The store normalizes (trim / blank → default); once clean, the input
    // shows what was actually kept.
    setDeviceName(draft)
    setDirty(false)
  }

  const discard = () => setDirty(false)

  const reset = () => {
    setDeviceName(null)
    setDirty(false)
  }

  return (
    <SettingItem label={t('settings.device_state.device_name')}>
      <div className="flex items-center gap-2">
        <input
          type="text"
          aria-label={t('settings.device_state.device_name_aria')}
          data-testid="device-state-name"
          placeholder={defaultDeviceName}
          spellCheck={false}
          value={dirty ? draft : current}
          onChange={(e) => {
            setDraft(e.target.value)
            setDirty(true)
          }}
          onBlur={commit}
          onKeyDown={(e) => {
            if (e.nativeEvent.isComposing) return
            if (e.key === 'Enter') commit()
            else if (e.key === 'Escape') discard()
          }}
          className="bg-surface-secondary border border-border-default rounded px-2 py-1 text-xs text-text-primary w-48"
        />
        {deviceName !== null && (
          <button
            type="button"
            data-testid="device-state-name-reset"
            onClick={reset}
            className="px-2.5 py-1 rounded-md border border-border-default text-text-secondary text-xs hover:text-text-primary hover:border-border-active"
          >
            {t('settings.device_state.device_name_reset')}
          </button>
        )}
      </div>
    </SettingItem>
  )
}
