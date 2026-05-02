import { getModules, type ModuleDefinition } from '../../lib/module-registry'
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
//
// AR-3 (codex adversarial review #617): no `useMemo` for registry-derived
// data. HMR deliberately preserves `useModuleEnabledStore` but rebuilds the
// module + contribution registries. A memoised snapshot would keep rendering
// stale rows / "Open settings" targets after a hot reload; since N ≤ a
// handful of disableable modules, recompute at render time.
export function ModulesSwitchboardSection({ ctx: _ctx }: Props = {}) {
  const t = useI18nStore((s) => s.t)
  const hasPending = useModuleEnabledStore((s) => s.hasPendingChanges())

  const modules = getModules().filter((m) => m.disableable === true)

  return (
    // Spec §4.5 — outer wrapper drops `p-6` because GlobalSettingsPage
    // already pads the section container; doubling up was producing
    // visible inset drift versus other module-owned sections.
    <div className="space-y-6">
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

// AR-2 (codex adversarial review #617): "Open settings →" CTA removed. None
// of the current opt-in modules (editor/files/browser/memory-monitor) expose
// a purdex-scope contribution, so in production the link branch was 100%
// dead. The affordance will be re-introduced once PR 2 migrates Editor
// Buffers to a real `settings: [{ scope: 'purdex', ... }]` declaration.
function ModuleRow({ module: m, t }: { module: ModuleDefinition; t: T }) {
  const enabled = useModuleEnabledStore((s) => s.isEnabled(m.id))

  const handleToggle = (next: boolean) => {
    useModuleEnabledStore.getState().setEnabled(m.id, next)
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
