// spa/src/components/session-launcher/SessionLauncher.tsx — the "new session"
// surface (host-launcher spec §5): a name field plus the host's projects, each
// card carrying the host's command icons. Presentational: it owns no transport,
// only the store reads, the keyboard model and the busy/error states; the
// caller decides what happens to the created session.
import { useEffect, useRef, useState } from 'react'
import { useLocation } from 'wouter'
import { FolderSimple } from '@phosphor-icons/react'
import { launchSession, SEND_UNSUPPORTED, type LaunchRequest } from '../../lib/session-launch'
import { isHostLive } from '../../lib/host-live'
import { isValidSessionName } from '../../lib/session-name'
import { encodeHostRouteId } from '../../lib/host-routes'
import type { Session } from '../../lib/host-api'
import { EMPTY_HOST_CONFIG, useHostConfigStore } from '../../stores/useHostConfigStore'
import { useI18nStore } from '../../stores/useI18nStore'
import { useUndoToast } from '../../stores/useUndoToast'
import { CommandIconView } from '../hosts/CommandIconView'

interface Props {
  hostId: string
  /**
   * The caller's liveness verdict at render time (host disconnected, or tmux
   * unavailable). `run()` re-checks it against the store as well, because a
   * launcher can sit open while the host drops.
   */
  disabled: boolean
  onLaunched: (session: Session) => void
  onCancel: () => void
  /** Injection point for tests; production uses the pinned launch helper. */
  launch?: typeof launchSession
}

const NEXT_KEYS = new Set(['ArrowDown', 'ArrowRight'])
const PREV_KEYS = new Set(['ArrowUp', 'ArrowLeft'])
const ACTIVATE_KEYS = new Set(['Enter', ' '])

