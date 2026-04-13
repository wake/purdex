import { useState, useMemo, useRef, useEffect } from 'react'
import { MagnifyingGlass, X } from '@phosphor-icons/react'
import Fuse from 'fuse.js'
import { useVirtualizer } from '@tanstack/react-virtual'
import { useI18nStore } from '../../../stores/useI18nStore'
import type { IconWeight } from '../../../types/tab'
import { CURATED_ICON_CATEGORIES } from '../constants'
import { getIconPath, isWeightLoaded, prefetchWeight } from '../lib/icon-path-cache'
import { renderPaths } from '../lib/render-paths'
import iconMetaData from '../generated/icon-meta.json'

interface IconMeta {
  n: string
  t: string[]
  c: string[]
}

const iconMeta: IconMeta[] = iconMetaData as IconMeta[]

const fuse = new Fuse(iconMeta, {
  keys: ['n', 't', 'c'],
  threshold: 0.3,
})

const COLS = 8
const WEIGHTS: IconWeight[] = ['bold', 'regular', 'thin', 'light', 'fill', 'duotone']

function IconCell({
  name, selected, onSelect, weight,
}: {
  name: string; selected: boolean; onSelect: () => void; weight: string
}) {
  const pathData = getIconPath(name, weight)
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
      {pathData ? (
        <svg width={18} height={18} viewBox="0 0 256 256" fill="currentColor">
          {renderPaths(pathData)}
        </svg>
      ) : (
        <span className="text-xs">{name.charAt(0)}</span>
      )}
    </button>
  )
}

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
  currentWeight?: IconWeight
}

export function WorkspaceIconPicker({ currentIcon, onSelect, onCancel, inline, currentWeight = 'bold' }: Props) {
  const t = useI18nStore((s) => s.t)
  const categories = Object.keys(CURATED_ICON_CATEGORIES)
  const [activeCategory, setActiveCategory] = useState(categories[0])
  const [search, setSearch] = useState('')
  const [weight, setWeight] = useState<IconWeight>(currentWeight)
  const scrollRef = useRef<HTMLDivElement>(null)
  const [, setTick] = useState(0)

  // Prefetch weight data in useEffect (not in render body)
  useEffect(() => {
    if (!isWeightLoaded(weight)) {
      prefetchWeight(weight)
        .then(() => setTick((t) => t + 1))
        .catch(() => {})
    }
  }, [weight])

  // Reset scroll position when category or search changes
  useEffect(() => {
    if (scrollRef.current) scrollRef.current.scrollTop = 0
  }, [activeCategory, search])

  const displayIcons = useMemo(() => {
    if (!search.trim()) return CURATED_ICON_CATEGORIES[activeCategory] ?? []
    return fuse.search(search.trim()).map((r) => r.item.n)
  }, [search, activeCategory])

  const rowCount = Math.ceil(displayIcons.length / COLS)

  // eslint-disable-next-line react-hooks/incompatible-library -- TanStack Virtual is safe here, picker is not memoized
  const virtualizer = useVirtualizer({
    count: rowCount,
    getScrollElement: () => scrollRef.current,
    estimateSize: () => 38, // 32px icon + 6px gap
    overscan: 3,
    initialRect: { width: 300, height: 192 }, // fallback for environments without layout (jsdom)
  })

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

      {/* Weight toggle */}
      <div className="flex items-center gap-1.5 mb-3">
        <span className="text-[11px] text-text-tertiary mr-0.5">Style</span>
        {WEIGHTS.map((w) => (
          <button
            key={w}
            data-testid={`weight-${w}`}
            onClick={() => setWeight(w)}
            className={`px-2 py-0.5 rounded text-[11px] capitalize cursor-pointer transition-colors ${
              weight === w
                ? 'bg-accent/20 text-accent font-semibold'
                : 'text-text-secondary hover:text-text-primary hover:bg-surface-hover'
            }`}
          >
            {w}
          </button>
        ))}
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

      {/* Virtualized icon grid */}
      <div ref={scrollRef} className="max-h-48 overflow-y-auto p-0.5">
        {displayIcons.length === 0 ? (
          <div className="flex items-center justify-center h-24 text-xs text-text-tertiary">
            No results found
          </div>
        ) : (
          <div style={{ height: virtualizer.getTotalSize(), position: 'relative' }}>
            {virtualizer.getVirtualItems().map((vRow) => {
              const startIdx = vRow.index * COLS
              const rowIcons = displayIcons.slice(startIdx, startIdx + COLS)
              return (
                <div
                  key={vRow.key}
                  className="flex gap-1.5"
                  style={{
                    position: 'absolute',
                    top: 0,
                    left: 0,
                    transform: `translateY(${vRow.start}px)`,
                    height: vRow.size,
                  }}
                >
                  {rowIcons.map((name) => (
                    <IconCell
                      key={name}
                      name={name}
                      selected={name === currentIcon}
                      onSelect={() => onSelect(name)}
                      weight={weight}
                    />
                  ))}
                </div>
              )
            })}
          </div>
        )}
      </div>

      {/* Clear + cancel */}
      <div className="flex items-center justify-between mt-3">
        <button
          data-testid="clear-icon"
          onClick={() => onSelect('')}
          className="flex items-center gap-1 px-2 py-1 rounded text-xs text-text-secondary hover:text-text-primary hover:bg-surface-hover cursor-pointer"
        >
          <X size={12} />
          Clear
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
