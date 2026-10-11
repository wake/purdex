// spa/src/components/deck/take-back-ui.ts — what makes a whole block a "click to take it back" button (K-2b): role, focus, pointer, Enter / Space.
import type { KeyboardEvent } from 'react'

export function activateProps(onActivate: () => void, title: string) {
  return {
    role: 'button' as const, tabIndex: 0, title,
    onClick: onActivate,
    onKeyDown: (e: KeyboardEvent) => { if (e.target === e.currentTarget && (e.key === 'Enter' || e.key === ' ')) { e.preventDefault(); onActivate() } },
  }
}

export const TAKE_BACK_BOX = 'cursor-pointer hover:bg-accent/20 focus-visible:outline focus-visible:outline-1 focus-visible:outline-accent'
