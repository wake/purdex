import { useState } from 'react'
import { Prohibit } from '@phosphor-icons/react'
import { useHostStore } from '../../stores/useHostStore'
import { useI18nStore } from '../../stores/useI18nStore'
import { HOST_COLOR_PRESETS, normalizeHostColor } from '../../lib/host-color'
import { Field } from './form-fields'

export function HostColorField({ hostId }: { hostId: string }) {
  const t = useI18nStore((s) => s.t)
  const current = useHostStore((s) => s.hosts[hostId]?.color)
  const setHostColor = useHostStore((s) => s.setHostColor)

  const [draft, setDraft] = useState(current ?? '')
  const [invalid, setInvalid] = useState(false)
  const [synced, setSynced] = useState(current)

  // Re-sync the draft when the stored color changes (render-phase adjust, no effect).
  if (synced !== current) {
    setSynced(current)
    setDraft(current ?? '')
    setInvalid(false)
  }

  const commit = () => {
    if (draft.trim() === '') {
      setInvalid(false)
      setHostColor(hostId, null)
      return
    }
    const normalized = normalizeHostColor(draft)
    if (!normalized) {
      setInvalid(true)
      return
    }
    setInvalid(false)
    setHostColor(hostId, normalized)
    setDraft(normalized)
  }

  return (
    <Field label={t('hosts.color.label')}>
      <div className="space-y-1">
        <div className="flex flex-wrap items-center gap-1.5">
          {HOST_COLOR_PRESETS.map((hex) => {
            const pressed = current === hex
            return (
              <button
                key={hex}
                type="button"
                aria-label={hex}
                aria-pressed={pressed}
                onClick={() => setHostColor(hostId, hex)}
                className={`w-[18px] h-[18px] rounded cursor-pointer border border-border-default ${
                  pressed ? 'ring-2 ring-offset-1 ring-offset-surface-primary ring-text-primary' : ''
                }`}
                style={{ background: hex }}
              />
            )
          })}
          <button
            type="button"
            aria-label={t('hosts.color.clear')}
            title={t('hosts.color.clear')}
            data-testid="host-color-clear"
            onClick={() => {
              // The store selector may not change (already uncolored), so reset local state directly.
              setDraft('')
              setInvalid(false)
              setHostColor(hostId, null)
            }}
            className="w-[18px] h-[18px] rounded cursor-pointer border border-border-default flex items-center justify-center text-text-muted hover:text-text-secondary"
          >
            <Prohibit size={12} />
          </button>
          <input
            type="text"
            aria-label={t('hosts.color.custom_aria')}
            aria-invalid={invalid}
            data-testid="host-color-hex"
            placeholder="#3b82f6"
            spellCheck={false}
            value={draft}
            onChange={(e) => setDraft(e.target.value)}
            onBlur={commit}
            onKeyDown={(e) => {
              if (e.key !== 'Enter' || e.nativeEvent.isComposing) return
              commit()
            }}
            className="ml-1 bg-surface-secondary border border-border-default rounded px-2 py-0.5 text-xs text-text-primary font-mono w-24"
          />
        </div>
        {invalid && <span role="alert" className="block text-xs text-red-400">{t('hosts.color.invalid')}</span>}
      </div>
    </Field>
  )
}
