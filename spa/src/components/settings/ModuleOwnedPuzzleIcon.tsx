import { PuzzlePiece } from '@phosphor-icons/react'

/**
 * The marker on a settings row that a module owns. One component so the three sidebars cannot drift (#822); the
 * Hosts sidebar deliberately asks for the brighter `secondary` tone and a smaller size.
 */
export function ModuleOwnedPuzzleIcon({ size = 12, tone = 'muted' }: { size?: number; tone?: 'muted' | 'secondary' }) {
  return (
    <PuzzlePiece
      size={size}
      weight="bold"
      className={`flex-shrink-0 ${tone === 'secondary' ? 'text-text-secondary' : 'text-text-muted'}`}
      aria-hidden
    />
  )
}
