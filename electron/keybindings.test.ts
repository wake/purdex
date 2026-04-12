import { describe, it, expect, vi } from 'vitest'
import { getDefaultKeybindings, buildMenuTemplate } from './keybindings'

describe('getDefaultKeybindings', () => {
  it('returns a non-empty array of keybindings', () => {
    const bindings = getDefaultKeybindings()
    expect(bindings.length).toBeGreaterThan(0)
  })

  it('returns a copy (not the original array)', () => {
    const a = getDefaultKeybindings()
    const b = getDefaultKeybindings()
    expect(a).not.toBe(b)
    expect(a).toEqual(b)
  })

  it('every binding has required fields', () => {
    for (const b of getDefaultKeybindings()) {
      expect(b.action).toBeTruthy()
      expect(b.accelerator).toBeTruthy()
      expect(b.label).toBeTruthy()
      expect(b.menuCategory).toBeTruthy()
      expect(b.menuGroup).toBeTruthy()
    }
  })

  it('has no duplicate action+accelerator pairs', () => {
    const seen = new Set<string>()
    for (const b of getDefaultKeybindings()) {
      const key = `${b.action}::${b.accelerator}`
      expect(seen.has(key), `duplicate: ${key}`).toBe(false)
      seen.add(key)
    }
  })

  it('mutation of returned array does not affect next call', () => {
    const a = getDefaultKeybindings()
    a.push({
      action: 'fake',
      accelerator: 'X',
      label: 'Fake',
      menuCategory: 'App',
      menuGroup: 'app',
    })
    const b = getDefaultKeybindings()
    expect(b.find((x) => x.action === 'fake')).toBeUndefined()
  })
})

