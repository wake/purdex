import type { MenuItemConstructorOptions } from 'electron'

export interface KeybindingDef {
  action: string
  accelerator: string
  label: string
  menuCategory: 'App' | 'Tab' | 'View' | 'Edit'
}

const DEFAULT_KEYBINDINGS: KeybindingDef[] = [
  { action: 'switch-tab-1', accelerator: 'CommandOrControl+1', label: 'Tab 1', menuCategory: 'Tab' },
  { action: 'switch-tab-2', accelerator: 'CommandOrControl+2', label: 'Tab 2', menuCategory: 'Tab' },
  { action: 'switch-tab-3', accelerator: 'CommandOrControl+3', label: 'Tab 3', menuCategory: 'Tab' },
  { action: 'switch-tab-4', accelerator: 'CommandOrControl+4', label: 'Tab 4', menuCategory: 'Tab' },
  { action: 'switch-tab-5', accelerator: 'CommandOrControl+5', label: 'Tab 5', menuCategory: 'Tab' },
  { action: 'switch-tab-6', accelerator: 'CommandOrControl+6', label: 'Tab 6', menuCategory: 'Tab' },
  { action: 'switch-tab-7', accelerator: 'CommandOrControl+7', label: 'Tab 7', menuCategory: 'Tab' },
  { action: 'switch-tab-8', accelerator: 'CommandOrControl+8', label: 'Tab 8', menuCategory: 'Tab' },
  { action: 'switch-tab-last', accelerator: 'CommandOrControl+9', label: 'Last Tab', menuCategory: 'Tab' },
  { action: 'prev-tab', accelerator: 'CommandOrControl+Alt+Left', label: 'Previous Tab', menuCategory: 'Tab' },
  { action: 'next-tab', accelerator: 'CommandOrControl+Alt+Right', label: 'Next Tab', menuCategory: 'Tab' },
  { action: 'reopen-closed-tab', accelerator: 'CommandOrControl+Shift+T', label: 'Reopen Closed Tab', menuCategory: 'Tab' },
  { action: 'open-settings', accelerator: 'CommandOrControl+,', label: 'Settings', menuCategory: 'App' },
  { action: 'open-history', accelerator: 'CommandOrControl+Y', label: 'History', menuCategory: 'View' },
]

export function getDefaultKeybindings(): KeybindingDef[] {
  return DEFAULT_KEYBINDINGS
}

export function buildMenuTemplate(
  bindings: KeybindingDef[],
  send: (action: string) => void,
): MenuItemConstructorOptions[] {
  const byCategory = new Map<string, MenuItemConstructorOptions[]>()
  for (const b of bindings) {
    const items = byCategory.get(b.menuCategory) ?? []
    items.push({
      label: b.label,
      accelerator: b.accelerator,
      click: () => send(b.action),
    })
    byCategory.set(b.menuCategory, items)
  }

  const isMac = process.platform === 'darwin'

  const appMenu: MenuItemConstructorOptions = {
    label: 'tmux-box',
    submenu: [
      ...(isMac ? [{ role: 'about' as const }] : []),
      ...(byCategory.get('App') ?? []),
      { type: 'separator' as const },
      ...(isMac
        ? [
            { role: 'hide' as const },
            { role: 'hideOthers' as const },
            { role: 'unhide' as const },
            { type: 'separator' as const },
          ]
        : []),
      { role: 'quit' as const },
    ],
  }

  const tabItems = byCategory.get('Tab') ?? []
  const tabMenu: MenuItemConstructorOptions = {
    label: 'Tab',
    submenu: [
      ...tabItems.filter((i) => /^Tab \d$/.test((i as { label: string }).label)),
      ...tabItems.filter((i) => (i as { label: string }).label === 'Last Tab'),
      { type: 'separator' as const },
      ...tabItems.filter((i) =>
        ['Previous Tab', 'Next Tab'].includes((i as { label: string }).label),
      ),
      { type: 'separator' as const },
      ...tabItems.filter((i) => (i as { label: string }).label === 'Reopen Closed Tab'),
    ],
  }

  const viewMenu: MenuItemConstructorOptions = {
    label: 'View',
    submenu: [...(byCategory.get('View') ?? [])],
  }

  const editMenu: MenuItemConstructorOptions = {
    label: 'Edit',
    submenu: [
      { role: 'undo' as const },
      { role: 'redo' as const },
      { type: 'separator' as const },
      { role: 'cut' as const },
      { role: 'copy' as const },
      { role: 'paste' as const },
      { role: 'selectAll' as const },
    ],
  }

  return [appMenu, editMenu, tabMenu, viewMenu]
}
