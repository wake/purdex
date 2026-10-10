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

// A2 (spec §5.3): GFM tables render as a real <table>, wrapped so a wide
// table scrolls horizontally inside itself, not the whole transcript.
describe('RoomProse GFM tables (A2)', () => {
  const TABLE = '| a | b |\n|---|---|\n| 1 | 2 |'

  it('renders a GFM table', () => {
    render(<RoomProse content={TABLE} />)
    expect(screen.getByRole('table')).toBeInTheDocument()
    expect(screen.getAllByRole('columnheader').map((c) => c.textContent)).toEqual(['a', 'b'])
  })

  it('table is wrapped in overflow-x-auto', () => {
    render(<RoomProse content={TABLE} />)
    expect(screen.getByRole('table').parentElement).toHaveClass('overflow-x-auto')
  })
})

// A3 (spec §5.1): body-size headings and terminal density, not Tailwind
// Typography's prose-sm scale.
describe('RoomProse worker prose scale (A3)', () => {
  it('uses the worker prose scale, not prose-sm', () => {
    render(<RoomProse content={'# h\n\ntext'} />)
    const body = screen.getByTestId('room-prose').querySelector('[data-search-unit], .worker-prose')!
    expect(body).toHaveClass('worker-prose')
    expect(body).not.toHaveClass('prose-sm')
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

  // #2463: the --wt-* vars its .worker-prose rules read come from RoomProse's own root, so a caller outside the execution pane
  // (deck, chat) still gets the list indent and the monospace code face. jsdom computes no custom properties, so this pins
  // the inline style on the root; the real-Chromium numbers are in the PR.
  it('carries the worker theme vars on its own root, with no pane around it', () => {
    render(<RoomProse content={'- a\n  - b\n\nuse `x`'} />)
    const root = screen.getByTestId('room-prose')
    expect(root.style.getPropertyValue('--wt-list-indent')).toBe('1.5em')
    expect(root.style.getPropertyValue('--wt-code-font')).toBe('Menlo, Monaco, monospace')
    expect(root.style.getPropertyValue('--wt-block-gap')).not.toBe('')
    expect(document.querySelector('ul ul')).not.toBeNull()
    expect(document.querySelector('p code')!.textContent).toBe('x')
  })

  it('follows the selected worker theme', async () => {
    const { useWorkerSettingsStore } = await import('../../stores/useWorkerSettingsStore')
    const { registerWorkerTheme } = await import('../../lib/worker-theme/registry')
    const { PURDEX_THEME } = await import('../../lib/worker-theme/purdex')
    registerWorkerTheme({ ...PURDEX_THEME, id: 'rp-test', vars: { ...PURDEX_THEME.vars, 'list-indent': '3em' } })
    const before = useWorkerSettingsStore.getState().theme
    useWorkerSettingsStore.setState({ theme: 'rp-test' })
    try {
      render(<RoomProse content="- a" />)
      expect(screen.getByTestId('room-prose').style.getPropertyValue('--wt-list-indent')).toBe('3em')
    } finally { useWorkerSettingsStore.setState({ theme: before }) }
  })
})
