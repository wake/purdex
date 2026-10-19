// spa/src/components/settings/personas/PersonaSourcePicker.tsx — the 「正本主機」 choice (name pool v2 spec §4.3) and the
// line that says the source is offline. The choice is per device (`usePersonaSourceStore`); the page edits the source's
// copy and the App copies it to the other hosts.
import { useI18nStore } from '../../../stores/useI18nStore'
import { useHostStore } from '../../../stores/useHostStore'
import { hostLabel, hostLookOf } from '../../../lib/host-look'
import { selectPersonasV1, useNexHostStore } from '../../../stores/useNexHostStore'
import { usePersonaSourceStore } from '../../../stores/usePersonaSourceStore'

interface Props {
  /** The resolved source host of this device, or `null` when the App has no host. */
  sourceId: string | null
  /** Another host's copy is shown because the source cannot be reached. */
  offline: boolean
  disabled: boolean
}

export function PersonaSourcePicker({ sourceId, offline, disabled }: Props) {
  const t = useI18nStore((s) => s.t)
  const hostOrder = useHostStore((s) => s.hostOrder)
  const hosts = useHostStore((s) => s.hosts)
  const setSource = usePersonaSourceStore((s) => s.setSource)
  const nex = useNexHostStore((s) => s.byHost)
  const label = (id: string) => hostLabel(id, hostLookOf(id))
  // A host that does not declare `personas.v1` is neither read nor written (spec §4.3), so it cannot be a source. The
  // current choice stays listed so the select never shows a value it does not have.
  const serves = (id: string) => (nex[id]?.fetchedAt ?? 0) > 0 && selectPersonasV1(id)({ byHost: nex })
  const ids = hostOrder.filter((id) => hosts[id] && (serves(id) || id === sourceId))
  if (sourceId === null || ids.length === 0) return null
  return (
    <div className="mt-3" data-testid="persona-source">
      <label className="flex items-center gap-2 text-xs text-text-secondary">
        <span>{t('settings.personas.source.label')}</span>
        <select
          data-testid="persona-source-select"
          className="rounded-md border border-border-default bg-surface-primary px-2 py-1 text-xs text-text-primary disabled:opacity-50"
          value={sourceId}
          disabled={disabled}
          onChange={(e) => setSource(e.target.value)}
        >
          {ids.map((id) => <option key={id} value={id}>{label(id)}</option>)}
        </select>
      </label>
      <p className="mt-1 text-xs text-text-secondary">{t('settings.personas.source.help')}</p>
      {offline && (
        <p className="mt-1 text-xs text-text-secondary" data-testid="personas-source-offline">
          {t('settings.personas.sourceOffline', { host: label(sourceId) })}
        </p>
      )}
    </div>
  )
}
