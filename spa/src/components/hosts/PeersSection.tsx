// spa/src/components/hosts/PeersSection.tsx — Hosts › Peers (Phase D spec §5).
// Renders a PairingSnapshot from loadPairings and wires three buttons:
// Refresh, and one Rename per direction when the peer's self alias drifts
// from the entry name (spec D-5: adoption is a plain PUT {alias}). Nothing
// here is cached or persisted; the snapshot lives in useState and dies with
// the component (spec D-6, D-8).
import { useCallback, useEffect, useRef, useState } from 'react'
import { ArrowsClockwise, CheckCircle, WarningCircle, Circle } from '@phosphor-icons/react'
import { useHostStore } from '../../stores/useHostStore'
import { useI18nStore } from '../../stores/useI18nStore'
import {
  HostApiError, fetchHostInfo, fetchPeerSettings, listPeerHosts, updatePeerHost, verifyPeerHost,
} from '../../lib/host-api'
import { aliasDrift, pairStatus, type PairStatus, type Side } from '../../lib/peer-pairing'
import { loadPairings, type PairingApi, type PairingAppHost, type PairingRow, type PairingSnapshot } from '../../lib/peer-pairing-load'

const STATUS_CLASS: Record<PairStatus, string> = {
  bidirectional: 'text-status-success',
  'one-way': 'text-status-warning',
  unpaired: 'text-status-error',
  'outbound-only': 'text-text-muted',     // half-verifiable, never green (spec D-4)
  'return-unknown': 'text-text-muted',    // transient, never green
  checking: 'text-text-muted',
}

interface Props { hostId: string }

export function PeersSection({ hostId }: Props) {
  const t = useI18nStore((s) => s.t)
  const hosts = useHostStore((s) => s.hosts)
  const host = hosts[hostId]

  const [snap, setSnap] = useState<PairingSnapshot | null>(null)
  const [busy, setBusy] = useState(false)
  // Generation counter: a snapshot from a run started for a previous hostId
  // (or a previous Refresh) must never paint over the current one.
  const gen = useRef(0)
  // A row's onRenamed closes over the render-time hostId. If updatePeerHost
  // resolves after hostId changed (or the section unmounted), calling run()
  // from that stale closure would start a NEW generation for the OLD hostId
  // and repaint it over the current page — gen ordering alone does not catch
  // this because the stale run becomes the newest generation. liveHost /
  // alive let each onRenamed check "is my hostId still current, and is this
  // component still mounted" before restarting the page.
  const liveHost = useRef(hostId)
  liveHost.current = hostId
  const alive = useRef(true)
  useEffect(() => {
    alive.current = true
    return () => { alive.current = false }
  }, [])

  const run = useCallback(async () => {
    const my = ++gen.current
    setBusy(true)
    const { hosts: hs, hostOrder: order, runtime: rt, getDaemonBase } = useHostStore.getState()
    const toApp = (id: string): PairingAppHost => ({ hostId: id, name: hs[id]?.name ?? id, url: getDaemonBase(id), status: rt[id]?.status })
    const others = order.filter((id) => id !== hostId).map(toApp)
    const emit = (s: PairingSnapshot) => { if (gen.current === my) setSnap(s) }
    // Built per run, not at module load: tests that fully mock host-api
    // (without importOriginal) and load the module registry must not blow
    // up just from this file being imported.
    const api: PairingApi = { info: fetchHostInfo, settings: fetchPeerSettings, list: listPeerHosts, verify: verifyPeerHost }
    try {
      await loadPairings(toApp(hostId), others, api, emit)
    } finally {
      if (gen.current === my) setBusy(false)
    }
  }, [hostId])

  useEffect(() => {
    setSnap(null)
    void run()
    // eslint-disable-next-line react-hooks/exhaustive-deps -- gen is a run counter; the latest value is the point
    return () => { gen.current++ }
  }, [run])

  if (!host) return null

  return (
    <div className="max-w-3xl">
      <div className="flex items-center justify-between mb-1">
        <h2 className="text-lg font-semibold">{t('hosts.peers')}</h2>
        <button type="button" data-testid="peers-refresh" disabled={busy} onClick={() => void run()}
          className="flex items-center gap-1.5 px-3 py-1.5 rounded text-xs bg-surface-tertiary text-text-secondary hover:text-text-primary cursor-pointer disabled:opacity-50">
          <ArrowsClockwise size={14} className={busy ? 'animate-spin' : ''} />{busy ? t('peers.checking') : t('peers.refresh')}
        </button>
      </div>
      <p className="text-xs text-text-muted mb-1">{t('peers.desc')}</p>
      {/* The selected host's own identity, labelled: the third of the three names (spec §2.1).
          It is what the return direction's entry on the counterpart must point at. */}
      {snap?.self && (
        <p data-testid="peers-self" className="text-xs text-text-muted mb-4 font-mono">
          {host.name} · {t('peers.self_alias_label')}: <span className="text-text-secondary">{snap.self.self_alias}</span> · {snap.self.host_id}
        </p>
      )}

      {snap?.error && (
        <div data-testid="peers-banner" className="flex items-start justify-between gap-3 px-3 py-2.5 rounded-md mb-4 bg-red-500/10 border border-red-500/20">
          <p className="text-sm text-red-400">{t('peers.banner', { call: snap.error.call, message: snap.error.message })}</p>
          <button type="button" data-testid="peers-retry" disabled={busy} onClick={() => void run()}
            className="text-xs px-2 py-1 rounded bg-surface-tertiary cursor-pointer disabled:opacity-50">{t('peers.retry')}</button>
        </div>
      )}

      {snap && !snap.error && snap.rows.length === 0 && (
        <p data-testid="peers-empty" className="text-sm text-text-muted">{t('peers.empty')}</p>
      )}

      {snap && !snap.error && snap.rows.length > 0 && (
        <div className="space-y-3">
          {snap.rows.map((row) => (
            // Keyed by host + alias: in production HostPage remounts the section
            // per host, this makes the prop-change path safe too, and the
            // unmounted DirectionLine's late setState is a no-op in React 19.
            <PeerRow key={`${hostId}:${row.entry.alias}`} hostId={hostId} hostName={host.name} self={snap.self!} row={row}
              busy={busy}
              onRenamed={() => { if (alive.current && liveHost.current === hostId) void run() }} />
          ))}
        </div>
      )}
    </div>
  )
}

