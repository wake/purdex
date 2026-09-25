// spa/src/components/room/RoomProse.test.tsx — the agent's prose in the room
// (spec §4.1): markdown at the pane's one left edge, capped by a reading
// measure only. Carried over from MessageBubble's assistant arm (T4.3).
import { describe, it, expect, beforeEach } from 'vitest'
import { render, screen, cleanup } from '@testing-library/react'
import RoomProse from './RoomProse'

beforeEach(() => { cleanup() })

describe('RoomProse', () => {
  it('renders markdown with inline code', () => {
    render(<RoomProse content="use `npm install`" />)
    expect(screen.getByText('npm install')).toBeInTheDocument()
  })

  it('renders markdown bold content', () => {
    render(<RoomProse content="**bold text**" />)
    const bold = document.querySelector('strong')
    expect(bold).toBeInTheDocument()
    expect(bold?.textContent).toBe('bold text')
  })

  it('renders a code block', () => {
    render(<RoomProse content={'```js\nconsole.log("hi")\n```'} />)
    expect(document.querySelector('code')).toBeInTheDocument()
  })

  it('is capped by a reading measure, not a percentage of the pane', () => {
    render(<RoomProse content="hi" />)
    const prose = screen.getByTestId('room-prose')
    expect(prose.className).toContain('max-w-[90ch]')
    expect(prose.className).not.toMatch(/max-w-\[\d+%\]/)
    expect(prose.className).not.toContain('justify-end')
  })

  it('draws no bubble and no avatar', () => {
    const { container } = render(<RoomProse content="test" />)
    expect(container.querySelector('[data-testid="user-bubble"]')).toBeNull()
    expect(container.querySelector('[data-testid="icon-user"]')).toBeNull()
    expect(container.querySelector('[data-testid="icon-assistant"]')).toBeNull()
  })
})

// P-B2.2 task 8 (spec §4.4 R1): `streaming` appends a blinking cursor after
// the markdown body. The typewriter is on spec §3's do-not-touch list.
describe('RoomProse streaming cursor (R1)', () => {
  it('streaming renders the cursor as a sibling after the prose div', () => {
    const { container } = render(<RoomProse content="partial text" streaming />)
    const wrapper = container.querySelector('[data-testid="room-prose"]')!
    const cursor = wrapper.querySelector('[data-testid="stream-cursor"]')!
    expect(cursor).toBeInTheDocument()
    expect(cursor).toHaveTextContent('▌')
    expect(cursor).toHaveClass('stream-cursor')
    expect(cursor).toHaveAttribute('aria-hidden', 'true')
    // At the end of the flow: a direct child of the wrapper, right after the
    // prose container, not inside ReactMarkdown's output.
    const prose = wrapper.querySelector('.prose')!
    expect(cursor.parentElement).toBe(wrapper)
    expect(prose.nextElementSibling).toBe(cursor)
    expect(prose.contains(cursor)).toBe(false)
    expect(wrapper.querySelectorAll('[data-testid="stream-cursor"]')).toHaveLength(1)
  })

  it('without streaming renders no cursor', () => {
    const { container } = render(<RoomProse content="done" />)
    expect(container.querySelector('[data-testid="stream-cursor"]')).toBeNull()
  })
})
