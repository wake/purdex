import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import type { ReactNode } from 'react'
import { BreadcrumbPopover } from './BreadcrumbPopover'

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
