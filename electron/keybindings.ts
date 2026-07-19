import type { MenuItemConstructorOptions } from 'electron'
import { getDefaultKeybindings, type KeybindingDef, type MenuGroup } from './keybindings-manifest'

// The keybinding data itself lives in ./keybindings-manifest (electron-free so
// the SPA parity test can import it); re-exported here so existing importers
// keep working unchanged.
export { getDefaultKeybindings }
export type { KeybindingDef, MenuGroup }

export function buildMenuTemplate(
  bindings: KeybindingDef[],
  send: (action: string) => void,
  mainHandlers?: Record<string, () => void>,
): MenuItemConstructorOptions[] {
  // Platform filter
  const platform = process.platform
  const filtered = bindings.filter((b) => !b.platform || b.platform === platform)

  // Build menu items with dedup: first-seen action gets visible item, rest get hidden (accelerator-only)
  const byGroup = new Map<MenuGroup, MenuItemConstructorOptions[]>()
  const byCategory = new Map<string, MenuItemConstructorOptions[]>()
  const seenActions = new Set<string>()

  for (const b of filtered) {
    const handler = mainHandlers?.[b.action]
    const isDuplicate = seenActions.has(b.action)
    seenActions.add(b.action)

    const item: MenuItemConstructorOptions = {
      label: b.label,
      accelerator: b.accelerator,
      click: handler ?? (() => send(b.action)),
      ...((b.hidden || isDuplicate) ? { visible: false } : {}),
    }

    const groupItems = byGroup.get(b.menuGroup) ?? []
    groupItems.push(item)
    byGroup.set(b.menuGroup, groupItems)

    const catItems = byCategory.get(b.menuCategory) ?? []
    catItems.push(item)
    byCategory.set(b.menuCategory, catItems)
  }

  const isMac = platform === 'darwin'

  const appMenu: MenuItemConstructorOptions = {
    label: 'Purdex',
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
    submenu: [
      ...(byCategory.get('View') ?? []),
      { type: 'separator' as const },
      { role: 'toggleDevTools' as const },
    ],
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

  const browserMenu: MenuItemConstructorOptions = {
    label: 'Browser',
    submenu: [...(byGroup.get('browser') ?? [])],
  }

  return [appMenu, fileMenu, editMenu, tabMenu, browserMenu, viewMenu]
}
