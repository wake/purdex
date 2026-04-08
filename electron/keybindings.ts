import type { MenuItemConstructorOptions } from 'electron'

export type MenuGroup = 'tab-index' | 'tab-nav' | 'tab-action' | 'workspace-nav' | 'app' | 'view' | 'file'

export interface KeybindingDef {
  action: string
  accelerator: string
  label: string
  menuCategory: 'App' | 'File' | 'Tab' | 'View' | 'Edit'
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
  // Note: Control+Tab may conflict with macOS "Move focus to next window" in some keyboard settings
  { action: 'next-tab', accelerator: 'Control+Tab', label: 'Next Tab (Ctrl)', menuCategory: 'Tab', menuGroup: 'tab-nav' },
  { action: 'prev-tab', accelerator: 'Control+Shift+Tab', label: 'Previous Tab (Ctrl)', menuCategory: 'Tab', menuGroup: 'tab-nav' },
  { action: 'new-tab', accelerator: 'CommandOrControl+T', label: 'New Tab', menuCategory: 'Tab', menuGroup: 'tab-action' },
  { action: 'close-tab', accelerator: 'CommandOrControl+W', label: 'Close Tab', menuCategory: 'Tab', menuGroup: 'tab-action' },
  { action: 'reopen-closed-tab', accelerator: 'CommandOrControl+Shift+T', label: 'Reopen Closed Tab', menuCategory: 'Tab', menuGroup: 'tab-action' },
  { action: 'open-settings', accelerator: 'CommandOrControl+,', label: 'Settings', menuCategory: 'App', menuGroup: 'app' },
  { action: 'open-history', accelerator: 'CommandOrControl+Y', label: 'History', menuCategory: 'View', menuGroup: 'view' },
  // Workspace navigation
  { action: 'switch-workspace-1', accelerator: 'CommandOrControl+Alt+1', label: 'Workspace 1', menuCategory: 'Tab', menuGroup: 'workspace-nav' },
  { action: 'switch-workspace-2', accelerator: 'CommandOrControl+Alt+2', label: 'Workspace 2', menuCategory: 'Tab', menuGroup: 'workspace-nav' },
  { action: 'switch-workspace-3', accelerator: 'CommandOrControl+Alt+3', label: 'Workspace 3', menuCategory: 'Tab', menuGroup: 'workspace-nav' },
  { action: 'switch-workspace-4', accelerator: 'CommandOrControl+Alt+4', label: 'Workspace 4', menuCategory: 'Tab', menuGroup: 'workspace-nav' },
  { action: 'switch-workspace-5', accelerator: 'CommandOrControl+Alt+5', label: 'Workspace 5', menuCategory: 'Tab', menuGroup: 'workspace-nav' },
  { action: 'switch-workspace-6', accelerator: 'CommandOrControl+Alt+6', label: 'Workspace 6', menuCategory: 'Tab', menuGroup: 'workspace-nav' },
  { action: 'switch-workspace-7', accelerator: 'CommandOrControl+Alt+7', label: 'Workspace 7', menuCategory: 'Tab', menuGroup: 'workspace-nav' },
  { action: 'switch-workspace-8', accelerator: 'CommandOrControl+Alt+8', label: 'Workspace 8', menuCategory: 'Tab', menuGroup: 'workspace-nav' },
  { action: 'switch-workspace-9', accelerator: 'CommandOrControl+Alt+9', label: 'Workspace 9', menuCategory: 'Tab', menuGroup: 'workspace-nav' },
  { action: 'prev-workspace', accelerator: 'CommandOrControl+Alt+Up', label: 'Previous Workspace', menuCategory: 'Tab', menuGroup: 'workspace-nav' },
  { action: 'next-workspace', accelerator: 'CommandOrControl+Alt+Down', label: 'Next Workspace', menuCategory: 'Tab', menuGroup: 'workspace-nav' },
  // File
  { action: 'new-window', accelerator: 'CommandOrControl+N', label: 'New Window', menuCategory: 'File', menuGroup: 'file' },
]

export function getDefaultKeybindings(): KeybindingDef[] {
  return [...DEFAULT_KEYBINDINGS]
}

export function buildMenuTemplate(
  bindings: KeybindingDef[],
  send: (action: string) => void,
  mainHandlers?: Record<string, () => void>,
): MenuItemConstructorOptions[] {
  const byGroup = new Map<MenuGroup, MenuItemConstructorOptions[]>()
  const byCategory = new Map<string, MenuItemConstructorOptions[]>()
  for (const b of bindings) {
    const handler = mainHandlers?.[b.action]
    const item: MenuItemConstructorOptions = {
      label: b.label,
      accelerator: b.accelerator,
      click: handler ?? (() => send(b.action)),
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
      { type: 'separator' as const },
      ...(byGroup.get('workspace-nav') ?? []),
    ],
  }

  const fileMenu: MenuItemConstructorOptions = {
    label: 'File',
    submenu: [...(byGroup.get('file') ?? [])],
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

  return [appMenu, fileMenu, editMenu, tabMenu, viewMenu]
}
