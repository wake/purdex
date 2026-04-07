import { useState, useMemo, Suspense, lazy } from 'react'
import { MagnifyingGlass, X } from '@phosphor-icons/react'
import type { Icon } from '@phosphor-icons/react'
import { useI18nStore } from '../../../stores/useI18nStore'
import { CURATED_ICON_CATEGORIES, CURATED_ICON_SET } from '../constants'
import { ALL_ICON_NAMES, iconLoaders } from '../generated/icon-loader'

/** Cache of resolved lazy components */
const lazyCache = new Map<string, React.LazyExoticComponent<Icon>>()

function getLazy(name: string): React.LazyExoticComponent<Icon> | null {
  if (lazyCache.has(name)) return lazyCache.get(name)!
  const loader = iconLoaders[name]
  if (!loader) return null
  const L = lazy(() => loader().then((comp) => ({ default: comp })))
  lazyCache.set(name, L)
  return L
}

/* eslint-disable react-hooks/static-components -- lazyCache guarantees stable reference per icon name */
function IconCell({ name, selected, onSelect }: { name: string; selected: boolean; onSelect: () => void }) {
  const LazyIcon = useMemo(() => getLazy(name), [name])
  return (
    <button
      data-icon={name}
      title={name}
      aria-pressed={selected}
      onClick={onSelect}
      className={`w-8 h-8 rounded-md flex items-center justify-center cursor-pointer transition-colors ${
        selected
          ? 'bg-accent/20 ring-2 ring-accent text-text-primary'
          : 'bg-surface-tertiary text-text-secondary hover:text-text-primary hover:bg-surface-hover'
      }`}
    >
      {LazyIcon ? (
        <Suspense fallback={<span className="text-[10px] opacity-40">{name.charAt(0)}</span>}>
          <LazyIcon size={18} />
        </Suspense>
      ) : (
        <span className="text-xs">{name.charAt(0)}</span>
      )}
    </button>
  )
}
/* eslint-enable react-hooks/static-components */

const categoryLabels: Record<string, string> = {
  general: 'General', development: 'Dev', objects: 'Objects',
  communication: 'Chat', media: 'Media', arrows: 'Arrows',
  nature: 'Nature', business: 'Biz',
}

interface Props {
  currentIcon: string | undefined
  onSelect: (icon: string) => void
  onCancel: () => void
  inline?: boolean
}

export function WorkspaceIconPicker({ currentIcon, onSelect, onCancel, inline }: Props) {
  const t = useI18nStore((s) => s.t)
  const categories = Object.keys(CURATED_ICON_CATEGORIES)
  const [activeCategory, setActiveCategory] = useState(categories[0])
  const [search, setSearch] = useState('')

  const displayIcons = useMemo(() => {
    if (!search.trim()) return CURATED_ICON_CATEGORIES[activeCategory] ?? []
    const q = search.trim().toLowerCase()
    const curated = Object.values(CURATED_ICON_CATEGORIES).flat()
      .filter((n) => n.toLowerCase().includes(q))
    const full = ALL_ICON_NAMES
      .filter((n) => n.toLowerCase().includes(q) && !CURATED_ICON_SET.has(n))
    return [...curated, ...full].slice(0, 100)
  }, [search, activeCategory])

  const content = (
    <div className={inline ? '' : 'bg-surface-secondary border border-border-default rounded-lg shadow-xl w-full max-w-sm mx-4 p-5'}>
      {!inline && <h3 className="text-sm font-semibold text-text-primary mb-3">{t('workspace.change_icon')}</h3>}

      {/* Search */}
      <div className="relative mb-3">
        <MagnifyingGlass size={14} className="absolute left-2.5 top-1/2 -translate-y-1/2 text-text-tertiary" />
        <input
          value={search}
          onChange={(e) => setSearch(e.target.value)}
          placeholder="Search icons..."
          className="w-full pl-8 pr-3 py-1.5 bg-surface-tertiary border border-border-subtle rounded-md text-xs text-text-primary placeholder:text-text-tertiary focus:outline-none focus:ring-1 focus:ring-accent"
        />
      </div>

      {/* Category tabs (hidden during search) */}
      {!search.trim() && (
        <div className="flex flex-wrap gap-1 mb-3">
          {categories.map((cat) => (
            <button
              key={cat}
              data-testid={`category-${cat}`}
              onClick={() => setActiveCategory(cat)}
              className={`px-2 py-0.5 rounded text-[11px] cursor-pointer transition-colors ${
                activeCategory === cat
                  ? 'bg-accent/20 text-accent font-semibold'
                  : 'text-text-secondary hover:text-text-primary hover:bg-surface-hover'
              }`}
            >
              {categoryLabels[cat] ?? cat}
            </button>
          ))}
        </div>
      )}

      {/* Icon grid */}
      <div className="grid grid-cols-8 gap-1.5 max-h-48 overflow-y-auto">
        {displayIcons.map((name) => (
          <IconCell
            key={name}
            name={name}
            selected={name === currentIcon}
            onSelect={() => onSelect(name)}
          />
        ))}
      </div>

      {/* Clear + cancel */}
      <div className="flex items-center justify-between mt-3">
        <button
          data-testid="clear-icon"
          onClick={() => onSelect('')}
          className="flex items-center gap-1 px-2 py-1 rounded text-xs text-text-secondary hover:text-text-primary hover:bg-surface-hover cursor-pointer"
        >
          <X size={12} />
          {t('common.clear') ?? 'Clear'}
        </button>
        {!inline && (
          <button onClick={onCancel} className="px-3 py-1.5 rounded text-xs bg-surface-tertiary text-text-secondary hover:text-text-primary cursor-pointer">
            {t('common.cancel')}
          </button>
        )}
      </div>
    </div>
  )

  if (inline) return content
  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/50">
      {content}
    </div>
  )
}
