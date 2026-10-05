import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { InterfaceSubNav } from './InterfaceSubNav'
import type { InterfaceSubsection } from '../../lib/interface-subsection-registry'

// Settings → Interface has a single sub-tab since the Pane / Sidebar
// coming-soon stubs were removed (shell cleanup spec §7).
describe('InterfaceSubNav with a single sub-tab', () => {
  const newTab: InterfaceSubsection = {
    id: 'new-tab',
    label: 'settings.interface.new_tab',
    order: 0,
    component: () => null,
  }

  it('renders exactly one enabled, active button labelled New Tab', () => {
    render(<InterfaceSubNav items={[newTab]} active="new-tab" onSelect={() => {}} />)
    const buttons = screen.getAllByRole('button')
    expect(buttons).toHaveLength(1)
    const btn = screen.getByTestId('interface-subnav-new-tab')
    expect(btn).toBe(buttons[0])
    expect(btn).not.toBeDisabled()
    expect(btn).toHaveAttribute('data-active', 'true')
    expect(btn).toHaveTextContent('New Tab')
    expect(btn).not.toHaveTextContent('coming soon')
  })

  it('selecting the only sub-tab reports its id', () => {
    const onSelect = vi.fn()
    render(<InterfaceSubNav items={[newTab]} active="new-tab" onSelect={onSelect} />)
    fireEvent.click(screen.getByTestId('interface-subnav-new-tab'))
    expect(onSelect).toHaveBeenCalledWith('new-tab')
  })
})
