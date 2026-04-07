import { Check } from '@phosphor-icons/react'
import { useI18nStore } from '../../../stores/useI18nStore'
import { WORKSPACE_COLORS } from '../constants'

interface Props {
  currentColor: string
  onSelect: (color: string) => void
  onCancel: () => void
}

export function WorkspaceColorPicker({ currentColor, onSelect, onCancel }: Props) {
  const t = useI18nStore((s) => s.t)
  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/50">
      <div className="bg-surface-secondary border border-border-default rounded-lg shadow-xl w-full max-w-xs mx-4 p-5">
        <h3 className="text-sm font-semibold text-text-primary mb-3">{t('workspace.change_color')}</h3>
        <div className="grid grid-cols-6 gap-2">
          {WORKSPACE_COLORS.map((color) => (
            <button key={color} data-color={color} aria-pressed={color === currentColor} onClick={() => onSelect(color)}
              className={`w-8 h-8 rounded-full cursor-pointer flex items-center justify-center transition-transform hover:scale-110 ${
                color === currentColor ? 'ring-2 ring-white ring-offset-2 ring-offset-surface-secondary' : ''
              }`} style={{ backgroundColor: color }}>
              {color === currentColor && <Check size={14} className="text-white" />}
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
