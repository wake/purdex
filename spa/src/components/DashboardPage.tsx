import type { PaneRendererProps } from '../lib/module-registry'
import { useI18nStore } from '../stores/useI18nStore'

// eslint-disable-next-line @typescript-eslint/no-unused-vars
export function DashboardPage(_props: PaneRendererProps) {
  const t = useI18nStore((s) => s.t)
  return (
    <div className="flex-1 flex items-center justify-center text-text-muted text-sm">
      {t('page.dashboard.title')}
    </div>
  )
}
