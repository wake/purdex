export interface SettingsSectionDef {
  id: string
  label: string
  order: number
  component?: React.ComponentType
}

const sections: SettingsSectionDef[] = []

export function registerSettingsSection(def: SettingsSectionDef): void {
  const idx = sections.findIndex((s) => s.id === def.id)
  if (idx >= 0) {
    sections[idx] = def
  } else {
    sections.push(def)
  }
  sections.sort((a, b) => a.order - b.order)
}

export function getSettingsSections(): SettingsSectionDef[] {
  return [...sections]
}

export function clearSettingsSectionRegistry(): void {
  sections.length = 0
}
