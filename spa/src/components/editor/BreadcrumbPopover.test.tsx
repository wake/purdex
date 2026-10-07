import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, cleanup } from '@testing-library/react'
import type { ReactNode } from 'react'
import { BreadcrumbPopover } from './BreadcrumbPopover'
import { TITLE_BAR_HEIGHT } from '../FloatingPanel'

// Render portal children inline for jsdom.
vi.mock('react-dom', async (importOriginal) => {
  const actual = await importOriginal<typeof import('react-dom')>()
  return { ...actual, createPortal: (node: ReactNode) => node }
})

// Reduce i18n to key identity.
vi.mock('../../stores/useI18nStore', () => ({
  useI18nStore: (selector: (s: { t: (k: string) => string }) => unknown) =>
    selector({ t: (k: string) => k }),
}))

function makeAnchorRect(): DOMRect {
  return {
    left: 100,
    top: 50,
    right: 180,
    bottom: 70,
    width: 80,
    height: 20,
    x: 100,
    y: 50,
    toJSON: () => ({}),
  } as DOMRect
}

describe('BreadcrumbPopover', () => {
  it('C3-1: renders one <li> per buffer', () => {
    render(
      <BreadcrumbPopover
        buffers={['a.md', 'b.md', 'c.md']}
        currentBufferKey="/buffer/a.md"
        onSwitch={() => {}}
        onManage={() => {}}
        onDismiss={() => {}}
        anchorRect={makeAnchorRect()}
      />,
    )
    expect(screen.getAllByRole('listitem')).toHaveLength(3)
  })

  it('C3-2: current buffer item has aria-current="true"', () => {
    render(
      <BreadcrumbPopover
        buffers={['a.md', 'b.md', 'c.md']}
        currentBufferKey="/buffer/b.md"
        onSwitch={() => {}}
        onManage={() => {}}
        onDismiss={() => {}}
        anchorRect={makeAnchorRect()}
      />,
    )
    const currentItem = screen.getByRole('button', { name: /b\.md/ })
    expect(currentItem.getAttribute('aria-current')).toBe('true')
    const otherItem = screen.getByRole('button', { name: /a\.md/ })
    expect(otherItem.getAttribute('aria-current')).not.toBe('true')
  })

  it('C3-3: clicking non-current item fires onSwitch with full path', () => {
    const onSwitch = vi.fn()
    const onDismiss = vi.fn()
    render(
      <BreadcrumbPopover
        buffers={['a.md', 'b.md', 'c.md']}
        currentBufferKey="/buffer/b.md"
        onSwitch={onSwitch}
        onManage={() => {}}
        onDismiss={onDismiss}
        anchorRect={makeAnchorRect()}
      />,
    )
    fireEvent.click(screen.getByRole('button', { name: /a\.md/ }))
    expect(onSwitch).toHaveBeenCalledTimes(1)
    expect(onSwitch).toHaveBeenCalledWith('/buffer/a.md')
    expect(onDismiss).toHaveBeenCalled()
  })

  it('C3-4: Escape key dismisses', () => {
    const onDismiss = vi.fn()
    render(
      <BreadcrumbPopover
        buffers={['a.md']}
        currentBufferKey="/buffer/a.md"
        onSwitch={() => {}}
        onManage={() => {}}
        onDismiss={onDismiss}
        anchorRect={makeAnchorRect()}
      />,
    )
    fireEvent.keyDown(document, { key: 'Escape' })
    expect(onDismiss).toHaveBeenCalledTimes(1)
  })

  it('C3-5: clicking Manage buffers fires onManage', () => {
    const onManage = vi.fn()
    const onDismiss = vi.fn()
    render(
      <BreadcrumbPopover
        buffers={['a.md']}
        currentBufferKey="/buffer/a.md"
        onSwitch={() => {}}
        onManage={onManage}
        onDismiss={onDismiss}
        anchorRect={makeAnchorRect()}
      />,
    )
    fireEvent.click(screen.getByTestId('breadcrumb-popover-manage'))
    expect(onManage).toHaveBeenCalledTimes(1)
    expect(onDismiss).toHaveBeenCalled()
  })
})

