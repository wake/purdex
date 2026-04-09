import { getModules } from '../lib/module-registry'
import type { PaneContent } from '../types/tab'

interface Props {
  onSelect: (content: PaneContent) => void
}

const SIMPLE_KINDS = new Set(['dashboard', 'history', 'hosts', 'memory-monitor'])

export function NewPanePage({ onSelect }: Props) {
  const paneModules = getModules().filter((m) => m.pane && SIMPLE_KINDS.has(m.pane.kind))

  return (
    <div className="flex-1 flex flex-col items-center justify-center p-8">
      <h2 className="text-sm text-text-muted mb-4">Select content for this pane</h2>
      <div className="flex flex-wrap gap-2 max-w-md">
        {paneModules.map((m) => (
          <button
            key={m.id}
            className="px-4 py-2 rounded-lg border border-border-subtle bg-surface-secondary hover:bg-surface-hover text-text-primary text-sm transition-colors"
            onClick={() => onSelect({ kind: m.pane!.kind } as PaneContent)}
          >
            {m.name}
          </button>
        ))}
      </div>
    </div>
  )
}
