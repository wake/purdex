/**
 * DeleteConfirmDialog — the confirmation Storage shows before deleting a SET of
 * entries, listing every path it is about to remove.
 *
 * Both callers need the same thing and for the same reason: a bare count
 * ("Delete 3 buffer(s)?") tells the user nothing about *which* files are going.
 * Clean Empty deletes files the user never explicitly selected, and a batch
 * delete acts on a set of path strings captured at click time that the tree may
 * have moved on from. Naming the paths is what lets the user notice either.
 *
 * It is deliberately a dumb view: the caller owns the paths, the verification
 * and the actual delete. `testIdPrefix` keeps the two call sites individually
 * addressable from tests (`empty-cleanup-*` / `delete-selection-*`).
 */
export function DeleteConfirmDialog({
  testIdPrefix,
  title,
  message,
  note,
  paths,
  confirmLabel,
  cancelLabel,
  onCancel,
  onConfirm,
}: {
  testIdPrefix: string
  title: string
  message: string
  /** Optional second line — e.g. "N selected entries no longer exist". */
  note?: string
  paths: string[]
  confirmLabel: string
  cancelLabel: string
  onCancel: () => void
  onConfirm: () => void
}) {
  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/50"
      data-testid={`${testIdPrefix}-dialog`}
    >
      <div className="flex max-h-[80vh] w-[420px] flex-col rounded-lg border border-border-default bg-surface-primary shadow-lg">
        <div className="border-b border-border-subtle px-4 py-3">
          <h3 className="text-sm font-medium text-text-primary">{title}</h3>
          <p className="mt-1 text-xs text-text-muted">{message}</p>
          {note && (
            <p data-testid={`${testIdPrefix}-note`} className="mt-1 text-xs text-amber-400">
              {note}
            </p>
          )}
        </div>
        <ul className="flex-1 overflow-y-auto px-4 py-3 text-xs text-text-secondary">
          {paths.map((path) => (
            <li key={path} data-testid={`${testIdPrefix}-item`} className="truncate py-0.5">
              {path}
            </li>
          ))}
        </ul>
        <div className="flex justify-end gap-2 border-t border-border-subtle px-4 py-3">
          <button
            data-testid={`${testIdPrefix}-cancel`}
            onClick={onCancel}
            className="px-3 py-1 rounded-md text-xs text-text-secondary hover:bg-surface-hover"
          >
            {cancelLabel}
          </button>
          <button
            data-testid={`${testIdPrefix}-confirm`}
            onClick={onConfirm}
            className="px-3 py-1 rounded-md text-xs text-text-primary bg-surface-secondary hover:bg-surface-hover hover:text-status-error"
          >
            {confirmLabel}
          </button>
        </div>
      </div>
    </div>
  )
}
