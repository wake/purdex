import { describe, it, expect, beforeEach, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { ThemeEditor } from './ThemeEditor'
import { useThemeStore } from '../../stores/useThemeStore'
import { registerTheme, clearThemeRegistry } from '../../lib/theme-registry'
import type { ThemeTokens } from '../../lib/theme-tokens'

const stubTokens: ThemeTokens = {
  'surface-primary': '#0a0a1a',
  'surface-secondary': '#12122a',
  'surface-tertiary': '#08081a',
  'surface-elevated': '#1e1e3e',
  'surface-hover': '#1a1a32',
  'surface-active': '#272444',
  'surface-input': '#2a2a2a',
  'text-primary': '#e0e0e0',
  'text-secondary': '#9ca3af',
  'text-muted': '#6b7280',
  'text-inverse': '#0a0a1a',
  'border-default': '#404040',
  'border-active': '#7a6aaa',
  'border-subtle': '#2a2a2a',
  'accent': '#7a6aaa',
  'accent-hover': '#8a7aba',
  'accent-muted': 'rgba(122, 106, 170, 0.2)',
  'terminal-bg': '#0a0a1a',
  'terminal-fg': '#e0e0e0',
  'terminal-cursor': '#e0e0e0',
  'status-error': '#4a3038',
  'status-warning': '#4a4028',
  'status-success': '#2a4a3a',
}

describe('ThemeEditor', () => {
  beforeEach(() => {
    clearThemeRegistry()
    registerTheme({ id: 'dark', name: 'Dark', tokens: stubTokens, builtin: true })
    useThemeStore.setState({ activeThemeId: 'dark', customThemes: {} })
  })

  it('shows token inputs grouped by category', () => {
    render(<ThemeEditor baseThemeId="dark" onClose={() => {}} />)

    // Check group headers exist via their toggle buttons
    expect(screen.getByLabelText('Toggle Surface group')).toBeTruthy()
    expect(screen.getByLabelText('Toggle Text group')).toBeTruthy()
    expect(screen.getByLabelText('Toggle Border group')).toBeTruthy()
    expect(screen.getByLabelText('Toggle Accent group')).toBeTruthy()
    expect(screen.getByLabelText('Toggle Terminal group')).toBeTruthy()
    expect(screen.getByLabelText('Toggle Status group')).toBeTruthy()

    // Check some token labels
    expect(screen.getByText('Primary Background')).toBeTruthy()
    expect(screen.getByText('Primary Text')).toBeTruthy()
    expect(screen.getByText('Default Border')).toBeTruthy()
  })

  it('save creates custom theme and activates it', () => {
    const onClose = vi.fn()
    render(<ThemeEditor baseThemeId="dark" onClose={onClose} />)

    // Change name
    const nameInput = screen.getByLabelText('Theme name')
    fireEvent.change(nameInput, { target: { value: 'My Custom' } })

    // Save
    fireEvent.click(screen.getByLabelText('Save theme'))

    // Should have created a custom theme
    const state = useThemeStore.getState()
    const customIds = Object.keys(state.customThemes)
    expect(customIds.length).toBe(1)
    expect(state.customThemes[customIds[0]].name).toBe('My Custom')
    expect(state.activeThemeId).toBe(customIds[0])
    expect(onClose).toHaveBeenCalled()
  })

  it('cancel does not modify store', () => {
    const onClose = vi.fn()
    render(<ThemeEditor baseThemeId="dark" onClose={onClose} />)

    // Change name (but don't save)
    const nameInput = screen.getByLabelText('Theme name')
    fireEvent.change(nameInput, { target: { value: 'Unsaved' } })

    // Cancel
    fireEvent.click(screen.getByText('Cancel'))

    const state = useThemeStore.getState()
    expect(Object.keys(state.customThemes).length).toBe(0)
    expect(state.activeThemeId).toBe('dark')
    expect(onClose).toHaveBeenCalled()
  })

  it('reset restores tokens from base theme', () => {
    render(<ThemeEditor baseThemeId="dark" onClose={() => {}} />)

    // Change a token
    const hexInput = screen.getByLabelText('Primary Background hex value')
    fireEvent.change(hexInput, { target: { value: '#ff0000' } })
    expect(hexInput).toHaveValue('#ff0000')

    // Reset
    fireEvent.click(screen.getByLabelText('Reset to base'))
    expect(screen.getByLabelText('Primary Background hex value')).toHaveValue('#0a0a1a')
  })

  it('collapsing a group hides its tokens', () => {
    render(<ThemeEditor baseThemeId="dark" onClose={() => {}} />)

    // Surface tokens should be visible
    expect(screen.getByText('Primary Background')).toBeTruthy()

    // Collapse Surface group
    fireEvent.click(screen.getByLabelText('Toggle Surface group'))

    // Token label should now be hidden
    expect(screen.queryByText('Primary Background')).toBeNull()
  })
})
