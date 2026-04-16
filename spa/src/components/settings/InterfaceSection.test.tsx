import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { InterfaceSection } from './InterfaceSection'
import {
  clearInterfaceSubsectionRegistry,
  registerInterfaceSubsection,
} from '../../lib/interface-subsection-registry'

const NewTab = () => <div data-testid="nt-body">new-tab-body</div>
const Pane = () => <div data-testid="pane-body">pane-body</div>

beforeEach(() => {
  clearInterfaceSubsectionRegistry()
  registerInterfaceSubsection({ id: 'new-tab', label: 'settings.interface.new_tab', order: 0, component: NewTab })
  registerInterfaceSubsection({ id: 'pane', label: 'settings.interface.pane', order: 1, component: Pane, disabled: true, disabledReason: 'settings.coming_soon' })
})

describe('InterfaceSection', () => {
  it('renders active subsection body', () => {
    render(<InterfaceSection activeSubsection="new-tab" onSelectSubsection={() => {}} />)
    expect(screen.getByTestId('nt-body')).toBeInTheDocument()
  })

  it('shows disabled subsection in nav with "coming soon" hint but does not render body', () => {
    render(<InterfaceSection activeSubsection="new-tab" onSelectSubsection={() => {}} />)
    const paneBtn = screen.getByTestId('interface-subnav-pane')
    expect(paneBtn).toBeInTheDocument()
    expect(screen.queryByTestId('pane-body')).not.toBeInTheDocument()
  })

  it('calls onSelectSubsection on nav click (enabled only)', () => {
    const onSel = vi.fn()
    render(<InterfaceSection activeSubsection="new-tab" onSelectSubsection={onSel} />)
    fireEvent.click(screen.getByTestId('interface-subnav-pane'))
    expect(onSel).not.toHaveBeenCalled()  // disabled

    fireEvent.click(screen.getByTestId('interface-subnav-new-tab'))
    expect(onSel).toHaveBeenCalledWith('new-tab')
  })

  it('renders nothing when active id refers to a disabled subsection', () => {
    render(<InterfaceSection activeSubsection="pane" onSelectSubsection={() => {}} />)
    expect(screen.queryByTestId('nt-body')).not.toBeInTheDocument()
    expect(screen.queryByTestId('pane-body')).not.toBeInTheDocument()
  })
})
