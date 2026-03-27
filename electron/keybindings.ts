import type { MenuItemConstructorOptions } from 'electron'

export type MenuGroup = 'tab-index' | 'tab-nav' | 'tab-action' | 'app' | 'view'

export interface KeybindingDef {
  action: string
  accelerator: string
  label: string
  menuCategory: 'App' | 'Tab' | 'View' | 'Edit'
  menuGroup: MenuGroup
}

const DEFAULT_KEYBINDINGS: readonly KeybindingDef[] = [
  { action: 'switch-tab-1', accelerator: 'CommandOrControl+1', label: 'Tab 1', menuCategory: 'Tab', menuGroup: 'tab-index' },
  { action: 'switch-tab-2', accelerator: 'CommandOrControl+2', label: 'Tab 2', menuCategory: 'Tab', menuGroup: 'tab-index' },
  { action: 'switch-tab-3', accelerator: 'CommandOrControl+3', label: 'Tab 3', menuCategory: 'Tab', menuGroup: 'tab-index' },
  { action: 'switch-tab-4', accelerator: 'CommandOrControl+4', label: 'Tab 4', menuCategory: 'Tab', menuGroup: 'tab-index' },
  { action: 'switch-tab-5', accelerator: 'CommandOrControl+5', label: 'Tab 5', menuCategory: 'Tab', menuGroup: 'tab-index' },
  { action: 'switch-tab-6', accelerator: 'CommandOrControl+6', label: 'Tab 6', menuCategory: 'Tab', menuGroup: 'tab-index' },
  { action: 'switch-tab-7', accelerator: 'CommandOrControl+7', label: 'Tab 7', menuCategory: 'Tab', menuGroup: 'tab-index' },
  { action: 'switch-tab-8', accelerator: 'CommandOrControl+8', label: 'Tab 8', menuCategory: 'Tab', menuGroup: 'tab-index' },
  { action: 'switch-tab-last', accelerator: 'CommandOrControl+9', label: 'Last Tab', menuCategory: 'Tab', menuGroup: 'tab-index' },
  { action: 'prev-tab', accelerator: 'CommandOrControl+Alt+Left', label: 'Previous Tab', menuCategory: 'Tab', menuGroup: 'tab-nav' },
  { action: 'next-tab', accelerator: 'CommandOrControl+Alt+Right', label: 'Next Tab', menuCategory: 'Tab', menuGroup: 'tab-nav' },
  { action: 'reopen-closed-tab', accelerator: 'CommandOrControl+Shift+T', label: 'Reopen Closed Tab', menuCategory: 'Tab', menuGroup: 'tab-action' },
  { action: 'open-settings', accelerator: 'CommandOrControl+,', label: 'Settings', menuCategory: 'App', menuGroup: 'app' },
  { action: 'open-history', accelerator: 'CommandOrControl+Y', label: 'History', menuCategory: 'View', menuGroup: 'view' },
]

export function getDefaultKeybindings(): KeybindingDef[] {
  return [...DEFAULT_KEYBINDINGS]
}

export function buildMenuTemplate(
  bindings: KeybindingDef[],
  send: (action: string) => void,
): MenuItemConstructorOptions[] {
  const byGroup = new Map<MenuGroup, MenuItemConstructorOptions[]>()
  const byCategory = new Map<string, MenuItemConstructorOptions[]>()
  for (const b of bindings) {
    const item: MenuItemConstructorOptions = {
      label: b.label,
      accelerator: b.accelerator,
      click: () => send(b.action),
    }
    // Group by menuGroup for ordered submenu assembly
    const groupItems = byGroup.get(b.menuGroup) ?? []
    groupItems.push(item)
    byGroup.set(b.menuGroup, groupItems)
    // Group by menuCategory for non-Tab menus
    const catItems = byCategory.get(b.menuCategory) ?? []
    catItems.push(item)
    byCategory.set(b.menuCategory, catItems)
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

  const tabMenu: MenuItemConstructorOptions = {
    label: 'Tab',
    submenu: [
      ...(byGroup.get('tab-index') ?? []),
      { type: 'separator' as const },
      ...(byGroup.get('tab-nav') ?? []),
      { type: 'separator' as const },
      ...(byGroup.get('tab-action') ?? []),
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
