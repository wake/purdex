// spa/src/components/headless/HeadlessLauncher.tsx — the New Tab "Headless"
// section for one host (P-C spec §4.2): routes on the host's Nexen readiness
// from `useNexHostStore` and, when ready, renders a delegate form driven
// entirely by the host's own capabilities (roots, profiles, brief limit).
// The form owns its field state and validity; sending and the outcome
// mapping live in `useHeadlessLaunchSubmit`; the caller decides what a
// created execution becomes.
import { useEffect, useState } from 'react'
import { useLocation } from 'wouter'
import type { NexCapabilities } from '../../lib/nex/types'
import { joinCwd, utf8ByteLength, validateSubPath } from '../../lib/nex/cwd-input'
import { encodeHostRouteId } from '../../lib/host-routes'
import type { PaneContent } from '../../types/tab'
import { useNexHostStore } from '../../stores/useNexHostStore'
import { useHeadlessLauncherMemoryStore } from '../../stores/useHeadlessLauncherMemoryStore'
import { useI18nStore } from '../../stores/useI18nStore'
import { useHeadlessLaunchSubmit } from '../../hooks/useHeadlessLaunchSubmit'
import { HeadlessLauncherFields } from './HeadlessLauncherFields'

interface Props {
  hostId: string
  onSelect: (content: PaneContent) => void
}

/** Spec F1: the daemon's brief limit when an older capabilities payload omits it. */
export const DEFAULT_BRIEF_MAX_BYTES = 65536

export function HeadlessLauncher({ hostId, onSelect }: Props) {
  const t = useI18nStore((s) => s.t)
  const [, setLocation] = useLocation()
  const entry = useNexHostStore((s) => s.byHost[hostId])

  useEffect(() => {
    void useNexHostStore.getState().ensure(hostId)
  }, [hostId])

  const phase = entry?.phase ?? 'loading'
  if (phase === 'loading' || phase === 'unknown') {
    return (
      <div data-testid="headless-loading" className="flex flex-col gap-2 p-2 animate-pulse" aria-busy="true">
        <span className="text-xs text-text-muted">{t('newtab.headless.loading')}</span>
        <div className="h-16 rounded bg-surface-secondary" />
        <div className="h-7 w-1/2 rounded bg-surface-secondary" />
      </div>
    )
  }
  if (phase === 'disabled') {
    return (
      <button
        type="button"
        data-testid="headless-disabled"
        onClick={() => setLocation(`/hosts/${encodeHostRouteId(hostId)}/nex`)}
        className="text-left text-xs text-text-muted hover:text-accent cursor-pointer"
      >
        {t('newtab.headless.disabled')}
      </button>
    )
  }
  if (phase !== 'ready' || !entry?.capabilities) {
    return (
      <p data-testid="headless-unavailable" className="text-xs text-red-400">
        {t('newtab.headless.unavailable', { error: entry?.error ?? '' })}
      </p>
    )
  }
  return <HeadlessForm hostId={hostId} caps={entry.capabilities} onSelect={onSelect} />
}

/** The user's explicit pick if the host still offers it, else what it remembered, else the host's default. */
function pick(choice: string | null, remembered: string | undefined, offered: string[], fallback: string): string {
  if (choice !== null && offered.includes(choice)) return choice
  if (remembered !== undefined && offered.includes(remembered)) return remembered
  return fallback
}

function HeadlessForm({ hostId, caps, onSelect }: Props & { caps: NexCapabilities }) {
  const remembered = useHeadlessLauncherMemoryStore((s) => s.byHost[hostId])
  const [brief, setBrief] = useState('')
  const [rootChoice, setRootChoice] = useState<string | null>(null)
  const [sub, setSub] = useState('')
  const [profileChoice, setProfileChoice] = useState<string | null>(null)
  const { busy, error, submit } = useHeadlessLaunchSubmit(hostId, caps, onSelect)

  const rootPaths = caps.roots.map((r) => r.path)
  const root = pick(rootChoice, remembered?.root, rootPaths, rootPaths[0] ?? '')
  const profile = pick(profileChoice, remembered?.profile, caps.sandbox_profiles, caps.sandbox_default_profile)
  const maxBytes = caps.brief?.max_bytes ?? DEFAULT_BRIEF_MAX_BYTES
  const usedBytes = utf8ByteLength(brief)
  const subVerdict = validateSubPath(sub)
  const canSubmit = !busy && rootPaths.length > 0 && brief.trim() !== '' && usedBytes <= maxBytes && subVerdict.ok

  const run = () => {
    if (!canSubmit || !subVerdict.ok) return
    void submit({ brief, cwd: joinCwd(root, subVerdict.value), root, profile })
  }

  return (
    <HeadlessLauncherFields
      caps={caps}
      brief={brief}
      usedBytes={usedBytes}
      maxBytes={maxBytes}
      root={root}
      sub={sub}
      subVerdict={subVerdict}
      profile={profile}
      busy={busy}
      canSubmit={canSubmit}
      error={error}
      onBrief={setBrief}
      onRoot={setRootChoice}
      onSub={setSub}
      onProfile={setProfileChoice}
      onSubmit={run}
    />
  )
}
