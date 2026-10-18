// spa/src/components/settings/personas/PersonaList.tsx — the left side of Settings › Personas: one row per persona,
// the retired ones last and dimmed (name pool v2 plan NA-0). Nothing here writes; a click only selects.
import { Crown } from '@phosphor-icons/react'
import { Avatar } from '../../Avatar'
import { useI18nStore } from '../../../stores/useI18nStore'
import { personaColor } from '../../../lib/names/persona-color'
import type { Persona } from '../../../lib/names/persona-types'

interface Props {
  items: Persona[]
  /** The selected persona's code; `''` for a persona being created (it has no row yet). */
  selected: string | null
  /** A save is in flight: the selection stays where it is until the answer lands. */
  disabled?: boolean
  onSelect: (code: string) => void
}

/** Active personas first, retired ones after, each group in the collection's own order. */
export function orderForList(items: Persona[]): Persona[] {
  return [...items.filter((p) => !p.retired), ...items.filter((p) => p.retired)]
}

export function PersonaList({ items, selected, disabled = false, onSelect }: Props) {
  const t = useI18nStore((s) => s.t)
  return (
    <ul className="flex flex-col gap-0.5" data-testid="persona-list">
      {orderForList(items).map((p) => (
        <li key={p.code}>
          <button
            type="button"
            disabled={disabled}
            onClick={() => onSelect(p.code)}
            aria-current={selected === p.code ? 'true' : undefined}
            data-testid={`persona-row-${p.code}`}
            className={`flex w-full cursor-pointer items-center gap-2 rounded-md px-2 py-1.5 text-left hover:bg-surface-secondary ${
              selected === p.code ? 'bg-surface-secondary' : ''
            } ${p.retired ? 'opacity-45' : ''}`}
          >
            <Avatar size={32} code={p.code} name={p.name} src={p.avatar_data} fallbackColor={personaColor(p.code)} />
            <span className="min-w-0 flex-1">
              <span className="flex items-center gap-1">
                <span className="truncate text-sm text-text-primary">{p.name || t('settings.personas.list.no_name')}</span>
                {p.lead && <Crown size={12} weight="fill" aria-label={t('settings.personas.list.lead')} className="shrink-0 text-text-secondary" />}
              </span>
              <span className="block truncate text-xs text-text-muted">{p.code}</span>
            </span>
            <span className="shrink-0 text-xs text-text-muted">
              {p.retired ? t('settings.personas.list.retired') : p.avatar.source}
            </span>
          </button>
        </li>
      ))}
    </ul>
  )
}
