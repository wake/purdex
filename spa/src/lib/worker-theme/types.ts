// Worker pane theme shape (spec §4.1). A worker theme controls appearance
// only — it never changes room / chat structure, subscriptions or behaviour
// (spec T2). Colour values are expressions over the app theme's CSS
// variables (`var(--text-muted)`, `color-mix(...)`), so one worker theme
// renders correctly under every app theme.

/**
 * The closed set of `--wt-*` custom properties a worker theme must define
 * (spec §4.1). Adding a variable here means every registered theme's `vars`
 * must supply it — `WorkerTheme['vars']` is `Record<WorkerThemeVar, string>`.
 */
export type WorkerThemeVar =
  | 'font-size'
  | 'line-height'
  | 'block-gap'
  | 'heading-weight'
  | 'list-marker-color'
  | 'list-indent'
  | 'user-band-bg'
  | 'user-band-fg'
  | 'user-band-prefix-color'
  | 'rail-color'
  | 'footer-color'
  | 'footer-error-color'
  | 'table-border'
  | 'table-header-bg'
  | 'code-font'

export interface WorkerTheme {
  /** Stable identity, e.g. `'purdex'`. Persisted as `useWorkerSettingsStore.theme`. */
  id: string
  /** Locale key for the theme's display name in the settings select. */
  labelKey: string
  /** CSS values for each `WorkerThemeVar`; colours reference app theme variables. */
  vars: Record<WorkerThemeVar, string>
}
