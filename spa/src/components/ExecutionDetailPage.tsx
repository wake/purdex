// spa/src/components/ExecutionDetailPage.tsx — read-only execution detail
// landing (M0 dispatch, Task P.12). Fetches GET /api/execution/{id} and renders
// status + artifacts. It is strictly observe-only: it never mounts a terminal,
// never opens a stdin channel; the only interactive affordance is a button that
// *focuses an already-open* session tab (also observe-only).
import { useEffect, useState } from 'react'
import {
  fetchExecutionView,
  resolveExecutionHostId,
  type ExecutionView,
} from '../lib/execution-api'
import { focusExistingSessionTab } from '../lib/deeplink/deeplinkResolver'
import { findTabBySessionCode } from '../lib/pane-tree'
import { useTabStore } from '../stores/useTabStore'

interface ExecutionDetailPageProps {
  executionId: string
  host?: string
}

type LoadState =
  | { phase: 'loading' }
  | { phase: 'error'; message: string }
  | { phase: 'not-found' }
  | { phase: 'ready'; view: ExecutionView }

// Map runtime status → a human label. `accepted` is surfaced as "Queued" to
// match Ploom's projection vocabulary (queued/running/completed/failed).
const STATUS_LABEL: Record<string, string> = {
  accepted: 'Queued',
  running: 'Running',
  completed: 'Completed',
  failed: 'Failed',
}

function statusLabel(status: string): string {
  return STATUS_LABEL[status] ?? status
}

function diffArtifact(view: ExecutionView) {
  return view.artifacts?.find((a) => a.kind === 'diff')
}

export function ExecutionDetailPage({ executionId, host }: ExecutionDetailPageProps) {
  const [state, setState] = useState<LoadState>({ phase: 'loading' })
  // Bumping this re-runs the fetch effect (Retry). State only ever transitions
  // *after* the await, so the effect never setStates synchronously.
  const [reloadKey, setReloadKey] = useState(0)
  // Subscribe so the observe-only "focus session" affordance appears/disappears
  // as the matching tab opens or closes.
  const tabs = useTabStore((s) => s.tabs)

  useEffect(() => {
    let cancelled = false
    const hostId = resolveExecutionHostId(host)
    fetchExecutionView(hostId, executionId)
      .then((view) => {
        if (cancelled) return
        setState(view ? { phase: 'ready', view } : { phase: 'not-found' })
      })
      .catch((err: unknown) => {
        if (cancelled) return
        setState({ phase: 'error', message: err instanceof Error ? err.message : String(err) })
      })
    return () => { cancelled = true }
  }, [executionId, host, reloadKey])

  // Retry is a user event (not an effect), so resetting to loading here is fine.
  const retry = () => {
    setState({ phase: 'loading' })
    setReloadKey((k) => k + 1)
  }

  if (state.phase === 'loading') {
    return (
      <div className="flex-1 p-6 text-sm text-text-muted" data-testid="execution-loading">
        Loading execution…
      </div>
    )
  }

  if (state.phase === 'not-found') {
    return (
      <div className="flex-1 p-6" data-testid="execution-not-found">
        <h2 className="text-lg font-semibold text-text-primary">Execution not found</h2>
        <p className="mt-2 text-sm text-text-muted">
          No execution <code>{executionId}</code> on this daemon. It may have been pruned, or the
          deeplink points at a different host.
        </p>
      </div>
    )
  }

  if (state.phase === 'error') {
    return (
      <div className="flex-1 p-6" data-testid="execution-error">
        <h2 className="text-lg font-semibold text-text-primary">Couldn’t load execution</h2>
        <p className="mt-2 text-sm text-text-muted">{state.message}</p>
        <button
          type="button"
          onClick={retry}
          className="mt-4 rounded border border-border px-3 py-1.5 text-sm text-text-primary hover:bg-surface-hover"
        >
          Retry
        </button>
      </div>
    )
  }

  const { view } = state
  const diff = diffArtifact(view)
  const openTabId = view.session_code ? findTabBySessionCode(tabs, view.session_code) : undefined

  return (
    <div className="flex-1 overflow-auto p-6" data-testid="execution-detail">
      <header className="mb-6">
        <div className="flex items-center gap-3">
          <h2 className="text-xl font-semibold text-text-primary">Execution</h2>
          <span
            className="rounded-full bg-surface px-2.5 py-0.5 text-xs font-medium text-text-secondary"
            data-testid="execution-status"
          >
            {statusLabel(view.status)}
          </span>
        </div>
        <code className="mt-1 block text-xs text-text-muted">{view.execution_id}</code>
      </header>

      <dl className="grid grid-cols-[max-content_1fr] gap-x-6 gap-y-2 text-sm">
        <dt className="text-text-muted">Dispatch</dt>
        <dd className="text-text-primary"><code>{view.dispatch_id}</code></dd>
        <dt className="text-text-muted">Provider</dt>
        <dd className="text-text-primary">{view.provider}</dd>
        <dt className="text-text-muted">Repo</dt>
        <dd className="break-all text-text-primary">{view.repo_location || '—'}</dd>
        <dt className="text-text-muted">Base commit</dt>
        <dd className="text-text-primary"><code>{view.head_at_start || '—'}</code></dd>
        <dt className="text-text-muted">Sandbox</dt>
        <dd className="text-text-primary">{view.sandbox_profile || '—'}</dd>
        {view.outcome_source ? (
          <>
            <dt className="text-text-muted">Outcome</dt>
            <dd className="text-text-primary">{view.outcome_source}</dd>
          </>
        ) : null}
        {view.session_code ? (
          <>
            <dt className="text-text-muted">Session</dt>
            <dd className="text-text-primary"><code>{view.session_code}</code></dd>
          </>
        ) : null}
      </dl>

      {diff ? (
        <section className="mt-6" data-testid="execution-diff">
          <h3 className="text-sm font-semibold text-text-primary">Changes</h3>
          <p className="mt-1 text-sm text-text-muted">
            {String(diff.meta?.files ?? 0)} files,{' '}
            <span className="text-green-500">+{String(diff.meta?.add ?? 0)}</span>{' '}
            <span className="text-red-500">−{String(diff.meta?.del ?? 0)}</span>
          </p>
        </section>
      ) : null}

      {view.session_code && openTabId ? (
        <section className="mt-6" data-testid="execution-observe">
          <button
            type="button"
            onClick={() => focusExistingSessionTab(view.session_code as string)}
            className="rounded border border-border px-3 py-1.5 text-sm text-text-primary hover:bg-surface-hover"
          >
            Observe session output
          </button>
          <p className="mt-1 text-xs text-text-muted">Read-only — dispatched runs don’t accept input.</p>
        </section>
      ) : null}
    </div>
  )
}
