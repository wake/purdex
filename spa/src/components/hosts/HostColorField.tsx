import { useState } from 'react'
import { useHostStore } from '../../stores/useHostStore'
import { useI18nStore } from '../../stores/useI18nStore'
import { useHostLook } from '../../lib/host-look'
import type { HostColorMode } from '../../lib/host-color'
import { Field } from './form-fields'
import { HostColorPicker } from './HostColorPicker'

/**
 * Per-host color row (spec §6): `HostColorPicker` bound to the host's look. Every edit goes straight to the store so
 * the badge on every tab row follows.
 */
export function HostColorField({
  hostId,
  mode: controlledMode,
  onModeChange,
}: {
  hostId: string
  mode?: HostColorMode
  onModeChange?: (mode: HostColorMode) => void
}) {
  const t = useI18nStore((s) => s.t)
  const look = useHostLook(hostId)
  const setHostColorLayer = useHostStore((s) => s.setHostColorLayer)
  const clearHostColorMode = useHostStore((s) => s.clearHostColorMode)

  const [innerMode, setInnerMode] = useState<HostColorMode>('console')
  const mode = controlledMode ?? innerMode

  return (
    <Field label={t('hosts.color.label')}>
      <HostColorPicker
        colors={look.colors}
        legacyColor={look.color}
        mode={mode}
        onModeChange={(m) => {
          setInnerMode(m)
          onModeChange?.(m)
        }}
        onSetLayer={(m, layer, value) => setHostColorLayer(hostId, m, layer, value)}
        onClearMode={(m) => clearHostColorMode(hostId, m)}
      />
    </Field>
  )
}
