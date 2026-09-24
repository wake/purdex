import { useRef, useState } from 'react'
import { useHostStore } from '../../stores/useHostStore'
import { useI18nStore } from '../../stores/useI18nStore'
import { useHostLook } from '../../lib/host-look'
import { DEFAULT_HOST_ICON, isIconWeight, type HostColorMode } from '../../lib/host-color'
import { WorkspaceIcon } from '../../features/workspace/components/WorkspaceIcon'
import { WorkspaceIconPicker } from '../../features/workspace/components/WorkspaceIconPicker'
import { Field } from './form-fields'
import { HostBadgePreview } from './HostBadgePreview'
import { FloatingPanel } from '../FloatingPanel'

/**
 * Per-host icon picker. Wraps `WorkspaceIconPicker` in `inline` mode (which
 * renders no workspace-specific header) and maps its "clear" contract —
 * `onSelect('')` — onto `setHostIcon(hostId, null)`.
 *
 * When `mode` is supplied (the Color row's selected mode), a live badge
 * preview renders to the right, following that mode.
 */
export function HostIconField({ hostId, mode }: { hostId: string; mode?: HostColorMode }) {
  const t = useI18nStore((s) => s.t)
  const look = useHostLook(hostId)
  const icon = look.icon
  const storedWeight = look.iconWeight
  const setHostIcon = useHostStore((s) => s.setHostIcon)
  const [open, setOpen] = useState(false)
  const buttonsRef = useRef<HTMLDivElement>(null)

  const weight = isIconWeight(storedWeight) ? storedWeight : 'regular'

  const clear = () => {
    setHostIcon(hostId, null)
    setOpen(false)
  }

  return (
    <Field label={t('hosts.icon.label')}>
      <div className="flex items-start gap-6">
        <div className="space-y-2">
          <div ref={buttonsRef} className="flex items-center gap-2">
            <button
              type="button"
              data-testid="host-icon-preview"
              aria-label={t('hosts.icon.change')}
              title={t('hosts.icon.change')}
              aria-expanded={open}
              onClick={() => setOpen((v) => !v)}
              className="w-7 h-7 rounded border border-border-default flex items-center justify-center text-text-secondary hover:text-text-primary hover:border-text-muted cursor-pointer"
            >
              <WorkspaceIcon icon={icon ?? DEFAULT_HOST_ICON} name="" size={18} weight={weight} />
            </button>
            <button
              type="button"
              data-testid="host-icon-default"
              onClick={clear}
              className="px-2 py-1 rounded text-xs bg-surface-secondary border border-border-default text-text-secondary hover:text-text-primary cursor-pointer"
            >
              {t('hosts.icon.default')}
            </button>
          </div>
          {open && (
            <FloatingPanel title={t('hosts.icon.change')} anchorRef={buttonsRef} onClose={() => setOpen(false)} width={360}>
              <WorkspaceIconPicker
                inline
                currentIcon={icon}
                currentWeight={weight}
                onSelect={(name) => {
                  if (name === '') {
                    clear()
                    return
                  }
                  setHostIcon(hostId, name, weight)
                  setOpen(false)
                }}
                onWeightChange={(w) => setHostIcon(hostId, icon ?? DEFAULT_HOST_ICON, w)}
                onCancel={() => setOpen(false)}
              />
            </FloatingPanel>
          )}
        </div>
        {mode && <HostBadgePreview hostId={hostId} mode={mode} />}
      </div>
    </Field>
  )
}
