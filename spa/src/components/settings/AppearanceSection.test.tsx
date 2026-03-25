import { describe, it, expect, beforeEach } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { AppearanceSection } from './AppearanceSection'
import { useThemeStore } from '../../stores/useThemeStore'
import { registerTheme, clearThemeRegistry } from '../../lib/theme-registry'
import type { ThemeTokens } from '../../lib/theme-tokens'

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
