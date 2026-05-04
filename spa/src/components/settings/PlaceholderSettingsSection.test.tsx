import { describe, it, expect, vi } from 'vitest'
import { render, screen } from '@testing-library/react'

const useI18nStoreMock = vi.hoisted(() => vi.fn())
vi.mock('../../stores/useI18nStore', () => ({
  useI18nStore: (selector: (s: unknown) => unknown) => useI18nStoreMock(selector),
}))

import { PlaceholderSettingsSection } from './PlaceholderSettingsSection'

describe('PlaceholderSettingsSection', () => {
  it('T8a: renders the no-purdex-settings i18n key text', () => {
    useI18nStoreMock.mockClear()
    useI18nStoreMock.mockImplementation((sel: (s: unknown) => unknown) =>
      sel({ t: (k: string) => k }),
    )
    render(<PlaceholderSettingsSection />)
    expect(screen.getByText('settings.module.no_purdex_settings')).toBeTruthy()
  })

  it('T8b: subscribes only to the `t` selector (single useI18nStore call)', () => {
    useI18nStoreMock.mockClear()
    useI18nStoreMock.mockImplementation((sel: (s: unknown) => unknown) =>
      sel({ t: (k: string) => k }),
    )
    render(<PlaceholderSettingsSection />)
    // Single selector call for `t`. This is a shape guard — it cannot prove
    // zero subscriptions to *other* stores at the test level; reviewers
    // still inspect imports during PR review.
    expect(useI18nStoreMock).toHaveBeenCalledTimes(1)
  })
})
