import { getInterfaceSubsections } from '../../lib/interface-subsection-registry'
import { InterfaceSubNav } from './InterfaceSubNav'

interface Props {
  activeSubsection: string
  onSelectSubsection: (id: string) => void
}

export function InterfaceSection({ activeSubsection, onSelectSubsection }: Props) {
  const subs = getInterfaceSubsections()
  const selected = subs.find((s) => s.id === activeSubsection)
  return (
    <div className="flex h-full">
      <InterfaceSubNav items={subs} active={activeSubsection} onSelect={onSelectSubsection} />
      <div className="flex-1 overflow-auto">
        {selected && !selected.disabled && <selected.component />}
      </div>
    </div>
  )
}
