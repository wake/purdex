import { getNewTabProviders } from '../lib/new-tab-registry'
import type { PaneContent } from '../types/tab'

interface Props {
  onSelect: (content: PaneContent) => void
}

export function NewTabPage({ onSelect }: Props) {
  const providers = getNewTabProviders()

  return (
    <div className="flex-1 flex flex-col items-center justify-start pt-16 gap-8 overflow-y-auto">
      <h2 className="text-lg text-gray-400">New Tab</h2>
      {providers.length === 0 && (
        <p className="text-sm text-gray-600">No content providers registered</p>
      )}
      {providers.map((p) => (
        <section key={p.id} className="w-full max-w-md">
          <h3 className="text-sm font-medium text-gray-400 mb-2 px-2">{p.label}</h3>
          <p.component onSelect={onSelect} />
        </section>
      ))}
    </div>
  )
}
