import { describe, it, expect, beforeEach, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { ThemeImportModal } from './ThemeImportModal'
import { parseAndValidate } from '../../lib/theme-import'
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

describe('parseAndValidate', () => {
  it('returns error for null input', () => {
    expect(parseAndValidate(null)).toBe('Invalid JSON')
  })

  it('returns error for non-object input', () => {
    expect(parseAndValidate('string')).toBe('Invalid JSON')
  })

  it('returns error for missing name', () => {
    expect(parseAndValidate({ tokens: { 'accent': '#fff' } })).toBe('Missing "name" field')
  })

  it('returns error for empty name', () => {
    expect(parseAndValidate({ name: '  ', tokens: { 'accent': '#fff' } })).toBe('Missing "name" field')
  })

  it('returns error for missing tokens', () => {
    expect(parseAndValidate({ name: 'Test' })).toBe('Missing "tokens" field')
  })

  it('returns error for no valid token keys', () => {
    expect(parseAndValidate({ name: 'Test', tokens: { 'invalid-key': '#fff' } })).toBe('No valid token keys found')
  })

  it('returns payload for valid input', () => {
    const result = parseAndValidate({ name: 'Test', tokens: { 'accent': '#ff0000' } })
    expect(typeof result).toBe('object')
    expect(result).toHaveProperty('name', 'Test')
    expect(result).toHaveProperty('tokens')
  })

  it('filters out invalid token keys', () => {
    const result = parseAndValidate({
      name: 'Test',
      tokens: { 'accent': '#ff0000', 'not-a-key': '#000' },
    })
    expect(typeof result).toBe('object')
    if (typeof result !== 'string') {
      expect(result.tokens['accent' as keyof typeof result.tokens]).toBe('#ff0000')
      expect(Object.keys(result.tokens)).not.toContain('not-a-key')
    }
  })
})

describe('ThemeImportModal', () => {
  beforeEach(() => {
    clearThemeRegistry()
    registerTheme({ id: 'dark', name: 'Dark', tokens: stubTokens, builtin: true })
    useThemeStore.setState({ activeThemeId: 'dark', customThemes: {} })
  })

  it('valid JSON imports successfully', () => {
    const onClose = vi.fn()
    const onImported = vi.fn()
    render(<ThemeImportModal onClose={onClose} onImported={onImported} />)

    const textarea = screen.getByLabelText('Theme JSON')
    const validJson = JSON.stringify({
      name: 'Imported Theme',
      tokens: { 'accent': '#ff0000', 'accent-hover': '#ff3333' },
    })
    fireEvent.change(textarea, { target: { value: validJson } })
    fireEvent.click(screen.getByText('Import'))

    expect(onImported).toHaveBeenCalled()
    expect(onClose).toHaveBeenCalled()
    const state = useThemeStore.getState()
    const customIds = Object.keys(state.customThemes)
    expect(customIds.length).toBe(1)
    expect(state.customThemes[customIds[0]].name).toBe('Imported Theme')
  })

  it('invalid JSON shows error', () => {
    const onClose = vi.fn()
    const onImported = vi.fn()
    render(<ThemeImportModal onClose={onClose} onImported={onImported} />)

    const textarea = screen.getByLabelText('Theme JSON')
    fireEvent.change(textarea, { target: { value: 'not json' } })
    fireEvent.click(screen.getByText('Import'))

    expect(screen.getByTestId('import-error')).toBeTruthy()
    expect(screen.getByTestId('import-error').textContent).toBe('Invalid JSON syntax')
    expect(onImported).not.toHaveBeenCalled()
    expect(onClose).not.toHaveBeenCalled()
  })

  it('missing name shows error', () => {
    const onClose = vi.fn()
    const onImported = vi.fn()
    render(<ThemeImportModal onClose={onClose} onImported={onImported} />)

    const textarea = screen.getByLabelText('Theme JSON')
    fireEvent.change(textarea, { target: { value: '{"tokens":{"accent":"#fff"}}' } })
    fireEvent.click(screen.getByText('Import'))

    expect(screen.getByTestId('import-error').textContent).toBe('Missing "name" field')
    expect(onImported).not.toHaveBeenCalled()
  })

  it('renders all three tabs', () => {
    render(<ThemeImportModal onClose={() => {}} onImported={() => {}} />)
    expect(screen.getByText('Paste JSON')).toBeTruthy()
    expect(screen.getByText('File')).toBeTruthy()
    expect(screen.getByText('URL')).toBeTruthy()
  })

  it('close button calls onClose', () => {
    const onClose = vi.fn()
    render(<ThemeImportModal onClose={onClose} onImported={() => {}} />)
    fireEvent.click(screen.getByLabelText('Close import modal'))
    expect(onClose).toHaveBeenCalled()
  })
})
