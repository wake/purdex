import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import QuickReplyDock from './QuickReplyDock'

const replies = [
  { id: 'continue', text: 'continue' },
  { id: 'run-tests', text: 'run the tests' },
]

describe('QuickReplyDock', () => {
  it('renders a button per reply', () => {
    render(<QuickReplyDock replies={replies} onSend={() => {}} disabled={false} />)
    const buttons = screen.getAllByTestId('quick-reply')
    expect(buttons.map((b) => b.textContent)).toEqual(['continue', 'run the tests'])
    for (const b of buttons) expect(b.tagName).toBe('BUTTON')
  })

  it('renders nothing for an empty list', () => {
    const { container } = render(<QuickReplyDock replies={[]} onSend={() => {}} disabled={false} />)
    expect(container).toBeEmptyDOMElement()
  })

  it("a tap sends the reply's text", () => {
    const onSend = vi.fn()
    render(<QuickReplyDock replies={replies} onSend={onSend} disabled={false} />)
    fireEvent.click(screen.getAllByTestId('quick-reply')[1])
    expect(onSend).toHaveBeenCalledTimes(1)
    expect(onSend).toHaveBeenCalledWith('run the tests')
  })

  it('is disabled while a send is pending / the worker ended', () => {
    const onSend = vi.fn()
    render(<QuickReplyDock replies={replies} onSend={onSend} disabled />)
    for (const b of screen.getAllByTestId('quick-reply')) {
      expect(b).toBeDisabled()
      fireEvent.click(b)
    }
    expect(onSend).not.toHaveBeenCalled()
  })
})

describe('QuickReplyDock collapse', () => {
  it('toggles with the chevron, defaults expanded, and persists in the worker settings store', async () => {
    const { useWorkerSettingsStore } = await import('../../stores/useWorkerSettingsStore')
    useWorkerSettingsStore.setState({ quickRepliesCollapsed: false })
    render(<QuickReplyDock replies={[{ id: 'a', text: 'ok' }] as never} onSend={vi.fn()} disabled={false} />)
    expect(screen.getAllByTestId('quick-reply')).toHaveLength(1)
    fireEvent.click(screen.getByTestId('quick-reply-toggle'))
    expect(screen.queryAllByTestId('quick-reply')).toHaveLength(0)
    expect(useWorkerSettingsStore.getState().quickRepliesCollapsed).toBe(true)
    expect(screen.getByTestId('quick-reply-toggle')).toHaveAttribute('aria-expanded', 'false')
    fireEvent.click(screen.getByTestId('quick-reply-toggle'))
    expect(screen.getAllByTestId('quick-reply')).toHaveLength(1)
  })
})
