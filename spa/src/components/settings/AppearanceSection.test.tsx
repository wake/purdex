import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { AppearanceSection } from './AppearanceSection'
import { useThemeStore } from '../../stores/useThemeStore'
import { registerTheme, clearThemeRegistry } from '../../lib/theme-registry'
import type { ThemeTokens } from '../../lib/theme-tokens'

function stubNotification(permission: NotificationPermission, requestPermission: unknown = vi.fn().mockResolvedValue('granted')) {
  Object.defineProperty(window, 'Notification', {
    configurable: true, writable: true,
    value: Object.assign(function () {}, { permission, requestPermission }),
  })
}

const stubTokens: ThemeTokens = {
  'surface-primary': '#000',
  'surface-secondary': '#111',
  'surface-tertiary': '#222',
  'surface-elevated': '#333',
  'surface-hover': '#444',
  'surface-active': '#555',
  'surface-input': '#666',
  'text-primary': '#fff',
  'text-secondary': '#eee',
  'text-muted': '#ddd',
  'text-inverse': '#ccc',
  'border-default': '#bbb',
  'border-active': '#aaa',
  'border-subtle': '#999',
  'accent': '#888',
  'accent-hover': '#777',
  'accent-muted': '#666',
  'terminal-bg': '#000',
  'terminal-fg': '#fff',
  'terminal-cursor': '#fff',
  'status-error': '#f00',
  'status-warning': '#ff0',
  'status-success': '#0f0',
}

describe('AppearanceSection', () => {
  beforeEach(() => {
    clearThemeRegistry()
    registerTheme({ id: 'dark', name: 'Dark', tokens: stubTokens, builtin: true })
    registerTheme({ id: 'light', name: 'Light', tokens: stubTokens, builtin: true })
    registerTheme({ id: 'nord', name: 'Nord', tokens: stubTokens, builtin: true })
    registerTheme({ id: 'dracula', name: 'Dracula', tokens: stubTokens, builtin: true })
    useThemeStore.setState({ activeThemeId: 'dark', customThemes: {} })
  })

  it('renders section title', () => {
    render(<AppearanceSection />)
    expect(screen.getByText('Appearance')).toBeTruthy()
  })

  it('renders theme dropdown with preset themes', () => {
    render(<AppearanceSection />)
    const select = screen.getByLabelText('Theme') as HTMLSelectElement
    expect(select).toBeTruthy()
    expect(select.value).toBe('dark')
    // Check all preset options exist
    expect(screen.getByText('Dark')).toBeTruthy()
    expect(screen.getByText('Light')).toBeTruthy()
    expect(screen.getByText('Nord')).toBeTruthy()
    expect(screen.getByText('Dracula')).toBeTruthy()
  })

  it('changes theme on select', () => {
    render(<AppearanceSection />)
    const select = screen.getByLabelText('Theme') as HTMLSelectElement
    fireEvent.change(select, { target: { value: 'light' } })
    expect(useThemeStore.getState().activeThemeId).toBe('light')
  })

  it('shows Customize and Import buttons', () => {
    render(<AppearanceSection />)
    // There are two "Customize" buttons: one for theme, one for locale
    const customizeBtns = screen.getAllByLabelText('Customize')
    expect(customizeBtns.length).toBeGreaterThanOrEqual(1)
    expect(screen.getByLabelText('Import theme')).toBeTruthy()
  })

  it('renders language setting with enabled select', () => {
    render(<AppearanceSection />)
    expect(screen.getByText('Language')).toBeTruthy()
    const selectEl = screen.getByLabelText('Language') as HTMLSelectElement
    expect(selectEl).toHaveProperty('disabled', false)
  })

  it('shows delete button for custom themes', () => {
    // Register a custom theme
    const customId = useThemeStore.getState().createCustomTheme('My Theme', 'dark', {})
    useThemeStore.getState().setActiveTheme(customId)

    render(<AppearanceSection />)
    expect(screen.getByLabelText('Delete theme')).toBeTruthy()
  })

  it('deletes custom theme and falls back to dark', () => {
    const customId = useThemeStore.getState().createCustomTheme('My Theme', 'dark', {})
    useThemeStore.getState().setActiveTheme(customId)

    render(<AppearanceSection />)
    fireEvent.click(screen.getByLabelText('Delete theme'))
    expect(useThemeStore.getState().activeThemeId).toBe('dark')
  })

  it('shows export button for custom themes', () => {
    const customId = useThemeStore.getState().createCustomTheme('My Theme', 'dark', {})
    useThemeStore.getState().setActiveTheme(customId)

    render(<AppearanceSection />)
    expect(screen.getByLabelText('Export theme')).toBeTruthy()
  })

  it('does not show export/delete buttons for preset themes', () => {
    render(<AppearanceSection />)
    expect(screen.queryByLabelText('Export theme')).toBeNull()
    expect(screen.queryByLabelText('Delete theme')).toBeNull()
  })
})

