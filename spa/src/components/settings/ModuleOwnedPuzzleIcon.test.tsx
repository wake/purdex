import { render } from '@testing-library/react'
import { describe, it, expect } from 'vitest'
import { ModuleOwnedPuzzleIcon } from './ModuleOwnedPuzzleIcon'

describe('ModuleOwnedPuzzleIcon', () => {
  it('is a muted, decorative icon whose size can be set', () => {
    const { container } = render(<ModuleOwnedPuzzleIcon size={10} />)
    const svg = container.querySelector('svg')!
    expect(svg.getAttribute('aria-hidden')).toBe('true')
    expect(svg.getAttribute('class')).toContain('text-text-muted')
    expect(svg.getAttribute('width')).toBe('10')
  })

  it('takes the brighter tone when asked', () => {
    const { container } = render(<ModuleOwnedPuzzleIcon tone="secondary" />)
    expect(container.querySelector('svg')!.getAttribute('class')).toContain('text-text-secondary')
  })
})
