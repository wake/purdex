// spa/src/lib/conversations/deck-type.ts — the deck's and the chat's type size and line height as CSS variables on the pane root (c9d9c7-60).
// `--deck-font-size` / `--deck-line-height` are set only when the reader chose a value; index.css and the `.deck-t-*` classes read them with
// the built-in numbers as the fallback, so an untouched setting draws exactly what it always did.
import type { CSSProperties } from 'react'
import { useDeckTypeStore } from '../../stores/useDeckTypeStore'

export function deckTypeStyle(fontSize: number | null, lineHeight: number | null): CSSProperties | undefined {
  if (fontSize === null && lineHeight === null) return undefined
  return {
    ...(fontSize !== null ? { '--deck-font-size': `${fontSize}px` } : {}),
    ...(lineHeight !== null ? { '--deck-line-height': String(lineHeight) } : {}),
  } as CSSProperties
}

/** The pane root's `style`: re-renders the pane when the reader moves a slider, so the change is live. */
export function useDeckTypeStyle(): CSSProperties | undefined {
  const fontSize = useDeckTypeStore((s) => s.fontSize)
  const lineHeight = useDeckTypeStore((s) => s.lineHeight)
  return deckTypeStyle(fontSize, lineHeight)
}
