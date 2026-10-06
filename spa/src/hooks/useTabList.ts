// spa/src/hooks/useTabList.ts — the WAI-ARIA tabs keyboard model, shared by the small tab strips
// (Settings → Worker tabs, the New Tab Sessions / Workers switch): roving tabindex (only the selected tab is a
// tab stop), ArrowLeft / ArrowRight (wrapping) and Home / End move focus AND select, and every tab points at the one
// panel (`aria-controls`) that is labelled by the selected tab.
import { useId, useRef } from 'react'
import type { KeyboardEvent } from 'react'

export function useTabList<T extends string>(ids: readonly T[], selected: T, onSelect: (id: T) => void) {
  const base = useId()
  const refs = useRef<Map<T, HTMLElement | null>>(new Map())
  const tabId = (id: T) => `${base}-tab-${id}`
  const panelId = `${base}-panel`

  const move = (id: T) => {
    onSelect(id)
    refs.current.get(id)?.focus()
  }

  const onKeyDown = (e: KeyboardEvent, from: T) => {
    const i = ids.indexOf(from)
    let next: T | undefined
    if (e.key === 'ArrowRight') next = ids[(i + 1) % ids.length]
    else if (e.key === 'ArrowLeft') next = ids[(i - 1 + ids.length) % ids.length]
    else if (e.key === 'Home') next = ids[0]
    else if (e.key === 'End') next = ids[ids.length - 1]
    else return
    e.preventDefault()
    if (next !== undefined) move(next)
  }

  return {
    tabProps: (id: T) => ({
      id: tabId(id),
      role: 'tab' as const,
      'aria-selected': id === selected,
      'aria-controls': panelId,
      tabIndex: id === selected ? 0 : -1,
      ref: (el: HTMLElement | null) => { refs.current.set(id, el) },
      onKeyDown: (e: KeyboardEvent) => onKeyDown(e, id),
    }),
    panelProps: {
      id: panelId,
      role: 'tabpanel' as const,
      'aria-labelledby': tabId(selected),
    },
  }
}
