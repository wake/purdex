import type { ReactNode } from 'react'

export interface RebuildScreenProps {
  icon: ReactNode
  title: string
  description?: string
  /** e.g. the failure reason line */
  detail?: ReactNode
  closeLabel: string
  onClose: () => void
  /** the rebuild block(s) */
  children: ReactNode
  testId?: string
}

/** Shared layout of the "this pane's conversation ended — rebuild it" screens. */
export function RebuildScreen({ icon, title, description, detail, closeLabel, onClose, children, testId }: RebuildScreenProps) {
  return (
    <div
      className="flex flex-col items-center justify-center-safe h-full p-8 text-center overflow-y-auto"
      data-testid={testId}
    >
      {icon}
      <h2 className="text-lg font-medium text-zinc-300 mb-1">{title}</h2>
      {description !== undefined && <p className="text-sm text-zinc-500 mb-6">{description}</p>}
      {detail}
      <button className="text-sm text-zinc-400 hover:text-zinc-200 mb-8" onClick={onClose}>
        {closeLabel}
      </button>
      {children}
    </div>
  )
}
