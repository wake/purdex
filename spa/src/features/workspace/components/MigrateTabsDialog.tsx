import { ArrowRight, SkipForward } from '@phosphor-icons/react'
import { useI18nStore } from '../../../stores/useI18nStore'

interface Props {
  tabCount: number
  workspaceName: string
  onMigrate: () => void
  onSkip: () => void
}

export function MigrateTabsDialog({ tabCount, workspaceName, onMigrate, onSkip }: Props) {
  const t = useI18nStore((s) => s.t)

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/50">
      <div className="bg-surface-secondary border border-border-default rounded-lg shadow-xl w-full max-w-sm mx-4 p-5">
        <h3 className="text-sm font-semibold text-text-primary mb-2">
          {t('workspace.migrate_title')}
        </h3>
        <p className="text-xs text-text-muted mb-4">
          {t('workspace.migrate_description', { count: tabCount, name: workspaceName })}
        </p>
        <div className="flex justify-end gap-2">
          <button onClick={onSkip}
            className="px-3 py-1.5 rounded text-xs bg-surface-tertiary text-text-secondary hover:text-text-primary cursor-pointer flex items-center gap-1.5">
            <SkipForward size={14} />
            {t('workspace.migrate_skip')}
          </button>
          <button onClick={onMigrate}
            className="px-3 py-1.5 rounded text-xs bg-accent text-white hover:bg-accent/80 cursor-pointer flex items-center gap-1.5">
            <ArrowRight size={14} />
            {t('workspace.migrate_move')}
          </button>
        </div>
      </div>
    </div>
  )
}
