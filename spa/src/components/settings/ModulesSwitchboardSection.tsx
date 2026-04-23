import { useMemo } from 'react'
import { useLocation } from 'wouter'
import { getModules, type ModuleDefinition } from '../../lib/module-registry'
import { listContributions } from '../../lib/settings-contribution-registry'
import type { SettingsContextFor } from '../../lib/settings-contribution-types'
import { useModuleEnabledStore } from '../../stores/useModuleEnabledStore'
import { useI18nStore } from '../../stores/useI18nStore'
import { ToggleSwitch } from './ToggleSwitch'

type T = (key: string) => string

// Registered through `registerSettingsSection()` (legacy adapter), which
// wraps components as no-prop. The optional `ctx` keeps the signature
// compatible with both the legacy shape and future migration to a
// contribution-type section (`ctx: SettingsContextFor<'purdex'>`).
interface Props {
  ctx?: SettingsContextFor<'purdex'>
}

// Spec §4.4. Lists every module that declared `disableable: true`, lets the
// user toggle each on/off, and surfaces a reload-required banner when the
// user's live toggles diverge from the session baseline (captured at boot).
// The underlying Switchboard state lives in `useModuleEnabledStore`; the
// filter that actually hides a disabled module's settings entries is in
// `dispatch-settings-contributions.ts` — this component is view-only.
export function ModulesSwitchboardSection({ ctx: _ctx }: Props = {}) {
  const t = useI18nStore((s) => s.t)
  const hasPending = useModuleEnabledStore((s) => s.hasPendingChanges())

  // Re-compute when the registry changes between renders (e.g. HMR). The
  // list is derived from `getModules()` which is a synchronous snapshot —
  // safe to call during render.
  const modules = useMemo(
    () => getModules().filter((m) => m.disableable === true),
    [],
  )

  return (
    <div className="p-6 space-y-6">
      {hasPending && <ReloadBanner t={t} />}
      <div className="space-y-4">
        {modules.map((m) => (
          <ModuleRow key={m.id} module={m} t={t} />
        ))}
      </div>
    </div>
  )
}

function ReloadBanner({ t }: { t: T }) {
  return (
    <div
      data-testid="reload-required-banner"
      className="rounded border border-amber-500/40 bg-amber-500/10 p-3 text-xs text-text-primary"
    >
      <div className="font-medium">{t('settings.modules.reload_required.title')}</div>
      <div className="mt-1 text-text-secondary">
        {t('settings.modules.reload_required.hint')}
      </div>
    </div>
  )
}

function ModuleRow({ module: m, t }: { module: ModuleDefinition; t: T }) {
  const enabled = useModuleEnabledStore((s) => s.isEnabled(m.id))
  const [, setLocation] = useLocation()

  // Only show the "Open settings" link when the module actually has a
  // purdex-scope contribution to land on. Computing this from
  // `listContributions` keeps the component honest about what exists in the
  // live registry — if dispatch skipped the module, the link naturally
  // disappears on the next render after reload.
  const firstPurdexLocalId = useMemo(() => {
    const own = listContributions('purdex').find((c) => c.moduleId === m.id)
    return own?.localId
  }, [m.id])

  const handleToggle = (next: boolean) => {
    useModuleEnabledStore.getState().setEnabled(m.id, next)
  }

  const handleOpen = (e: React.MouseEvent) => {
    if (!enabled) {
      e.preventDefault()
      return
    }
    if (!firstPurdexLocalId) return
    setLocation(`/settings/${firstPurdexLocalId}`)
  }

  return (
    <div
      data-module-id={m.id}
      className="flex items-start gap-3 rounded border border-border-subtle bg-surface-primary p-3"
    >
      <div className="flex-1 min-w-0">
        <div className="text-sm font-medium text-text-primary">{m.name}</div>
        {m.descriptionKey && (
          <div className="mt-1 text-xs text-text-secondary">{t(m.descriptionKey)}</div>
        )}
        {firstPurdexLocalId && (
          <button
            type="button"
            data-open-settings=""
            aria-disabled={!enabled}
            onClick={handleOpen}
            className={`mt-2 text-xs ${
              enabled
                ? 'text-accent hover:underline cursor-pointer'
                : 'text-text-muted cursor-not-allowed'
            }`}
          >
            {t('settings.modules.open_settings')}
          </button>
        )}
      </div>
      <div className="flex-shrink-0">
        <ToggleSwitch
          label={m.name}
          checked={enabled}
          onChange={handleToggle}
        />
      </div>
    </div>
  )
}
