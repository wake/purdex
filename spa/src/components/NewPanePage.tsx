import { getModules } from '../lib/module-registry'
import type { PaneContent } from '../types/tab'

interface Props {
  onSelect: (content: PaneContent) => void
}

const SIMPLE_KINDS = new Set(['dashboard', 'history', 'hosts', 'memory-monitor'])

export function NewPanePage({ onSelect }: Props) {
  const paneKinds = getModules().flatMap((m) =>
    (m.panes ?? [])
      .filter((p) => SIMPLE_KINDS.has(p.kind))
      .map((p) => ({ moduleId: m.id, moduleName: m.name, kind: p.kind }))
  )

  return (
    <div className="flex-1 flex flex-col items-center justify-center p-8">
      <h2 className="text-sm text-text-muted mb-4">Select content for this pane</h2>
      <div className="flex flex-wrap gap-2 max-w-md">
        {paneKinds.map((pk) => (
          <button
            key={pk.kind}
            className="px-4 py-2 rounded-lg border border-border-subtle bg-surface-secondary hover:bg-surface-hover text-text-primary text-sm transition-colors"
            onClick={() => onSelect({ kind: pk.kind } as PaneContent)}
          >
            {pk.moduleName}
          </button>
        ))}
      </div>
    </div>
  )
}
