import { useI18nStore } from '../../../stores/useI18nStore'
import { useNewTabLayoutStore } from '../../../stores/useNewTabLayoutStore'
import type { ProfileKey } from '../../../lib/resolve-profile'

interface Props {
  active: ProfileKey
  onSelect: (k: ProfileKey) => void
  onToggleEnabled: (k: ProfileKey, enabled: boolean) => void
  renderMain: (k: ProfileKey) => React.ReactNode
  renderThumb: (k: ProfileKey) => React.ReactNode
}

const KEYS: ProfileKey[] = ['3col', '2col', '1col']
const LABEL_KEY: Record<ProfileKey, string> = {
  '3col': 'settings.interface.profile_3col',
  '2col': 'settings.interface.profile_2col',
  '1col': 'settings.interface.profile_1col',
}

export function NewTabProfileSwitcher({ active, onSelect, onToggleEnabled, renderMain, renderThumb }: Props) {
  const t = useI18nStore((s) => s.t)
  const profiles = useNewTabLayoutStore((s) => s.profiles)

  const meta = (k: ProfileKey) => {
    const p = profiles[k]
    return {
      enabled: p.enabled,
      isEmpty: p.columns.flat().length === 0,
      locked: k === '1col',
    }
  }

  return (
    <div className="flex flex-col gap-2">
      <div className="flex items-center gap-1 px-3 pt-2 flex-wrap">
        {KEYS.map((k) => {
          const m = meta(k)
          return (
            <div key={k} className="flex items-center gap-1">
              <button
                type="button"
                data-testid={`profile-tab-${k}`}
                data-active={k === active ? 'true' : undefined}
                onClick={() => onSelect(k)}
                className={[
                  'px-2 py-1 text-xs rounded-md transition-colors cursor-pointer',
                  k === active
                    ? 'bg-surface-elevated text-text-primary border border-border-active'
                    : 'text-text-secondary hover:bg-white/5 border border-transparent',
                ].join(' ')}
              >
                {t(LABEL_KEY[k])}
                {m.isEmpty && (
                  <span data-testid={`profile-empty-${k}`} className="ml-1 text-[10px] text-text-muted">
                    {t('settings.interface.profile_empty')}
                  </span>
                )}
              </button>
              <label className="inline-flex items-center gap-1 text-[10px] text-text-secondary select-none">
                <input
                  type="checkbox"
                  data-testid={`profile-toggle-${k}`}
                  checked={m.enabled}
                  disabled={m.locked}
                  onChange={(e) => { if (!m.locked) onToggleEnabled(k, e.target.checked) }}
                  title={m.locked ? t('settings.interface.profile_locked') : undefined}
                />
                <span>{t('settings.interface.enabled')}</span>
              </label>
              {!m.enabled && !m.isEmpty && !m.locked && (
                <span data-testid={`profile-hint-${k}`} className="text-[10px] text-text-muted">
                  {t('settings.interface.profile_prefilled')}
                </span>
              )}
            </div>
          )
        })}
      </div>
      <div className="flex-1 px-3">{renderMain(active)}</div>
      <div className="flex gap-2 px-3 pb-3">
        {KEYS.filter((k) => k !== active).map((k) => (
          <button
            key={k}
            type="button"
            className="border border-border-subtle rounded-md p-1 cursor-pointer hover:bg-white/5"
            onClick={() => onSelect(k)}
            data-testid={`profile-thumb-${k}`}
            title={t(LABEL_KEY[k])}
            aria-label={t(LABEL_KEY[k])}
          >
            {renderThumb(k)}
          </button>
        ))}
      </div>
    </div>
  )
}
