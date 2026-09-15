import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent, within } from '@testing-library/react'
import { HostColorMarkSetting } from './HostColorMarkSetting'
import type { HostColorMarkStyle } from '../../stores/useUISettingsStore'

// Option order in the SegmentControl: gradient, left-line, bottom-line, none
const STYLE_ORDER: HostColorMarkStyle[] = ['gradient', 'left-line', 'bottom-line', 'none']

function renderSetting(style: HostColorMarkStyle, width = 2) {
  const onStyleChange = vi.fn()
  const onWidthChange = vi.fn()
  const utils = render(
    <HostColorMarkSetting
      label="Sidebar mark"
      description="desc"
      style={style}
      width={width}
      onStyleChange={onStyleChange}
      onWidthChange={onWidthChange}
      testIdPrefix="host-color-test"
    />,
  )
  return { ...utils, onStyleChange, onWidthChange }
}

function styleButtons() {
  return within(screen.getByTestId('host-color-test-style')).getAllByRole('button')
}

describe('HostColorMarkSetting', () => {
  it('renders label and four style options', () => {
    renderSetting('gradient')
    expect(screen.getByText('Sidebar mark')).toBeTruthy()
    expect(styleButtons()).toHaveLength(4)
  })

  it('calls onStyleChange with the clicked style', () => {
    const { onStyleChange } = renderSetting('none')
    for (const [i, style] of STYLE_ORDER.entries()) {
      if (style === 'none') continue
      fireEvent.click(styleButtons()[i])
      expect(onStyleChange).toHaveBeenLastCalledWith(style)
    }
  })

  it('shows width input only for line styles', () => {
    for (const style of STYLE_ORDER) {
      const { unmount } = renderSetting(style)
      const input = screen.queryByTestId('host-color-test-width')
      if (style === 'left-line' || style === 'bottom-line') {
        expect(input).not.toBeNull()
        expect(input?.getAttribute('type')).toBe('number')
        expect(input?.getAttribute('aria-label')).toMatch(/^Sidebar mark: /)
        expect(screen.getByText('px')).toBeTruthy()
      } else {
        expect(input).toBeNull()
      }
      unmount()
    }
  })

  it('clamps width input to 1-6 before calling onWidthChange', () => {
    const { onWidthChange } = renderSetting('left-line', 2)
    const input = screen.getByTestId('host-color-test-width')
    fireEvent.change(input, { target: { value: '9' } })
    expect(onWidthChange).toHaveBeenLastCalledWith(6)
    fireEvent.change(input, { target: { value: '0' } })
    expect(onWidthChange).toHaveBeenLastCalledWith(1)
    fireEvent.change(input, { target: { value: '4' } })
    expect(onWidthChange).toHaveBeenLastCalledWith(4)
  })
})
