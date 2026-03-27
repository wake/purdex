import { describe, it, expect, beforeEach } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { SettingsPage } from './SettingsPage'
import { registerSettingsSection, clearSettingsSectionRegistry } from '../lib/settings-section-registry'
import { AppearanceSection } from './settings/AppearanceSection'
import { TerminalSection } from './settings/TerminalSection'
import type { Pane } from '../types/tab'

const settingsPane: Pane = {
  id: 'pane-set',
  content: { kind: 'settings', scope: 'global' },
}

describe('SettingsPage', () => {
  beforeEach(() => {
    clearSettingsSectionRegistry()
    registerSettingsSection({ id: 'appearance', label: 'Appearance', order: 0, component: AppearanceSection })
    registerSettingsSection({ id: 'terminal', label: 'Terminal', order: 1, component: TerminalSection })
    registerSettingsSection({ id: 'workspace', label: 'Workspace', order: 10 })
    registerSettingsSection({ id: 'sync', label: 'Sync', order: 11 })
  })

  it('renders sidebar and default appearance section', () => {
    render(<SettingsPage pane={settingsPane} isActive />)
    expect(screen.getAllByText('Appearance').length).toBeGreaterThan(0)
    expect(screen.getByText('Terminal')).toBeTruthy()
    expect(screen.getByText('Visual preferences for the application')).toBeTruthy()
  })

  it('switches to terminal section on sidebar click', () => {
    render(<SettingsPage pane={settingsPane} isActive />)
    fireEvent.click(screen.getByText('Terminal'))
    expect(screen.getByText('Terminal rendering and connection settings')).toBeTruthy()
  })

  it('preserves section across re-renders', () => {
    const { rerender } = render(<SettingsPage pane={settingsPane} isActive />)
    fireEvent.click(screen.getByText('Terminal'))
    expect(screen.getByText('Terminal rendering and connection settings')).toBeTruthy()
    // Re-render (simulates tab switch back)
    rerender(<SettingsPage pane={settingsPane} isActive />)
    expect(screen.getByText('Terminal rendering and connection settings')).toBeTruthy()
  })
})
