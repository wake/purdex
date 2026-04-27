import { describe, it, expect, beforeEach } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { LinkDetectionSection } from './LinkDetectionSection'
import { useUISettingsStore } from '../../stores/useUISettingsStore'

describe('LinkDetectionSection', () => {
  beforeEach(() => {
    useUISettingsStore.setState({
      linkDetectBareFilename: false,
    })
  })

  it('renders the bare-filename toggle bound to store', () => {
    render(<LinkDetectionSection />)
    const bareToggle = screen.getByLabelText(/Bare filenames|純檔名/)
    expect(bareToggle.getAttribute('aria-checked')).toBe('false')

    fireEvent.click(bareToggle)
    expect(useUISettingsStore.getState().linkDetectBareFilename).toBe(true)
  })

  it('does not render the file-path toggles (those moved to Editor settings)', () => {
    render(<LinkDetectionSection />)
    expect(screen.queryByLabelText(/Absolute paths|絕對路徑/)).toBeNull()
    expect(screen.queryByLabelText(/Tilde path|Tilde 路徑/)).toBeNull()
    expect(screen.queryByLabelText(/Relative paths with \/|含 \/ 的相對路徑/)).toBeNull()
  })
})
