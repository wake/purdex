import { useRef, useState } from 'react'
import { useI18nStore } from '../../stores/useI18nStore'
import { DEFAULT_HOST_ICON, isIconWeight } from '../../lib/host-color'
import { WorkspaceIcon } from '../../features/workspace/components/WorkspaceIcon'
import { WorkspaceIconPicker } from '../../features/workspace/components/WorkspaceIconPicker'
import { FloatingPanel } from '../FloatingPanel'
import type { IconWeight } from '../../types/tab'

/**
 * The icon editor's body, controlled: it shows `icon` / `weight` and reports every edit through `onChange` — it knows
 * no host. `HostIconField` binds it to a daemon host's look in the App's store; the probe-only host dialog binds it to
 * a draft. Wraps `WorkspaceIconPicker` in `inline` mode and maps its "clear" contract — `onSelect('')` — onto
 * `onChange(null)`.
 *
 * `onChange(icon, weight)`: `icon` null = back to the default (both keys dropped); `weight` is given only when the
 * edit carries one.
 */
export function HostIconPicker({ icon, weight: storedWeight, onChange }: {
  icon: string | undefined
  weight: unknown
  onChange: (icon: string | null, weight?: IconWeight) => void
}) {
  const t = useI18nStore((s) => s.t)
  const [open, setOpen] = useState(false)
  const buttonsRef = useRef<HTMLDivElement>(null)

  const weight = asWeight(storedWeight)

  const clear = () => {
    onChange(null)
    setOpen(false)
  }

  return (
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
              onChange(name, weight)
              setOpen(false)
            }}
            onWeightChange={(w) => onChange(icon ?? DEFAULT_HOST_ICON, w)}
            onCancel={() => setOpen(false)}
          />
        </FloatingPanel>
      )}
    </div>
  )
}

function asWeight(v: unknown): IconWeight {
  return isIconWeight(v) ? v : 'regular'
}
