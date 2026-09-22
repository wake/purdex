import { useNewTabLayoutStore } from '../../../stores/useNewTabLayoutStore'
import { colsClass } from '../../../lib/cols-class'
import type { PresetKey } from '../../../lib/resolve-preset'

interface Props { presetKey: PresetKey }

export function NewTabThumbnail({ presetKey }: Props) {
  const preset = useNewTabLayoutStore((s) => s.profiles[presetKey])
  const gridCols = colsClass(preset.columns.length)
  return (
    <div className={`grid gap-[2px] w-16 h-12 ${gridCols}`} aria-hidden="true">
      {preset.columns.map((ids, i) => (
        <div key={i} className="flex flex-col gap-[2px] rounded-sm bg-surface-elevated p-[2px]">
          {ids.slice(0, 6).map((id) => (
            <div key={id} className="h-[3px] rounded-[1px] bg-border-default" />
          ))}
        </div>
      ))}
    </div>
  )
}
