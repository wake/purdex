import { useEffect, useRef } from 'react'
import type { PopupSpec } from '../../../lib/file-open/open-file'

export interface FileNotFoundPopupProps {
  spec: PopupSpec
  /** Resolved cwd of the session (Layer 2 root). null → capability missing. */
  sessionCwd: string | null
  /** Resolved workspace projectPath (Layer 3 root). null → capability missing. */
  projectPath: string | null
  onClose: () => void
  /** Open a specific candidate path (cancel popup as side-effect). */
  onOpenPath: (path: string) => void
  /** Trigger Layer 2 fs.search (only meaningful when sessionCwd is non-null). */
  onSearchSessionCwd: () => void
  /** Trigger Layer 3 fs.search (only meaningful when projectPath is non-null). */
  onSearchWorkspace: () => void
}

/**
 * P5 file-not-found popup.
 *
 * The two primary CTAs surface the search root explicitly so the user knows
 * what scope they're opting into rather than seeing a generic "Search" button
 * (defensive review #4). When the underlying capability is absent (no
 * sessionCode → no sessionCwd, or no workspace projectPath), the relevant
 * button is disabled with an `aria-disabled` + tooltip explaining the gap.
 *
 * Focus trap is intentionally minimal — initial focus moves to the popup root
 * and ESC closes; tab order naturally cycles through the rendered buttons.
 * Layer 1 hits are read off `spec.hits` at render time so subsequent
 * path-cache mutations cannot mutate an already-mounted popup's options.
 */
export function FileNotFoundPopup({
  spec,
  sessionCwd,
  projectPath,
  onClose,
  onOpenPath,
  onSearchSessionCwd,
  onSearchWorkspace,
}: FileNotFoundPopupProps) {
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

  // Capability flags drive the CTA disabled state. We require BOTH ctx
  // sessionCode AND a resolved sessionCwd because the daemon needs the code
  // to resolve the root server-side; missing either is a hard "can't search".
  const sessionCapable = !!spec.ctx.sessionCode && !!sessionCwd
  const workspaceCapable = !!projectPath

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
          <div className="mb-4 space-y-3">
            <ExpandedSection
              titleScope="session"
              rootLabel={sessionCwd ?? '(unknown)'}
              hits={spec.layer2Hits.map((h) => h.path)}
              onOpenPath={onOpenPath}
            />
            <ExpandedSection
              titleScope="workspace"
              rootLabel={projectPath ?? '(unknown)'}
              hits={spec.layer3Hits.map((h) => h.path)}
              onOpenPath={onOpenPath}
            />
          </div>
        )}

        {(spec.mode === 'ask-expand' || spec.mode === 'layer1-multi') && (
          <div className="space-y-2 mb-4">
            <CtaButton
              label={`搜尋目前 session（cwd: ${sessionCwd ?? '—'}）`}
              ariaLabel="搜尋目前 session cwd"
              disabled={!sessionCapable}
              tooltip={
                sessionCapable
                  ? undefined
                  : 'No active session — cannot search session cwd'
              }
              onClick={onSearchSessionCwd}
            />
            <CtaButton
              label={`搜尋 workspace（projectPath: ${projectPath ?? '—'}）`}
              ariaLabel="搜尋 workspace projectPath"
              disabled={!workspaceCapable}
              tooltip={
                workspaceCapable
                  ? undefined
                  : 'Workspace projectPath not set — open Workspace settings to configure'
              }
              onClick={onSearchWorkspace}
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
  titleScope: 'session' | 'workspace'
  rootLabel: string
  hits: string[]
  onOpenPath: (path: string) => void
}

function ExpandedSection({ titleScope, rootLabel, hits, onOpenPath }: ExpandedSectionProps) {
  const heading =
    titleScope === 'session'
      ? `Session cwd: ${rootLabel}`
      : `Workspace projectPath: ${rootLabel}`
  return (
    <div>
      <h4 className="text-xs uppercase text-text-muted mb-1">{heading}</h4>
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
