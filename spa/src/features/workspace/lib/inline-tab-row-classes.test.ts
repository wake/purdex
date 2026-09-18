import { describe, it, expect } from 'vitest'
import { INLINE_TAB_ROW_CLASSES } from './inline-tab-row-classes'

describe('INLINE_TAB_ROW_CLASSES.hoverPreview', () => {
  it('shows the hover surface statically, with no hover: prefix left', () => {
    const cls = INLINE_TAB_ROW_CLASSES.hoverPreview
    expect(cls).toContain('bg-surface-hover')
    expect(cls).toContain('text-text-primary')
    expect(cls).not.toContain('hover:')
  })
})
