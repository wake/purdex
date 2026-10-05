import { useEffect, useRef } from 'react'
import type { PopupSpec } from '../../../lib/file-open/open-file'
import { useI18nStore } from '../../../stores/useI18nStore'

export interface FileNotFoundPopupProps {
  spec: PopupSpec
  /** Resolved cwd of the session (Layer 2 root). null → capability missing. */
  sessionCwd: string | null
  onClose: () => void
  /** Open a specific candidate path (cancel popup as side-effect). */
  onOpenPath: (path: string) => void
  /** Trigger Layer 2 fs.search (only meaningful when sessionCwd is non-null). */
  onSearchSessionCwd: () => void
}

/**
 * P5 file-not-found popup.
 *
 * The search CTA surfaces the search root explicitly so the user knows what
 * scope they're opting into rather than seeing a generic "Search" button
 * (defensive review #4). When the underlying capability is absent (no
 * sessionCode → no sessionCwd), the button is disabled with an
 * `aria-disabled` + tooltip explaining the gap.
 *
 * Focus trap is intentionally minimal — initial focus moves to the popup root
 * and ESC closes; tab order naturally cycles through the rendered buttons.
 * Layer 1 hits are read off `spec.hits` at render time so subsequent
 * path-cache mutations cannot mutate an already-mounted popup's options.
 */
export function FileNotFoundPopup({
  spec,
  sessionCwd,
  onClose,
  onOpenPath,
  onSearchSessionCwd,
}: FileNotFoundPopupProps) {
  const t = useI18nStore((s) => s.t)
  const dialogRef = useRef<HTMLDivElement>(null)

  // ESC closes; focus the dialog on mount so keyboard users land inside it.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose()
    }
    document.addEventListener('keydown', onKey)
    dialogRef.current?.focus()
    return () => document.removeEventListener('keydown', onKey)
  }, [onClose])

  // The capability flag drives the CTA disabled state. We require BOTH ctx
  // sessionCode AND a resolved sessionCwd because the daemon needs the code
  // to resolve the root server-side; missing either is a hard "can't search".
  const sessionCapable = !!spec.ctx.sessionCode && !!sessionCwd

  return (
    <div
      ref={dialogRef}
      role="dialog"
      aria-modal="true"
      aria-labelledby="pdx-file-not-found-title"
      tabIndex={-1}
      className="fixed inset-0 bg-black/50 flex items-center justify-center z-50"
      onClick={onClose}
    >
      <div
        className="bg-bg-primary rounded-lg p-6 max-w-lg w-full text-text-primary"
        onClick={(e) => e.stopPropagation()}
      >
        <h3 id="pdx-file-not-found-title" className="text-base font-medium mb-2">
          File not found
        </h3>
        <p className="text-sm text-text-secondary mb-4 break-all font-mono">{spec.file.path}</p>

        {spec.mode === 'layer1-multi' && (
          <div className="mb-4">
            <h4 className="text-xs uppercase text-text-muted mb-1">Recent candidates</h4>
            <ul className="border border-border-subtle rounded">
              {spec.hits.map((hit) => (
                <li key={hit}>
                  <button
                    type="button"
                    onClick={() => onOpenPath(hit)}
                    className="w-full text-left px-2 py-1 text-sm font-mono hover:bg-surface-hover truncate"
                  >
                    {hit}
                  </button>
                </li>
              ))}
            </ul>
          </div>
        )}

        {spec.mode === 'expanded' && (
          <div className="mb-4">
            <ExpandedSection
              rootLabel={sessionCwd ?? '(unknown)'}
              hits={spec.layer2Hits.map((h) => h.path)}
              onOpenPath={onOpenPath}
            />
          </div>
        )}

        {(spec.mode === 'ask-expand' || spec.mode === 'layer1-multi') && (
          <div className="mb-4">
            <CtaButton
              label={t('file_not_found.search_session_cwd_label', { path: sessionCwd ?? '—' })}
              ariaLabel={t('file_not_found.search_session_cwd')}
              disabled={!sessionCapable}
              tooltip={
                sessionCapable
                  ? undefined
                  : 'No active session — cannot search session cwd'
              }
              onClick={onSearchSessionCwd}
            />
          </div>
        )}

        <div className="flex justify-end">
          <button
            type="button"
            onClick={onClose}
            className="px-3 py-1 text-sm text-text-secondary hover:text-text-primary"
          >
            Cancel
          </button>
        </div>
      </div>
    </div>
  )
}

interface CtaButtonProps {
  label: string
  ariaLabel: string
  disabled: boolean
  tooltip?: string
  onClick: () => void
}

function CtaButton({ label, ariaLabel, disabled, tooltip, onClick }: CtaButtonProps) {
  return (
    <button
      type="button"
      aria-label={ariaLabel}
      aria-disabled={disabled}
      title={tooltip}
      onClick={() => {
        if (!disabled) onClick()
      }}
      className={`w-full text-left px-3 py-2 text-sm rounded border border-border-subtle ${
        disabled
          ? 'opacity-50 cursor-not-allowed text-text-muted'
          : 'bg-surface-secondary hover:bg-surface-hover'
      }`}
    >
      {label}
    </button>
  )
}

interface ExpandedSectionProps {
  rootLabel: string
  hits: string[]
  onOpenPath: (path: string) => void
}

function ExpandedSection({ rootLabel, hits, onOpenPath }: ExpandedSectionProps) {
  return (
    <div>
      <h4 className="text-xs uppercase text-text-muted mb-1">{`Session cwd: ${rootLabel}`}</h4>
      {hits.length === 0 ? (
        <p className="text-xs text-text-muted px-2 py-1">No matches.</p>
      ) : (
        <ul className="border border-border-subtle rounded">
          {hits.map((hit) => (
            <li key={hit}>
              <button
                type="button"
                onClick={() => onOpenPath(hit)}
                className="w-full text-left px-2 py-1 text-sm font-mono hover:bg-surface-hover truncate"
              >
                {hit}
              </button>
            </li>
          ))}
        </ul>
      )}
    </div>
  )
}