/* ─── One entry ─── */

interface RowProps {
  hostId: string
  hostName: string
  self: { host_id: string; self_alias: string }
  row: PairingRow
  busy: boolean
  onRenamed: () => void
}

function PeerRow({ hostId, hostName, self, row, busy, onRenamed }: RowProps) {
  const t = useI18nStore((s) => s.t)
  const status = pairStatus(row.outbound, row.inbound)
  const { entry, counterpart, returnEntry } = row

  const outDrift = row.outbound !== 'pending' && row.outbound.ok ? aliasDrift(entry.alias, row.outbound.self_alias) : ''
  const inDrift = returnEntry && typeof row.inbound === 'object' && row.inbound.ok ? aliasDrift(returnEntry.alias, row.inbound.self_alias) : ''

  return (
    <div data-testid={`peer-row-${entry.alias}`} className="border border-border-subtle rounded-lg px-4 py-3 text-sm">
      <div className="flex items-baseline gap-2 flex-wrap">
        <span data-testid="peer-alias" className="font-semibold text-text-primary">{entry.alias}</span>
        {counterpart && (
          <span data-testid="peer-app-host" className="text-xs text-text-secondary">{t('peers.app_host', { name: counterpart.name })}</span>
        )}
        <span data-testid="peer-status" data-status={status} className={`ml-auto text-xs font-medium ${STATUS_CLASS[status]}`}>
          {t('peers.status')}: {t(`peers.status.${status}`)}
        </span>
      </div>
      <div className="font-mono text-xs text-text-muted mt-0.5">
        <span data-testid="peer-url">{entry.url}</span>
        {entry.host_id && <> · <span data-testid="peer-host-id">{entry.host_id}</span></>}
      </div>

      <div className="mt-2 space-y-1.5">
        {/* Outbound: X → peer, verify(X, E.alias) */}
        <DirectionLine testId="peer-outbound" from={hostName} to={entry.alias} side={row.outbound}
          drift={outDrift} renameTarget={{ hostId, alias: entry.alias }} busy={busy} onRenamed={onRenamed} />

        {/* Return: peer → X, verify(Y, E'.alias) or one of the three sentences */}
        {counterpart && returnEntry ? (
          <DirectionLine testId="peer-inbound" from={entry.alias} to={hostName} side={row.inbound as Side}
            note={t('peers.their_entry', { name: counterpart.name, alias: returnEntry.alias })}
            drift={inDrift} renameTarget={{ hostId: counterpart.hostId, alias: returnEntry.alias }} busy={busy} onRenamed={onRenamed} />
        ) : (
          <div data-testid="peer-inbound" data-ok="none" className="flex items-center gap-2 text-text-muted">
            <Circle size={14} />
            <span className="font-mono text-xs">{t('peers.direction', { from: entry.alias, to: hostName })}</span>
            <span className="text-xs">{returnSentence(t, row, self, counterpart)}</span>
          </div>
        )}
      </div>
    </div>
  )
}

