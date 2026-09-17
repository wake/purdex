// spa/src/components/MessageBubble.test.tsx
import { describe, it, expect, beforeEach } from 'vitest'
import { render, screen, cleanup } from '@testing-library/react'
import MessageBubble from './MessageBubble'

beforeEach(() => { cleanup() })

describe('MessageBubble', () => {
  it('renders user message in a bubble', () => {
    const { container } = render(<MessageBubble role="user" content="hello" />)
    const bubble = container.querySelector('[data-testid="user-bubble"]')
    expect(bubble).toBeInTheDocument()
    expect(bubble).toHaveTextContent('hello')
  })

  it('renders assistant message without bubble wrapper', () => {
    const { container } = render(<MessageBubble role="assistant" content="hi there" />)
    expect(container.querySelector('[data-testid="user-bubble"]')).toBeNull()
    const text = container.querySelector('[data-testid="assistant-text"]')
    expect(text).toBeInTheDocument()
  })

  it('renders assistant markdown with code blocks', () => {
    render(<MessageBubble role="assistant" content="use `npm install`" />)
    expect(screen.getByText('npm install')).toBeInTheDocument()
  })

  it('applies correct user bubble classes', () => {
    const { container } = render(<MessageBubble role="user" content="test" />)
    const bubble = container.querySelector('[data-testid="user-bubble"]')
    expect(bubble?.className).toContain('bg-[#334a5e]')
  })

  it('renders user message as plain text (not markdown)', () => {
    render(<MessageBubble role="user" content="Hello, world!" />)
    expect(screen.getByText('Hello, world!')).toBeInTheDocument()
  })

  it('renders assistant markdown bold content', () => {
    render(<MessageBubble role="assistant" content="**bold text**" />)
    const bold = document.querySelector('strong')
    expect(bold).toBeInTheDocument()
    expect(bold?.textContent).toBe('bold text')
  })

  it('renders code block for assistant message', () => {
    const code = '```js\nconsole.log("hi")\n```'
    render(<MessageBubble role="assistant" content={code} />)
    const codeEl = document.querySelector('code')
    expect(codeEl).toBeInTheDocument()
  })

  it('does not render any avatar elements', () => {
    const { container: userContainer } = render(
      <MessageBubble role="user" content="test" />,
    )
    expect(userContainer.querySelector('[data-testid="icon-user"]')).toBeNull()
    expect(userContainer.querySelector('[data-testid="icon-assistant"]')).toBeNull()

    cleanup()

    const { container: assistantContainer } = render(
      <MessageBubble role="assistant" content="test" />,
    )
    expect(assistantContainer.querySelector('[data-testid="icon-user"]')).toBeNull()
    expect(assistantContainer.querySelector('[data-testid="icon-assistant"]')).toBeNull()
  })
})

// P-B2.2 task 8 (spec §4.4 R1): `streaming` appends a blinking cursor after
// the markdown body of an assistant bubble; user bubbles ignore it.
describe('MessageBubble streaming cursor (R1)', () => {
  it('assistant + streaming renders the cursor as a sibling after the prose div', () => {
    const { container } = render(
      <MessageBubble role="assistant" content="partial text" streaming />,
    )
    const wrapper = container.querySelector('[data-testid="assistant-text"]')!
    const cursor = wrapper.querySelector('[data-testid="stream-cursor"]')!
    expect(cursor).toBeInTheDocument()
    expect(cursor).toHaveTextContent('▌')
    expect(cursor).toHaveClass('stream-cursor')
    expect(cursor).toHaveAttribute('aria-hidden', 'true')
    // Sits at the end of the flow: direct child of the wrapper, immediately
    // after the prose container, not inside ReactMarkdown output.
    const prose = wrapper.querySelector('.prose')!
    expect(cursor.parentElement).toBe(wrapper)
    expect(prose.nextElementSibling).toBe(cursor)
    expect(prose.contains(cursor)).toBe(false)
    expect(wrapper.querySelectorAll('[data-testid="stream-cursor"]')).toHaveLength(1)
  })

  it('assistant without streaming renders no cursor', () => {
    const { container } = render(<MessageBubble role="assistant" content="done" />)
    expect(container.querySelector('[data-testid="stream-cursor"]')).toBeNull()
  })

  it('user + streaming renders no cursor', () => {
    const { container } = render(<MessageBubble role="user" content="hello" streaming />)
    expect(container.querySelector('[data-testid="user-bubble"]')).toBeInTheDocument()
    expect(container.querySelector('[data-testid="stream-cursor"]')).toBeNull()
  })
})

// P-B2.2 G5 guard: default-prop rendering must stay byte-identical while
// task 8 adds the optional `streaming` prop. Taken BEFORE any renderer change.
describe('MessageBubble default-prop snapshots (G5)', () => {
  it('user bubble', () => {
    const { container } = render(<MessageBubble role="user" content="Hello, world!" />)
    expect(container.firstChild).toMatchSnapshot()
  })

  it('assistant bubble with markdown and a code fence', () => {
    const md = 'Run **this**:\n\n```js\nconsole.log("hi")\n```\n'
    const { container } = render(<MessageBubble role="assistant" content={md} />)
    expect(container.firstChild).toMatchSnapshot()
  })
})
