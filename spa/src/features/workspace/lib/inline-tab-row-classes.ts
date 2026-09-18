/**
 * The two row surfaces an inline tab can be in. Shared between `InlineTab`
 * (the real row) and any mock-up of a sidebar row (e.g. `HostBadgePreview`)
 * so the mock cannot drift from the real classes.
 */
export const INLINE_TAB_ROW_CLASSES = {
  active: 'bg-surface-active text-white border border-transparent',
  inactive: 'text-text-muted hover:bg-surface-hover hover:text-text-primary border border-transparent',
} as const
