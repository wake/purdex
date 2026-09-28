import type { WorkerTheme } from './types'

/**
 * The Purdex worker theme — today's look, made the first registered theme
 * (spec §5). Colour values reference app theme variables so it renders
 * correctly under every app theme (dark / light / nord / dracula / custom).
 */
export const PURDEX_THEME: WorkerTheme = {
  id: 'purdex',
  labelKey: 'worker.theme.purdex',
  vars: {
    'font-size': '14px',
    // Tuned in A3 against the terminal row height (spec §5.1): measured
    // Menlo 14px / line-height:normal box height 16px on this device
    // (16 / 14 = 1.14, playwright cli run-code against about:blank), below
    // the 1.35 CJK readability floor, so the floor applies.
    'line-height': '1.35',
    // One line (spec §5.1).
    'block-gap': 'calc(var(--wt-line-height) * 1em)',
    'heading-weight': '600',
    'list-marker-color': 'var(--text-muted)',
    'list-indent': '1.5em',
    'user-band-bg': 'color-mix(in srgb, var(--text-primary) 9%, transparent)',
    'user-band-fg': 'var(--text-primary)',
    'user-band-prefix-color': 'var(--text-muted)',
    'rail-color': 'color-mix(in srgb, var(--text-muted) 55%, transparent)',
    'footer-color': 'var(--text-muted)',
    'footer-error-color': 'var(--status-error)',
    'table-border': 'var(--border-default)',
    'table-header-bg': 'color-mix(in srgb, var(--text-primary) 6%, transparent)',
    'code-font': 'Menlo, Monaco, monospace',
  },
}