function returnSentence(
  t: (k: string, p?: Record<string, string | number>) => string,
  row: PairingRow, self: { self_alias: string }, counterpart: { name: string } | null,
): string {
  if (row.inbound === 'not-app-host' || !counterpart) return t('peers.not_verifiable')
  if (row.inbound === 'counterpart-unavailable') return t('peers.could_not_ask', { name: counterpart.name, cause: row.counterpartCause })
  return t('peers.no_entry', { alias: self.self_alias, name: counterpart.name })
}

/* ─── One direction ─── */

interface LineProps {
  testId: string
  from: string
  to: string
  side: Side
  note?: string
  drift: string
  renameTarget: { hostId: string; alias: string }
  busy: boolean
  onRenamed: () => void
}

function DirectionLine({ testId, from, to, side, note, drift, renameTarget, busy, onRenamed }: LineProps) {
  const t = useI18nStore((s) => s.t)
  const [renaming, setRenaming] = useState(false)
  const [error, setError] = useState<string | null>(null)

  // A stale rename error must not survive past the drift it was about — a
  // Refresh that adopts the alias (or otherwise changes what Rename would
  // do) clears the old 409/400 text along with it.
  useEffect(() => { setError(null) }, [drift])

  const rename = async () => {
    setRenaming(true)
    setError(null)
    try {
      await updatePeerHost(renameTarget.hostId, renameTarget.alias, { alias: drift })
      onRenamed()
    } catch (e) {
      // 400/409 carry the daemon's own sentence; nothing is retried or auto-suffixed (v4 §7.2).
      setError(e instanceof HostApiError ? e.detail : e instanceof Error ? e.message : String(e))
    } finally {
      setRenaming(false)
    }
  }

  const ok = side === 'pending' ? 'pending' : String(side.ok)
  const Icon = side === 'pending' ? Circle : side.ok ? CheckCircle : WarningCircle
  const colour = side === 'pending' ? 'text-text-muted' : side.ok ? 'text-status-success' : 'text-status-error'

  return (
    <div data-testid={testId} data-ok={ok} className="flex items-center gap-2 flex-wrap">
      <Icon size={14} weight={side === 'pending' ? 'regular' : 'fill'} className={colour} />
      <span className="font-mono text-xs text-text-secondary">{t('peers.direction', { from, to })}</span>
      <span className="text-xs text-text-secondary">
        {side === 'pending' ? t('peers.checking')
          : side.ok ? t('peers.reachable', { version: side.daemon_version })
          : t('peers.failed', { error: side.error })}
      </span>
      {note && <span className="text-xs text-text-muted">{note}</span>}
      {/* The peer's self alias, spec §5.3: shown whenever it is known, with a
          separate drift marker + Rename only when it disagrees with the entry name. */}
      {side !== 'pending' && side.ok && side.self_alias && (
        <span data-testid={`${testId}-self-alias`} className={`text-xs ${drift ? 'text-status-warning' : 'text-text-muted'}`}>
          {t('peers.calls_itself', { alias: side.self_alias })}
        </span>
      )}
      {drift && (
        <span data-testid={`${testId}-drift`} className="text-xs text-status-warning">
          ({t('peers.drift')})
        </span>
      )}
      {drift && (
        <button type="button" data-testid={`${testId}-rename`} disabled={busy || renaming} onClick={() => void rename()}
          className="text-xs px-2 py-0.5 rounded bg-accent text-white cursor-pointer disabled:opacity-50">
          {renaming ? t('peers.renaming') : t('peers.rename_to', { alias: drift })}
        </button>
      )}
      {error && <span data-testid={`${testId}-rename-error`} className="text-xs text-status-error whitespace-pre-wrap">{error}</span>}
    </div>
  )
}
