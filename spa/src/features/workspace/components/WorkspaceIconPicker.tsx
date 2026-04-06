import { useI18nStore } from '../../../stores/useI18nStore'
import { WORKSPACE_ICONS } from '../constants'

interface Props {
  currentIcon: string | undefined
  onSelect: (icon: string) => void
  onCancel: () => void
}

export function WorkspaceIconPicker({ currentIcon, onSelect, onCancel }: Props) {
  const t = useI18nStore((s) => s.t)
  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/50">
      <div className="bg-surface-secondary border border-border-default rounded-lg shadow-xl w-full max-w-xs mx-4 p-5">
        <h3 className="text-sm font-semibold text-text-primary mb-3">{t('workspace.change_icon')}</h3>
        <div className="grid grid-cols-7 gap-2">
          {WORKSPACE_ICONS.map((icon) => (
            <button key={icon} data-icon={icon} aria-pressed={icon === currentIcon} onClick={() => onSelect(icon)}
              className={`w-8 h-8 rounded-md flex items-center justify-center text-sm cursor-pointer transition-colors ${
                icon === currentIcon ? 'bg-accent/20 ring-2 ring-accent text-text-primary' : 'bg-surface-tertiary text-text-secondary hover:text-text-primary hover:bg-surface-hover'
              }`}>
              {icon}
            </button>
          ))}
        </div>
        <div className="flex justify-end mt-4">
          <button onClick={onCancel} className="px-3 py-1.5 rounded text-xs bg-surface-tertiary text-text-secondary hover:text-text-primary cursor-pointer">
            {t('common.cancel')}
          </button>
        </div>
      </div>
    </div>
  )
}
