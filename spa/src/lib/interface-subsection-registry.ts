import type { ComponentType } from 'react'

interface InterfaceSubsectionBase {
  id: string
  label: string          // i18n key
  order: number
  component: ComponentType
}

export type InterfaceSubsection =
  | (InterfaceSubsectionBase & { disabled?: false; disabledReason?: never })
  | (InterfaceSubsectionBase & { disabled: true; disabledReason: string })

const subsections: InterfaceSubsection[] = []

export function registerInterfaceSubsection(def: InterfaceSubsection): void {
  const idx = subsections.findIndex((s) => s.id === def.id)
  if (idx >= 0) {
    subsections[idx] = def
  } else {
    subsections.push(def)
  }
  subsections.sort((a, b) => a.order - b.order)
}

export function getInterfaceSubsections(): InterfaceSubsection[] {
  return [...subsections]
}

export function clearInterfaceSubsectionRegistry(): void {
  subsections.length = 0
}