// #1825 (the #1801 fix, applied here). The top TITLE_BAR_HEIGHT px of the window
// is the title bar's OS drag region: a click there drags the window instead of
// reaching a buffer row, so no part of the popover may be placed in it — one
// taller than the room left scrolls instead.
describe('BreadcrumbPopover stays below the title bar (#1825)', () => {
  const anchorAt = (left: number, top: number): DOMRect =>
    ({ left, top, width: 80, height: 20, right: left + 80, bottom: top + 20, x: left, y: top, toJSON: () => ({}) }) as DOMRect

  function popoverAt(anchorRect: DOMRect) {
    return (
      <BreadcrumbPopover
        buffers={Array.from({ length: 20 }, (_, i) => `b${i}.md`)}
        currentBufferKey="/buffer/b0.md"
        onSwitch={() => {}}
        onManage={() => {}}
        onDismiss={() => {}}
        anchorRect={anchorRect}
      />
    )
  }

  const saved: Array<[object, string, PropertyDescriptor | undefined]> = []
  /** jsdom lays nothing out: hand the popover its height and the window its size. */
  function stubLayout(height: number, innerHeight: number) {
    Object.defineProperty(HTMLElement.prototype, 'offsetHeight', { configurable: true, get: () => height })
    Object.defineProperty(window, 'innerHeight', { configurable: true, value: innerHeight })
    Object.defineProperty(window, 'innerWidth', { configurable: true, value: 1024 })
  }

  beforeEach(() => {
    cleanup()
    saved.push(
      [HTMLElement.prototype, 'offsetHeight', Object.getOwnPropertyDescriptor(HTMLElement.prototype, 'offsetHeight')],
      [window, 'innerHeight', Object.getOwnPropertyDescriptor(window, 'innerHeight')],
      [window, 'innerWidth', Object.getOwnPropertyDescriptor(window, 'innerWidth')],
    )
  })
  afterEach(() => {
    cleanup()
    for (const [target, key, descriptor] of saved.splice(0)) {
      if (descriptor) Object.defineProperty(target, key, descriptor)
      else delete (target as Record<string, unknown>)[key]
    }
  })

  it('a tall popover in a short viewport starts below the title bar and scrolls instead of running off-screen', () => {
    stubLayout(300, 250)
    render(popoverAt(anchorAt(100, 40)))
    const el = screen.getByRole('dialog')
    // below: 60 + 4 + 300 > 246 → flip; above: 40 - 4 - 300 < 36 → the title bar's bottom edge
    expect(el.style.top).toBe(`${TITLE_BAR_HEIGHT}px`)
    expect(el.style.maxHeight).toBe(`${250 - TITLE_BAR_HEIGHT - 4}px`)
    expect(el.style.overflowY).toBe('auto')
    expect(el.style.left).toBe('100px')
  })

  it('a tall popover from an anchor near the bottom flips up only as far as the title bar, not to 4px', () => {
    stubLayout(300, 250)
    render(popoverAt(anchorAt(100, 200)))
    const el = screen.getByRole('dialog')
    // below: 224 + 300 > 246 → flip; above: 200 - 4 - 300 = -104 < 36
    expect(el.style.top).toBe(`${TITLE_BAR_HEIGHT}px`)
    expect(el.style.maxHeight).toBe(`${250 - TITLE_BAR_HEIGHT - 4}px`)
    expect(el.style.overflowY).toBe('auto')
  })

  it('moved down to the title bar\'s edge, it keeps its own cap when it still fits', () => {
    stubLayout(100, 200)
    render(popoverAt(anchorAt(100, 90)))
    const el = screen.getByRole('dialog')
    // below: 114 + 100 > 196 → flip; above: 90 - 4 - 100 = -14 < 36; 36 + 100 fits in 200 - 4
    expect(el.style.top).toBe(`${TITLE_BAR_HEIGHT}px`)
    expect(el.style.maxHeight).toBe('320px')
    expect(el.style.overflowY).toBe('')
  })

  it('room below the anchor: the same place as before, and its own 320px cap', () => {
    stubLayout(200, 800)
    render(popoverAt(anchorAt(100, 50)))
    const el = screen.getByRole('dialog')
    expect(el.style.left).toBe('100px')
    expect(el.style.top).toBe('74px') // 70 + 4
    expect(el.style.maxHeight).toBe('320px')
    expect(el.style.overflowY).toBe('')
  })

  it('no room below: flips above the anchor as before, and its own 320px cap', () => {
    stubLayout(200, 800)
    render(popoverAt(anchorAt(100, 700)))
    const el = screen.getByRole('dialog')
    expect(el.style.left).toBe('100px')
    expect(el.style.top).toBe('496px') // 700 - 4 - 200
    expect(el.style.maxHeight).toBe('320px')
    expect(el.style.overflowY).toBe('')
  })

  it('drops an earlier cap when it is placed again with room to spare', () => {
    stubLayout(300, 250)
    const { rerender } = render(popoverAt(anchorAt(100, 40)))
    const el = screen.getByRole('dialog')
    expect(el.style.maxHeight).toBe(`${250 - TITLE_BAR_HEIGHT - 4}px`)
    Object.defineProperty(window, 'innerHeight', { configurable: true, value: 1000 })
    rerender(popoverAt(anchorAt(100, 40)))
    expect(el.style.top).toBe('64px') // 60 + 4, and 64 + 300 fits in 1000 - 4
    expect(el.style.maxHeight).toBe('320px')
    expect(el.style.overflowY).toBe('')
  })

  it('is marked no-drag, so the title bar region does not take its pointer events', () => {
    stubLayout(200, 800)
    render(popoverAt(anchorAt(100, 50)))
    const el = screen.getByRole('dialog')
    // React assigns the camelCase property, which jsdom keeps as is (see FloatingPanel.test.tsx).
    expect((el.style as unknown as { WebkitAppRegion?: string }).WebkitAppRegion).toBe('no-drag')
  })
})
