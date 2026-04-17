import { useNewTabLayoutStore } from '../../../stores/useNewTabLayoutStore'
import { colsClass } from '../../../lib/cols-class'
import type { ProfileKey } from '../../../lib/resolve-profile'

interface Props { profileKey: ProfileKey }

export function NewTabThumbnail({ profileKey }: Props) {
  const profile = useNewTabLayoutStore((s) => s.profiles[profileKey])
  const gridCols = colsClass(profile.columns.length)
  return (
    <div className={`grid gap-[2px] w-16 h-12 ${gridCols}`} aria-hidden="true">
      {profile.columns.map((ids, i) => (
        <div key={i} className="flex flex-col gap-[2px] rounded-sm bg-surface-elevated p-[2px]">
          {ids.slice(0, 6).map((id) => (
            <div key={id} className="h-[3px] rounded-[1px] bg-border-default" />
          ))}
        </div>
      ))}
    </div>
  )
}
