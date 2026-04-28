import { describe, it, expect, beforeEach } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { EditorLinkDetectionSection } from './EditorLinkDetectionSection'
import { useUISettingsStore } from '../../../stores/useUISettingsStore'

describe('EditorLinkDetectionSection', () => {
  beforeEach(() => {
    useUISettingsStore.setState({
      linkDetectAbsolute: true,
      linkDetectTilde: true,
      linkDetectRelativeSlash: false,
    })
  })

  it('renders 3 file-path toggles bound to store', () => {
    render(<EditorLinkDetectionSection />)
    const absToggle = screen.getByLabelText(/Absolute paths|絕對路徑/)
    const tildeToggle = screen.getByLabelText(/Tilde path|Tilde 路徑/)
    const relToggle = screen.getByLabelText(/Relative paths with \/|含 \/ 的相對路徑/)

    expect(absToggle.getAttribute('aria-checked')).toBe('true')
    expect(tildeToggle.getAttribute('aria-checked')).toBe('true')
    expect(relToggle.getAttribute('aria-checked')).toBe('false')

    fireEvent.click(tildeToggle)
    expect(useUISettingsStore.getState().linkDetectTilde).toBe(false)

    fireEvent.click(relToggle)
    expect(useUISettingsStore.getState().linkDetectRelativeSlash).toBe(true)

    fireEvent.click(absToggle)
    expect(useUISettingsStore.getState().linkDetectAbsolute).toBe(false)
  })

  it('does not render the bare-filename toggle (that stays under Terminal settings)', () => {
    render(<EditorLinkDetectionSection />)
    expect(screen.queryByLabelText(/Bare filenames|純檔名/)).toBeNull()
  })
})
