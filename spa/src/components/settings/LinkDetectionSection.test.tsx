import { describe, it, expect, beforeEach } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { LinkDetectionSection } from './LinkDetectionSection'
import { useUISettingsStore } from '../../stores/useUISettingsStore'

describe('LinkDetectionSection', () => {
  beforeEach(() => {
    useUISettingsStore.setState({
      linkDetectAbsolute: true,
      linkDetectTilde: true,
      linkDetectRelativeSlash: false,
      linkDetectBareFilename: false,
    })
  })

  it('renders 4 toggles bound to store', () => {
    render(<LinkDetectionSection />)
    const absToggle = screen.getByLabelText(/Absolute paths|絕對路徑/)
    const tildeToggle = screen.getByLabelText(/Tilde path|Tilde 路徑/)
    const relToggle = screen.getByLabelText(/Relative paths with \/|含 \/ 的相對路徑/)
    const bareToggle = screen.getByLabelText(/Bare filenames|純檔名/)

    expect(absToggle.getAttribute('aria-checked')).toBe('true')
    expect(tildeToggle.getAttribute('aria-checked')).toBe('true')
    expect(relToggle.getAttribute('aria-checked')).toBe('false')
    expect(bareToggle.getAttribute('aria-checked')).toBe('false')

    fireEvent.click(tildeToggle)
    expect(useUISettingsStore.getState().linkDetectTilde).toBe(false)

    fireEvent.click(relToggle)
    expect(useUISettingsStore.getState().linkDetectRelativeSlash).toBe(true)

    fireEvent.click(bareToggle)
    expect(useUISettingsStore.getState().linkDetectBareFilename).toBe(true)

    fireEvent.click(absToggle)
    expect(useUISettingsStore.getState().linkDetectAbsolute).toBe(false)
  })
})
