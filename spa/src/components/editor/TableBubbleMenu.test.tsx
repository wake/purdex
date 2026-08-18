// T2.2b — the Live Mode table editing affordance.
//
// Mock-based by design (plan T2.2b): driving a real ProseMirror selection into a
// table cell inside jsdom costs far more than it proves, and the real
// parse→serialize round-trip is already pinned by `TiptapEditor.roundtrip.test.ts`.
// What matters here is the behaviour this component owns: when the menu is
// offered, what each button does to the editor, and that reaching for it never
// takes the caret out of the document.
import { createEvent, fireEvent, render, screen } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { TableBubbleMenu } from './TableBubbleMenu'
import type { Editor } from '@tiptap/react'

const shouldShowSpy = vi.hoisted(() => vi.fn())

vi.mock('@tiptap/react/menus', () => ({
  // Stand-in for the real bubble menu plugin wrapper: it asks `shouldShow`
  // whether the current selection deserves a menu and renders the children only
  // when it says yes — the one contract this component depends on.
  BubbleMenu: ({
    children,
    editor,
    shouldShow,
    pluginKey: _pluginKey,
    updateDelay: _updateDelay,
    options: _options,
    ...rest
  }: {
    children?: React.ReactNode
    editor?: unknown
    shouldShow?: ((props: { editor: unknown }) => boolean) | null
    pluginKey?: string
    updateDelay?: number
    options?: unknown
  } & React.HTMLAttributes<HTMLDivElement>) => {
    shouldShowSpy(shouldShow)
    const visible = shouldShow ? shouldShow({ editor }) : true
    if (!visible) return null
    return (
      <div data-testid="bubble-menu-host" {...rest}>
        {children}
      </div>
    )
  },
}))

type Chain = Record<string, ReturnType<typeof vi.fn>>

function createChain(): Chain {
  const chain: Chain = {}
  for (const command of [
    'focus',
    'addRowBefore',
    'addRowAfter',
    'deleteRow',
    'addColumnBefore',
    'addColumnAfter',
    'deleteColumn',
    'deleteTable',
  ]) {
    chain[command] = vi.fn(() => chain)
  }
  chain.run = vi.fn(() => true)
  return chain
}

function createEditor(inTable: boolean) {
  const chain = createChain()
  const editor = {
    isActive: vi.fn((name: string) => name === 'table' && inTable),
    chain: vi.fn(() => chain),
  }
  return { editor: editor as unknown as Editor, chain, isActive: editor.isActive }
}

const ACTIONS: Array<[testId: string, command: string]> = [
  ['add-row-before', 'addRowBefore'],
  ['add-row-after', 'addRowAfter'],
  ['delete-row', 'deleteRow'],
  ['add-column-before', 'addColumnBefore'],
  ['add-column-after', 'addColumnAfter'],
  ['delete-column', 'deleteColumn'],
  ['delete-table', 'deleteTable'],
]

describe('TableBubbleMenu', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('renders the menu when the selection is inside a table', () => {
    const { editor, isActive } = createEditor(true)

    render(<TableBubbleMenu editor={editor} />)

    expect(screen.getByTestId('bubble-menu-host')).toBeInTheDocument()
    expect(isActive).toHaveBeenCalledWith('table')
    for (const [testId] of ACTIONS) {
      expect(screen.getByTestId(`table-menu-${testId}`)).toBeInTheDocument()
    }
  })

  it('offers nothing when the selection is outside a table', () => {
    const { editor } = createEditor(false)

    render(<TableBubbleMenu editor={editor} />)

    expect(screen.queryByTestId('bubble-menu-host')).toBeNull()
    expect(screen.queryByTestId('table-menu-delete-table')).toBeNull()
  })

  it('renders nothing at all before the editor exists', () => {
    const { container } = render(<TableBubbleMenu editor={null} />)

    expect(container).toBeEmptyDOMElement()
    expect(shouldShowSpy).not.toHaveBeenCalled()
  })

  it.each(ACTIONS)('runs the %s command through the editor chain', (testId, command) => {
    const { editor, chain } = createEditor(true)

    render(<TableBubbleMenu editor={editor} />)
    fireEvent.click(screen.getByTestId(`table-menu-${testId}`))

    expect(chain[command]).toHaveBeenCalledTimes(1)
    // Every action keeps the caret in the document and is committed exactly once.
    expect(chain.focus).toHaveBeenCalledTimes(1)
    expect(chain.run).toHaveBeenCalledTimes(1)
    // No neighbouring command fired by accident.
    for (const [, other] of ACTIONS) {
      if (other !== command) expect(chain[other]).not.toHaveBeenCalled()
    }
  })

  it('does not steal focus from the editable surface when a button is pressed', () => {
    const { editor } = createEditor(true)

    render(<TableBubbleMenu editor={editor} />)

    for (const [testId] of ACTIONS) {
      const button = screen.getByTestId(`table-menu-${testId}`)
      const event = createEvent.mouseDown(button)
      fireEvent(button, event)
      // Preventing mousedown is what keeps the ProseMirror selection alive: the
      // menu lives outside the contenteditable, so an unprevented press blurs it
      // and every command would then act on a collapsed/blurred selection.
      expect(event.defaultPrevented).toBe(true)
    }
  })

  it('is keyboard reachable: every action is a real button with an accessible name', () => {
    const { editor } = createEditor(true)

    render(<TableBubbleMenu editor={editor} />)

    for (const [testId] of ACTIONS) {
      const button = screen.getByTestId(`table-menu-${testId}`)
      expect(button.tagName).toBe('BUTTON')
      expect(button).toHaveAttribute('type', 'button')
      // Not removed from the tab order…
      expect(button).not.toHaveAttribute('tabindex', '-1')
      // …and announced by something other than the icon glyph.
      expect(button.getAttribute('aria-label')).toBeTruthy()
      // Focusable without any of the commands firing.
      button.focus()
      expect(document.activeElement).toBe(button)
    }
  })

  it('exposes the group as a toolbar so arrow/tab navigation has a landmark', () => {
    const { editor } = createEditor(true)

    render(<TableBubbleMenu editor={editor} />)

    const host = screen.getByTestId('bubble-menu-host')
    expect(host).toHaveAttribute('role', 'toolbar')
    expect(host.getAttribute('aria-label')).toBeTruthy()
  })
})
