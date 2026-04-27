import { describe, it, expect, beforeEach, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { DisabledModulePlaceholder } from './DisabledModulePlaceholder'
import { useModuleEnabledStore } from '../../stores/useModuleEnabledStore'

describe('DisabledModulePlaceholder', () => {
  beforeEach(() => {
    useModuleEnabledStore.setState({ enabled: {}, baseline: null })
  })

  it('shows the moduleId in the title and the paneKind in the body', () => {
    render(<DisabledModulePlaceholder moduleId="my-mod" paneKind="my-pane" />)
    expect(screen.getByRole('heading')).toHaveTextContent(/my-mod/)
    expect(screen.getByText(/my-pane/)).toBeInTheDocument()
  })

  it('renders an enable button with an accessible label', () => {
    render(<DisabledModulePlaceholder moduleId="editor" paneKind="editor" />)
    const btn = screen.getByRole('button', { name: /editor/i })
    expect(btn).toBeInTheDocument()
  })

  it('clicking the enable button calls setEnabled(moduleId, true)', () => {
    const setEnabledSpy = vi.spyOn(useModuleEnabledStore.getState(), 'setEnabled')
    render(<DisabledModulePlaceholder moduleId="editor" paneKind="editor" />)
    fireEvent.click(screen.getByRole('button', { name: /editor/i }))
    expect(setEnabledSpy).toHaveBeenCalledWith('editor', true)
  })
})
