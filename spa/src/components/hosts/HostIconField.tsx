import { useHostStore } from '../../stores/useHostStore'
import { useI18nStore } from '../../stores/useI18nStore'
import { useHostLook } from '../../lib/host-look'
import type { HostColorMode } from '../../lib/host-color'
import { Field } from './form-fields'
import { HostBadgePreview } from './HostBadgePreview'
import { HostIconPicker } from './HostIconPicker'

/**
 * Per-host icon picker: `HostIconPicker` bound to the host's look — every edit goes to `setHostIcon`, where a cleared
 * icon is `setHostIcon(hostId, null)`.
 *
 * When `mode` is supplied (the Color row's selected mode), a live badge
 * preview renders to the right, following that mode.
 */
export function HostIconField({ hostId, mode }: { hostId: string; mode?: HostColorMode }) {
  const t = useI18nStore((s) => s.t)
  const look = useHostLook(hostId)
  const setHostIcon = useHostStore((s) => s.setHostIcon)

  return (
    <Field label={t('hosts.icon.label')}>
      <div className="flex items-start gap-6">
        <HostIconPicker icon={look.icon} weight={look.iconWeight} onChange={(icon, weight) => setHostIcon(hostId, icon, weight)} />
        {mode && <HostBadgePreview hostId={hostId} mode={mode} />}
      </div>
    </Field>
  )
}
