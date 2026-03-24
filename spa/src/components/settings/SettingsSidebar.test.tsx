import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { SettingsSidebar } from './SettingsSidebar'

describe('SettingsSidebar', () => {
  it('renders all section items', () => {
    render(<SettingsSidebar activeSection="appearance" onSelectSection={vi.fn()} />)
    expect(screen.getByText('Appearance')).toBeTruthy()
    expect(screen.getByText('Terminal')).toBeTruthy()
    expect(screen.getByText('Workspace')).toBeTruthy()
    expect(screen.getByText('Sync')).toBeTruthy()
  })

  it('highlights active section', () => {
    render(<SettingsSidebar activeSection="terminal" onSelectSection={vi.fn()} />)
    const terminalItem = screen.getByText('Terminal').closest('[data-section]')
    expect(terminalItem?.getAttribute('data-active')).toBe('true')
  })

  it('calls onSelectSection for enabled items', () => {
    const onSelect = vi.fn()
    render(<SettingsSidebar activeSection="appearance" onSelectSection={onSelect} />)
    fireEvent.click(screen.getByText('Terminal'))
    expect(onSelect).toHaveBeenCalledWith('terminal')
  })

  it('does not call onSelectSection for reserved items', () => {
    const onSelect = vi.fn()
    render(<SettingsSidebar activeSection="appearance" onSelectSection={onSelect} />)
    fireEvent.click(screen.getByText('Workspace'))
    expect(onSelect).not.toHaveBeenCalled()
  })

  it('shows coming soon badge on reserved items', () => {
    render(<SettingsSidebar activeSection="appearance" onSelectSection={vi.fn()} />)
    const badges = screen.getAllByText('coming soon')
    expect(badges.length).toBe(2)
  })
})