describe('buildMenuTemplate', () => {
  it('returns menu categories in correct order', () => {
    const bindings = getDefaultKeybindings()
    const send = vi.fn()
    const menus = buildMenuTemplate(bindings, send)

    const labels = menus.map((m) => m.label)
    expect(labels).toEqual(['Purdex', 'File', 'Edit', 'Tab', 'Browser', 'View'])
  })

  it('returns exactly 6 top-level menus', () => {
    const bindings = getDefaultKeybindings()
    const send = vi.fn()
    const menus = buildMenuTemplate(bindings, send)
    expect(menus.length).toBe(6)
  })

  it('click handler calls send with action', () => {
    const bindings = getDefaultKeybindings()
    const send = vi.fn()
    const menus = buildMenuTemplate(bindings, send)

    // Find Tab menu → "New Tab" item
    const tabMenu = menus.find((m) => m.label === 'Tab')
    expect(tabMenu).toBeTruthy()
    const items = tabMenu!.submenu as any[]
    const newTabItem = items.find((i: any) => i.label === 'New Tab')
    expect(newTabItem).toBeTruthy()

    newTabItem.click()
    expect(send).toHaveBeenCalledWith('new-tab')
  })

  it('mainHandlers override send for specific actions', () => {
    const bindings = getDefaultKeybindings()
    const send = vi.fn()
    const handler = vi.fn()
    const menus = buildMenuTemplate(bindings, send, { 'new-tab': handler })

    const tabMenu = menus.find((m) => m.label === 'Tab')
    const items = tabMenu!.submenu as any[]
    const newTabItem = items.find((i: any) => i.label === 'New Tab')

    newTabItem.click()
    expect(handler).toHaveBeenCalled()
    expect(send).not.toHaveBeenCalled()
  })

  it('mainHandlers do not affect actions without a handler', () => {
    const bindings = getDefaultKeybindings()
    const send = vi.fn()
    const handler = vi.fn()
    const menus = buildMenuTemplate(bindings, send, { 'new-tab': handler })

    const tabMenu = menus.find((m) => m.label === 'Tab')
    const items = tabMenu!.submenu as any[]
    const closeTabItem = items.find((i: any) => i.label === 'Close Tab')

    closeTabItem.click()
    expect(send).toHaveBeenCalledWith('close-tab')
    expect(handler).not.toHaveBeenCalled()
  })

  it('deduplicates actions — second occurrence is hidden', () => {
    // next-tab appears twice: CommandOrControl+Alt+Right (visible) and Control+Tab (hidden:true)
    // Both hidden:true and isDuplicate result in visible:false
    const bindings = getDefaultKeybindings()
    const send = vi.fn()
    const menus = buildMenuTemplate(bindings, send)

    const tabMenu = menus.find((m) => m.label === 'Tab')
    const items = tabMenu!.submenu as any[]

    // Filter actual items (not separators)
    const nextTabItems = items.filter((i: any) => i.label?.includes('Next Tab'))
    expect(nextTabItems.length).toBeGreaterThanOrEqual(1)

    const visibleNextTab = nextTabItems.filter((i: any) => i.visible !== false)
    expect(visibleNextTab.length).toBe(1)
  })

  it('hidden bindings register accelerator but are not visible', () => {
    const bindings = getDefaultKeybindings()
    const send = vi.fn()
    const menus = buildMenuTemplate(bindings, send)

    const tabMenu = menus.find((m) => m.label === 'Tab')
    const items = tabMenu!.submenu as any[]

    // Control+Tab binding is explicitly hidden
    const ctrlTab = items.find((i: any) => i.accelerator === 'Control+Tab')
    if (ctrlTab) {
      expect(ctrlTab.visible).toBe(false)
    }
  })

  it('Edit menu contains standard roles', () => {
    const bindings = getDefaultKeybindings()
    const send = vi.fn()
    const menus = buildMenuTemplate(bindings, send)

    const editMenu = menus.find((m) => m.label === 'Edit')
    expect(editMenu).toBeTruthy()
    const items = editMenu!.submenu as any[]

    const roles = items.map((i: any) => i.role).filter(Boolean)
    expect(roles).toContain('undo')
    expect(roles).toContain('redo')
    expect(roles).toContain('cut')
    expect(roles).toContain('copy')
    expect(roles).toContain('paste')
    expect(roles).toContain('selectAll')
  })

  it('View menu includes toggleDevTools', () => {
    const bindings = getDefaultKeybindings()
    const send = vi.fn()
    const menus = buildMenuTemplate(bindings, send)

    const viewMenu = menus.find((m) => m.label === 'View')
    expect(viewMenu).toBeTruthy()
    const items = viewMenu!.submenu as any[]

    expect(items.some((i: any) => i.role === 'toggleDevTools')).toBe(true)
  })

  it('App menu always includes quit', () => {
    const bindings = getDefaultKeybindings()
    const send = vi.fn()
    const menus = buildMenuTemplate(bindings, send)

    const appMenu = menus.find((m) => m.label === 'Purdex')
    expect(appMenu).toBeTruthy()
    const items = appMenu!.submenu as any[]

    expect(items.some((i: any) => i.role === 'quit')).toBe(true)
  })

  it('includes macOS-specific items on darwin', () => {
    const bindings = getDefaultKeybindings()
    const send = vi.fn()
    const menus = buildMenuTemplate(bindings, send)
    const appMenu = menus.find((m) => m.label === 'Purdex')
    const items = appMenu!.submenu as any[]

    if (process.platform === 'darwin') {
      expect(items.some((i: any) => i.role === 'about')).toBe(true)
      expect(items.some((i: any) => i.role === 'hide')).toBe(true)
      expect(items.some((i: any) => i.role === 'hideOthers')).toBe(true)
      expect(items.some((i: any) => i.role === 'unhide')).toBe(true)
    } else {
      expect(items.some((i: any) => i.role === 'about')).toBe(false)
      expect(items.some((i: any) => i.role === 'hide')).toBe(false)
    }
  })

  it('filters platform-specific bindings for current platform', () => {
    const bindings = getDefaultKeybindings()
    const send = vi.fn()
    const menus = buildMenuTemplate(bindings, send)

    const browserMenu = menus.find((m) => m.label === 'Browser')
    const items = browserMenu!.submenu as any[]

    if (process.platform === 'darwin') {
      // darwin-only bindings (Command+Left, Command+Right) should be included
      expect(items.some((i: any) => i.accelerator === 'Command+Left')).toBe(true)
      expect(items.some((i: any) => i.accelerator === 'Command+Right')).toBe(true)
    } else {
      // darwin-only bindings should be excluded on non-darwin
      expect(items.some((i: any) => i.accelerator === 'Command+Left')).toBe(false)
      expect(items.some((i: any) => i.accelerator === 'Command+Right')).toBe(false)
    }
  })

  it('works with empty bindings array', () => {
    const send = vi.fn()
    const menus = buildMenuTemplate([], send)

    expect(menus.length).toBe(6)
    // All menus should still have their structure (built-in items like roles)
    const labels = menus.map((m) => m.label)
    expect(labels).toEqual(['Purdex', 'File', 'Edit', 'Tab', 'Browser', 'View'])
  })

  it('works with custom bindings', () => {
    const send = vi.fn()
    const menus = buildMenuTemplate(
      [
        {
          action: 'test-action',
          accelerator: 'CommandOrControl+X',
          label: 'Test Item',
          menuCategory: 'File',
          menuGroup: 'file',
        },
      ],
      send,
    )

    const fileMenu = menus.find((m) => m.label === 'File')
    const items = fileMenu!.submenu as any[]
    const testItem = items.find((i: any) => i.label === 'Test Item')
    expect(testItem).toBeTruthy()
    expect(testItem.accelerator).toBe('CommandOrControl+X')

    testItem.click()
    expect(send).toHaveBeenCalledWith('test-action')
  })
})
