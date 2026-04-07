import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { BrowserToolbar } from '../BrowserToolbar'

function makeProps(overrides: Partial<Parameters<typeof BrowserToolbar>[0]> = {}) {
  return {
    url: 'https://github.com/',
    canGoBack: true,
    canGoForward: false,
    isLoading: false,
    context: 'tab' as const,
    onGoBack: vi.fn(),
    onGoForward: vi.fn(),
    onReload: vi.fn(),
    onStop: vi.fn(),
    onNavigate: vi.fn(),
    onOpenExternal: vi.fn(),
    onCopyUrl: vi.fn(),
    ...overrides,
  }
}

describe('BrowserToolbar', () => {
  it('renders back, forward, reload buttons', () => {
    render(<BrowserToolbar {...makeProps()} />)
    expect(screen.getByLabelText('Go back')).toBeInTheDocument()
    expect(screen.getByLabelText('Go forward')).toBeInTheDocument()
    expect(screen.getByLabelText('Reload')).toBeInTheDocument()
  })

  it('disables forward button when canGoForward is false', () => {
    render(<BrowserToolbar {...makeProps({ canGoForward: false })} />)
    expect(screen.getByLabelText('Go forward')).toBeDisabled()
  })

  it('disables back button when canGoBack is false', () => {
    render(<BrowserToolbar {...makeProps({ canGoBack: false })} />)
    expect(screen.getByLabelText('Go back')).toBeDisabled()
  })

  it('shows stop button when isLoading is true', () => {
    render(<BrowserToolbar {...makeProps({ isLoading: true })} />)
    expect(screen.getByLabelText('Stop')).toBeInTheDocument()
    expect(screen.queryByLabelText('Reload')).not.toBeInTheDocument()
  })

  it('calls onGoBack when back button clicked', () => {
    const onGoBack = vi.fn()
    render(<BrowserToolbar {...makeProps({ onGoBack })} />)
    fireEvent.click(screen.getByLabelText('Go back'))
    expect(onGoBack).toHaveBeenCalledOnce()
  })

  it('calls onNavigate with normalized URL on Enter', () => {
    const onNavigate = vi.fn()
    render(<BrowserToolbar {...makeProps({ onNavigate })} />)
    const input = screen.getByRole('textbox')
    fireEvent.focus(input)
    fireEvent.change(input, { target: { value: 'example.com' } })
    fireEvent.keyDown(input, { key: 'Enter' })
    expect(onNavigate).toHaveBeenCalledWith('https://example.com/')
  })

  it('does not call onNavigate for invalid URL', () => {
    const onNavigate = vi.fn()
    render(<BrowserToolbar {...makeProps({ onNavigate })} />)
    const input = screen.getByRole('textbox')
    fireEvent.focus(input)
    fireEvent.change(input, { target: { value: 'javascript:alert(1)' } })
    fireEvent.keyDown(input, { key: 'Enter' })
    expect(onNavigate).not.toHaveBeenCalled()
  })

  it('displays current URL in input', () => {
    render(<BrowserToolbar {...makeProps({ url: 'https://example.com/page' })} />)
    expect(screen.getByRole('textbox')).toHaveValue('https://example.com/page')
  })

  it('shows popOut in menu for tab context', () => {
    const onPopOut = vi.fn()
    render(<BrowserToolbar {...makeProps({ onPopOut })} />)
    fireEvent.click(screen.getByLabelText('More'))
    expect(screen.getByText('Open in mini browser')).toBeInTheDocument()
  })

  it('shows moveToTab in menu for mini-window context', () => {
    const onMoveToTab = vi.fn()
    render(<BrowserToolbar {...makeProps({ context: 'mini-window', onMoveToTab })} />)
    fireEvent.click(screen.getByLabelText('More'))
    expect(screen.getByText('Open in main window')).toBeInTheDocument()
  })
})
