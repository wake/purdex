import { getNewTabProviders } from '../lib/new-tab-registry'
import { useI18nStore } from '../stores/useI18nStore'
import type { PaneContent } from '../types/tab'

interface Props {
  onSelect: (content: PaneContent) => void
}

export function NewTabPage({ onSelect }: Props) {
  const t = useI18nStore((s) => s.t)
  const providers = getNewTabProviders()

  return (
    <div className="flex-1 flex flex-col items-center justify-start pt-16 gap-8 overflow-y-auto">
      <h2 className="text-lg text-text-secondary">{t('page.newtab.title')}</h2>
      {providers.length === 0 && (
        <p className="text-sm text-text-secondary">{t('page.newtab.empty')}</p>
      )}
      {providers.map((p) => (
        <section key={p.id} className="w-full max-w-md">
          <h3 className="text-sm font-medium text-text-secondary mb-2 px-2">{t(p.label)}</h3>
          <p.component onSelect={onSelect} />
        </section>
      ))}
    </div>
  )
}