export function SessionLauncher({ hostId, disabled, onLaunched, onCancel, launch = launchSession }: Props) {
  const t = useI18nStore((s) => s.t)
  const [, setLocation] = useLocation()
  const config = useHostConfigStore((s) => s.byHost[hostId] ?? EMPTY_HOST_CONFIG)
  const [name, setName] = useState('')
  const [triedEnter, setTriedEnter] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const busyRef = useRef(false)
  const aliveRef = useRef(true)
  const rootRef = useRef<HTMLDivElement>(null)
  const inputRef = useRef<HTMLInputElement>(null)

  // Set on every setup (not just at ref creation) so React 19 StrictMode's
  // setup → cleanup → setup double-invoke leaves a mounted launcher alive.
  useEffect(() => {
    aliveRef.current = true
    return () => { aliveRef.current = false }
  }, [])

  useEffect(() => {
    void useHostConfigStore.getState().ensureLoaded(hostId)
  }, [hostId])

  const trimmed = name.trim()
  const nameError = trimmed
    ? (isValidSessionName(trimmed) ? '' : t('tab.rename_invalid_format'))
    : (triedEnter ? t('launcher.name_required') : '')
  const locked = busy || disabled

  const run = async (req: LaunchRequest) => {
    if (busyRef.current || disabled) return
    // The host may have dropped since this launcher opened: never fire a create
    // at a daemon that is no longer there.
    if (!isHostLive(hostId)) { setError(t('launcher.offline')); return }
    busyRef.current = true
    setBusy(true)
    setError('')
    try {
      const outcome = await launch(hostId, req)
      if (!aliveRef.current) return
      if (outcome.status === 'failed') {
        setError(outcome.error)
        return
      }
      if (outcome.sendError) {
        useUndoToast.getState().show(outcome.sendError === SEND_UNSUPPORTED
          ? t('launcher.send_unsupported')
          : t('launcher.send_failed', { reason: outcome.sendError }))
      }
      // The host can drop between the create and the attach. The session exists
      // — say so and stay open, rather than closing on a pane that will never
      // appear. The caller only ever receives a session its host is still live
      // for, so it has no offline case of its own to render.
      if (!isHostLive(hostId)) { setError(t('launcher.created_offline')); return }
      onLaunched(outcome.session)
    } finally {
      busyRef.current = false
      if (aliveRef.current) setBusy(false)
    }
  }

  const items = (): HTMLElement[] =>
    Array.from(rootRef.current?.querySelectorAll<HTMLElement>('[data-launch-item]:not(:disabled)') ?? [])

  const handleInputKey = (e: React.KeyboardEvent<HTMLInputElement>) => {
    if (e.nativeEvent.isComposing) return
    if (e.key === 'Enter') {
      e.preventDefault()
      setTriedEnter(true)
      if (!trimmed || !isValidSessionName(trimmed)) return
      void run({ name: trimmed })
    } else if (e.key === 'ArrowDown') {
      e.preventDefault()
      items()[0]?.focus()
    } else if (e.key === 'Escape') {
      e.preventDefault()
      onCancel()
    }
  }

  const handleGridKey = (e: React.KeyboardEvent<HTMLElement>) => {
    if (e.key === 'Escape') { e.preventDefault(); onCancel(); return }
    const next = NEXT_KEYS.has(e.key)
    if (!next && !PREV_KEYS.has(e.key)) return
    const list = items()
    const index = list.indexOf(e.target as HTMLElement)
    if (index === -1) return
    e.preventDefault()
    if (next) list[Math.min(index + 1, list.length - 1)]?.focus()
    else if (index === 0) inputRef.current?.focus()
    else list[index - 1]?.focus()
  }

  /**
   * One launchable item's handlers. The key handler cancels the native
   * activation, so Enter/Space launch exactly once instead of also arriving as
   * the browser's synthesized click (Space's click lands on keyup, by which
   * time the busy guard may already have been released).
   */
  const activate = (req: LaunchRequest) => ({
    onClick: () => { void run(req) },
    onKeyDown: (e: React.KeyboardEvent) => {
      if (!ACTIVATE_KEYS.has(e.key) || e.nativeEvent.isComposing) return
      e.preventDefault()
      void run(req)
    },
  })

  const itemCls = 'cursor-pointer rounded focus:outline-none focus:ring-1 focus:ring-accent disabled:opacity-50 disabled:cursor-not-allowed'

  return (
    <div ref={rootRef} data-testid="launcher" className="@container flex flex-col gap-2 p-2 bg-surface-secondary border border-border-default rounded-md">
      <input
        ref={inputRef}
        data-testid="launcher-name"
        autoFocus
        value={name}
        disabled={locked}
        spellCheck={false}
        placeholder={t('launcher.name_placeholder')}
        onChange={(e) => setName(e.target.value)}
        onKeyDown={handleInputKey}
        className="w-full bg-surface-primary border border-border-default rounded px-2 py-1 text-sm text-text-primary disabled:opacity-50"
      />
      {nameError && <p data-testid="launcher-name-error" className="text-xs text-red-400">{nameError}</p>}
      {error && <p data-testid="launcher-error" className="text-xs text-red-400">{error}</p>}

      {config.status === 'unsupported' ? (
        <p data-testid="launcher-unsupported" className="text-xs text-text-muted">{t('launcher.unsupported')}</p>
      ) : config.projects.length === 0 ? (
        <p data-testid="launcher-empty" className="text-xs text-text-muted">
          {t('launcher.empty')}{' '}
          <button type="button" data-testid="launcher-empty-link" className="text-accent hover:underline cursor-pointer"
            onClick={() => { setLocation(`/hosts/${encodeHostRouteId(hostId)}/projects`); onCancel() }}>
            {t('launcher.empty_link')}
          </button>
        </p>
      ) : (
        <div data-testid="launcher-grid" className="grid grid-cols-2 @md:grid-cols-3 @3xl:grid-cols-4 gap-2" onKeyDown={handleGridKey}>
          {config.projects.map((project) => (
            <div key={project.id} data-testid={`launcher-project-${project.id}`}
              className="min-w-0 flex flex-col gap-1 p-2 rounded border border-border-subtle bg-surface-primary">
              <button type="button" data-launch-item data-testid={`launcher-project-name-${project.id}`} disabled={locked}
                {...activate({ name: trimmed, project })}
                className={`flex items-center gap-1.5 text-left text-sm font-bold text-text-primary min-w-0 ${itemCls}`}>
                <FolderSimple size={14} className="shrink-0 text-text-secondary" />
                <span className="truncate">{project.name}</span>
              </button>
              <span className="truncate text-xs text-text-muted font-mono" title={project.path}>{project.path}</span>
              {config.commands.length > 0 && (
                <div className="flex flex-wrap gap-1">
                  {config.commands.map((command) => (
                    <button key={command.id} type="button" data-launch-item
                      data-testid={`launcher-command-${project.id}-${command.id}`}
                      title={command.name} aria-label={`${project.name} · ${command.name}`} disabled={locked}
                      {...activate({ name: trimmed, project, command })}
                      className={`p-1 text-text-secondary hover:text-text-primary hover:bg-surface-hover ${itemCls}`}>
                      <CommandIconView icon={command.icon} size={16} />
                    </button>
                  ))}
                </div>
              )}
            </div>
          ))}
        </div>
      )}
    </div>
  )
}
