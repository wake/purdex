import { useMemo, useState } from 'react'
import { MagnifyingGlass } from '@phosphor-icons/react'
import { AGENT_ICON_VALUES } from '../../lib/command-icons'
import type { CommandIcon } from '../../lib/host-config-api'
import { useI18nStore } from '../../stores/useI18nStore'
import iconMetaData from '../../features/workspace/generated/icon-meta.json'
import { CommandIconView } from './CommandIconView'

interface IconMeta { n: string; t: string[]; c: string[] }

// Static import on purpose: WorkspaceIconPicker already pulls icon-meta into
// the main chunk, so a dynamic import() would not split it out (plan Task 1).
const CATALOG = iconMetaData as IconMeta[]

const PAGE = 120

function matches(meta: IconMeta, q: string): boolean {
  return meta.n.toLowerCase().includes(q) || meta.t.some((tag) => tag.toLowerCase().includes(q))
}

export function CommandIconPicker({ value, onChange, disabled = false }: {
  value: CommandIcon
  onChange: (icon: CommandIcon) => void
  disabled?: boolean
}) {
  const t = useI18nStore((s) => s.t)
  const [query, setQuery] = useState('')
  const [limit, setLimit] = useState(PAGE)

  const filtered = useMemo(() => {
    const q = query.trim().toLowerCase()
    return q ? CATALOG.filter((m) => matches(m, q)) : CATALOG
  }, [query])

  return (
    <div className="flex flex-col gap-2">
      <div className="text-[11px] text-text-secondary">{t('command_icon.agents')}</div>
      <div className="flex flex-wrap gap-2">
        {AGENT_ICON_VALUES.map((v) => {
          const selected = value.kind === 'agent' && value.value === v
          return (
            <button
              key={v}
              type="button"
              data-testid={`command-icon-agent-${v}`}
              aria-pressed={selected}
              title={v}
              disabled={disabled}
              onClick={() => onChange({ kind: 'agent', value: v })}
              className={`flex items-center px-3 py-1.5 rounded-md border text-xs transition-colors cursor-pointer disabled:cursor-not-allowed disabled:opacity-50 ${
                selected
                  ? 'bg-surface-elevated border-border-active text-text-primary'
                  : 'bg-transparent border-border-default text-text-muted hover:text-text-primary hover:border-text-muted'
              }`}
            >
              <CommandIconView icon={{ kind: 'agent', value: v }} size={16} />
            </button>
          )
        })}
      </div>

      <div className="text-[11px] text-text-secondary">{t('command_icon.all')}</div>
      <div className="relative">
        <MagnifyingGlass size={14} className="absolute left-2.5 top-1/2 -translate-y-1/2 text-text-muted" />
        <input
          data-testid="command-icon-search"
          value={query}
          disabled={disabled}
          onChange={(e) => { setQuery(e.target.value); setLimit(PAGE) }}
          placeholder={t('command_icon.search')}
          className="w-full pl-8 pr-3 py-1.5 bg-surface-tertiary border border-border-subtle rounded-md text-xs text-text-primary placeholder:text-text-muted focus:outline-none focus:ring-1 focus:ring-accent disabled:opacity-50"
        />
      </div>

      {filtered.length === 0 ? (
        <div data-testid="command-icon-empty" className="text-xs text-text-muted">{t('command_icon.no_results')}</div>
      ) : (
        <div className="max-h-48 overflow-y-auto p-0.5">
          <div className="flex flex-wrap gap-1.5">
            {filtered.slice(0, limit).map((m) => {
              const selected = value.kind === 'phosphor' && value.value === m.n
              return (
                <button
                  key={m.n}
                  type="button"
                  data-testid={`command-icon-phosphor-${m.n}`}
                  aria-pressed={selected}
                  title={m.n}
                  disabled={disabled}
                  onClick={() => onChange({ kind: 'phosphor', value: m.n })}
                  className={`w-8 h-8 rounded-md flex items-center justify-center cursor-pointer transition-colors disabled:cursor-not-allowed disabled:opacity-50 ${
                    selected
                      ? 'bg-accent/20 ring-2 ring-accent text-text-primary'
                      : 'bg-surface-tertiary text-text-secondary hover:text-text-primary hover:bg-surface-hover'
                  }`}
                >
                  <CommandIconView icon={{ kind: 'phosphor', value: m.n }} size={18} />
                </button>
              )
            })}
          </div>
          {filtered.length > limit && (
            <button
              type="button"
              data-testid="command-icon-more"
              onClick={() => setLimit((n) => n + PAGE)}
              className="mt-2 text-xs text-text-secondary hover:text-text-primary cursor-pointer"
            >
              {t('command_icon.more', { n: filtered.length - limit })}
            </button>
          )}
        </div>
      )}
    </div>
  )
}