describe('AppearanceSection web notification re-entry', () => {
  let originalNotificationDescriptor: PropertyDescriptor | undefined
  let originalElectronAPIDescriptor: PropertyDescriptor | undefined

  beforeEach(() => {
    clearThemeRegistry()
    registerTheme({ id: 'dark', name: 'Dark', tokens: stubTokens, builtin: true })
    useThemeStore.setState({ activeThemeId: 'dark', customThemes: {} })

    originalNotificationDescriptor = Object.getOwnPropertyDescriptor(window, 'Notification')
    originalElectronAPIDescriptor = Object.getOwnPropertyDescriptor(window, 'electronAPI')
    delete (window as unknown as { electronAPI?: unknown }).electronAPI
  })

  afterEach(() => {
    if (originalNotificationDescriptor) {
      Object.defineProperty(window, 'Notification', originalNotificationDescriptor)
    } else {
      delete (window as unknown as { Notification?: unknown }).Notification
    }
    if (originalElectronAPIDescriptor) {
      Object.defineProperty(window, 'electronAPI', originalElectronAPIDescriptor)
    } else {
      delete (window as unknown as { electronAPI?: unknown }).electronAPI
    }
  })

  it('unsupported: Notification not in window → row not rendered', () => {
    delete (window as unknown as { Notification?: unknown }).Notification
    render(<AppearanceSection />)
    expect(screen.queryByText('Browser notifications')).toBeNull()
    expect(screen.queryByRole('button', { name: 'Enable' })).toBeNull()
  })

  it('default: button enabled; clicking requests permission and updates to granted', async () => {
    const req = vi.fn().mockResolvedValue('granted')
    stubNotification('default', req)
    render(<AppearanceSection />)

    const btn = screen.getByRole('button', { name: 'Enable' }) as HTMLButtonElement
    expect(btn.disabled).toBe(false)

    fireEvent.click(btn)
    expect(req).toHaveBeenCalled()

    await waitFor(() => {
      expect(screen.getByRole('button', { name: 'Enabled' })).toBeTruthy()
    })
    expect((screen.getByRole('button', { name: 'Enabled' }) as HTMLButtonElement).disabled).toBe(true)
  })

  it('granted: button rendered disabled with granted label', () => {
    stubNotification('granted')
    render(<AppearanceSection />)
    const btn = screen.getByRole('button', { name: 'Enabled' }) as HTMLButtonElement
    expect(btn).toBeTruthy()
    expect(btn.disabled).toBe(true)
  })

  it('denied: button rendered disabled with denied label', () => {
    stubNotification('denied')
    render(<AppearanceSection />)
    const btn = screen.getByRole('button', { name: 'Blocked (change in browser settings)' }) as HTMLButtonElement
    expect(btn).toBeTruthy()
    expect(btn.disabled).toBe(true)
  })
})
