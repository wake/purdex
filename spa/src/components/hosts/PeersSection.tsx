// spa/src/components/hosts/PeersSection.tsx — Hosts › Peers (Phase D spec §5, §7).
// Renders a PairingSnapshot from loadPairings and wires the page's actions:
// Refresh, one Rename per direction when the peer's self alias drifts (spec
// D-5), Unpair per row (§7.2), Rotate / Commit / Cancel per direction line
// (§7.3, in RotationControls) and "Pair with…" below the rows (§7.1, in
// PairWithSection). Every write flow ends in a refresh (§7.4): the page's
// claim is always the result of the last dial, never of the last write.
//
// Nothing here is cached or persisted; the snapshot and the one flow's
// state live in useState and die with the component (spec D-6, D-8). A
// token value only ever exists inside the flow that consumes it.
import { useCallback, useEffect, useRef, useState, type ReactNode } from 'react'
import { ArrowsClockwise, CheckCircle, WarningCircle, Circle } from '@phosphor-icons/react'
import { useHostStore } from '../../stores/useHostStore'
import { hostLabel, hostLookOf, useHostLook } from '../../lib/host-look'
import { useI18nStore } from '../../stores/useI18nStore'
import { ConfirmDialog } from '../ConfirmDialog'
import {
  HostApiError, addPeerHost, fetchHostInfo, fetchPeerSettings, listPeerHosts, updatePeerHost, verifyPeerHost,
} from '../../lib/host-api'
import { aliasDrift, pairStatus, type PairStatus, type Side } from '../../lib/peer-pairing'
import { loadPairings, type PairingApi, type PairingAppHost, type PairingRow, type PairingSnapshot } from '../../lib/peer-pairing-load'
import { unpairHosts, type Report } from '../../lib/peer-pairing-actions'
import { actionApi, errText, rowKey, candidateKey, selfKey, type BoundRunFlow, type FlowResult, type FlowState, type RunFlow } from './peers/flow'
import { FlowNote } from './peers/FlowNote'
import { PairWithSection } from './peers/PairWithSection'
import { RotationControls } from './peers/RotationControls'
import { SelfAliasLine } from './peers/SelfAliasLine'

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
  const xUrl = useHostStore((s) => s.getDaemonBase(hostId))
  const host = hosts[hostId]
  const hostName = hostLabel(hostId, useHostLook(hostId))

  const [snap, setSnap] = useState<PairingSnapshot | null>(null)
  const [busy, setBusy] = useState(false)
  // The one write flow at a time: which row/candidate it belongs to, the step
  // it is on, and what it left behind (kept until the next flow).
  const [flow, setFlow] = useState<FlowState | null>(null)
  // Generation counter: a snapshot from a run started for a previous hostId
  // (or a previous Refresh) must never paint over the current one.
  const gen = useRef(0)
  // A row's callbacks close over the render-time hostId. If a write resolves
  // after hostId changed (or the section unmounted), calling run() from that
  // stale closure would start a NEW generation for the OLD hostId and repaint
  // it over the current page — gen ordering alone does not catch this because
  // the stale run becomes the newest generation. liveHost / alive let each
  // callback check "is my hostId still current, and is this component still
  // mounted" before restarting the page.
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
    const toApp = (id: string): PairingAppHost => ({ hostId: id, name: hostLabel(id, hostLookOf(id, hs)), url: getDaemonBase(id), status: rt[id]?.status })
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

  // One flow at a time; every flow ends with the refresh (spec §7.4). What the
  // flow reports (steps) and returns (error/hint) is text only — no outcome
  // ever carries a token (D-8).
  //
  // A flow is owned by a generation: flowGen is bumped when a flow starts and
  // when the page changes host, and every write a flow makes — the initial
  // "running", each report, the final result — is dropped unless it still owns
  // the current generation (codex D4b R1-1 / A-1: a flow started on host M
  // whose report lands after the page moved to host A must not lock A's page,
  // and its final write must not paint over A's own flow). flowGen also makes
  // the lock synchronous: a second click in the same tick sees a running flow
  // and is ignored, before any state update has rendered.
  const flowGen = useRef(0)
  const flowRunning = useRef(false)
  const runFlow: RunFlow = useCallback(async (key, fn) => {
    if (flowRunning.current) return
    const my = ++flowGen.current
    const owner = hostId
    const mine = () => flowGen.current === my && alive.current && liveHost.current === owner
    flowRunning.current = true
    setFlow({ key, step: null, running: true, error: '', hint: '' })
    const report: Report = (step) => { if (mine()) setFlow({ key, step, running: true, error: '', hint: '' }) }
    let res: FlowResult
    try {
      res = await fn(report)
    } catch (e) {
      res = { error: errText(e) }
    }
    if (!mine()) return
    flowRunning.current = false
    setFlow({ key: res.key ?? key, step: null, running: false, error: res.error ?? '', hint: res.hint ?? '' })
    await run()
  }, [hostId, run])

  useEffect(() => {
    setSnap(null)
    setFlow(null)
    // A host change orphans any in-flight flow: it loses the generation, so its
    // late writes are dropped, and the lock is released for the new host.
    flowGen.current++
    flowRunning.current = false
    void run()
    // eslint-disable-next-line react-hooks/exhaustive-deps -- gen is a run counter; the latest value is the point
    return () => { gen.current++ }
  }, [run])

  if (!host) return null

  const locked = busy || (flow?.running ?? false)
  // A flow note whose owner is no longer on screen (an unpaired row, a candidate that became a row) lands here.
  // The self-alias flow is always owned by the `peers-self` line (codex F3), never by this fallback.
  const orphanFlow = flow && snap && !snap.error
    && flow.key !== selfKey
    && !snap.rows.some((r) => rowKey(r.entry.alias) === flow.key)
    && !snap.candidates.some((c) => candidateKey(c.hostId) === flow.key)

  return (
    <div className="max-w-3xl">
      <div className="flex items-center justify-between mb-1">
        <h2 className="text-lg font-semibold">{t('hosts.peers')}</h2>
        <button type="button" data-testid="peers-refresh" disabled={locked} onClick={() => void run()}
          className="flex items-center gap-1.5 px-3 py-1.5 rounded text-xs bg-surface-tertiary text-text-secondary hover:text-text-primary cursor-pointer disabled:opacity-50">
          <ArrowsClockwise size={14} className={busy ? 'animate-spin' : ''} />{busy ? t('peers.checking') : t('peers.refresh')}
        </button>
      </div>
      <p className="text-xs text-text-muted mb-1">{t('peers.desc')}</p>
      {/* The selected host's own identity (the third of the three names, spec §2.1) and its
          self-alias editor (#1196). Keyed by host so the editor's state dies with a host change. */}
      {snap?.self && (
        <SelfAliasLine key={hostId} hostId={hostId} hostName={hostName} self={snap.self}
          busy={locked} flow={flow} runFlow={(fn) => runFlow(selfKey, fn)} />
      )}

      {snap?.error && (
        <div data-testid="peers-banner" className="flex items-start justify-between gap-3 px-3 py-2.5 rounded-md mb-4 bg-red-500/10 border border-red-500/20">
          <p className="text-sm text-red-400">{t('peers.banner', { call: snap.error.call, message: snap.error.message })}</p>
          <button type="button" data-testid="peers-retry" disabled={locked} onClick={() => void run()}
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
            <PeerRow key={`${hostId}:${row.entry.alias}`} hostId={hostId} hostName={hostName} xUrl={xUrl} self={snap.self!} row={row}
              busy={locked} flow={flow} runFlow={(fn) => runFlow(rowKey(row.entry.alias), fn)} />
          ))}
        </div>
      )}

      {orphanFlow && <FlowNote flow={flow} flowKey={flow.key} />}

      {snap && !snap.error && snap.self && (
        <PairWithSection hostId={hostId} self={snap.self} xUrl={xUrl} candidates={snap.candidates}
          busy={locked} flow={flow} runFlow={runFlow} />
      )}
    </div>
  )
}

/* ─── One entry ─── */

interface RowProps {
  hostId: string
  hostName: string
  xUrl: string
  self: { host_id: string; self_alias: string }
  row: PairingRow
  busy: boolean
  flow: FlowState | null
  runFlow: BoundRunFlow

}

function PeerRow({ hostId, hostName, xUrl, self, row, busy, flow, runFlow }: RowProps) {
  const t = useI18nStore((s) => s.t)
  const [confirmUnpair, setConfirmUnpair] = useState(false)
  const status = pairStatus(row.outbound, row.inbound)
  const { entry, counterpart, returnEntry } = row

  const outDrift = row.outbound !== 'pending' && row.outbound.ok ? aliasDrift(entry.alias, row.outbound.self_alias) : ''
  const inDrift = returnEntry && typeof row.inbound === 'object' && row.inbound.ok ? aliasDrift(returnEntry.alias, row.inbound.self_alias) : ''

  // Which App host presents on each line, and whether it can be pushed to
  // (spec §7.3: Rotate needs the counterpart to be an available App host).
  const counterpartAvailable = counterpart !== null && row.inbound !== 'not-app-host' && row.inbound !== 'counterpart-unavailable'
  const inboundPush = !counterpartAvailable ? null
    : returnEntry
      ? (token: string) => updatePeerHost(counterpart.hostId, returnEntry.alias, { token }).then(() => undefined)
      : (token: string) => addPeerHost(counterpart.hostId, { alias: self.self_alias, url: xUrl, token }).then(() => undefined)
  const inboundLabel = returnEntry
    ? (typeof row.inbound === 'object' && !row.inbound.ok ? 'retry_return' : 'rotate')
    : 'create_return'

  const unpair = () => {
    setConfirmUnpair(false)
    void runFlow(async (report) => {
      const out = await unpairHosts(
        { hostId, alias: entry.alias },
        counterpart && returnEntry ? { hostId: counterpart.hostId, alias: returnEntry.alias } : null,
        actionApi(), report,
      )
      const errors = [
        out.xError && t('peers.flow_error', { step: t('peers.step.delete-x'), error: out.xError }),
        out.yError && t('peers.flow_error', { step: t('peers.step.delete-y'), error: out.yError }),
      ].filter(Boolean)
      return errors.length ? { error: errors.join(' ') } : {}
    })
  }

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
        <button type="button" data-testid={`peer-unpair-${entry.alias}`} disabled={busy} onClick={() => setConfirmUnpair(true)}
          className="text-xs px-2 py-0.5 rounded bg-surface-tertiary text-text-secondary hover:text-status-error cursor-pointer disabled:opacity-50 disabled:cursor-default">
          {flow?.running && flow.key === rowKey(entry.alias) && (flow.step === 'delete-x' || flow.step === 'delete-y') ? t('peers.unpairing') : t('peers.unpair')}
        </button>
      </div>
      <div className="font-mono text-xs text-text-muted mt-0.5">
        <span data-testid="peer-url">{entry.url}</span>
        {entry.host_id && <> · <span data-testid="peer-host-id">{entry.host_id}</span></>}
      </div>

      <div className="mt-2 space-y-1.5">
        {/* Outbound: X → peer, verify(X, E.alias). Its token lives on the peer's entry for X (returnEntry),
            so the rotation controls exist only when that entry is known. */}
        <DirectionLine testId="peer-outbound" from={hostName} to={entry.alias} side={row.outbound}
          drift={outDrift} renameTarget={{ hostId, alias: entry.alias }} busy={busy} runFlow={runFlow}>
          {counterpart && returnEntry && (
            <RotationControls holder={{ hostId: counterpart.hostId, alias: returnEntry.alias }} row={returnEntry}
              stale={row.gateStale.returnEntry} evidenceDialled
              push={(token) => updatePeerHost(hostId, entry.alias, { token }).then(() => undefined)}
              label="rotate" testId="peer-outbound" busy={busy} runFlow={runFlow} />
          )}
        </DirectionLine>

        {/* Return: peer → X, verify(Y, E'.alias) or one of the three sentences. Its token lives on X's
            own entry; the evidence dial is the peer's, so it counts only when this run made one. */}
        {counterpart && returnEntry ? (
          <DirectionLine testId="peer-inbound" from={entry.alias} to={hostName} side={row.inbound as Side}
            note={t('peers.their_entry', { name: counterpart.name, alias: returnEntry.alias })}
            drift={inDrift} renameTarget={{ hostId: counterpart.hostId, alias: returnEntry.alias }} busy={busy} runFlow={runFlow}>
            <RotationControls holder={{ hostId, alias: entry.alias }} row={entry} stale={row.gateStale.entry}
              evidenceDialled={typeof row.inbound === 'object'} push={inboundPush} label={inboundLabel}
              testId="peer-inbound" busy={busy} runFlow={runFlow} />
          </DirectionLine>
        ) : (
          <div data-testid="peer-inbound" data-ok="none" className="flex items-center gap-2 flex-wrap text-text-muted">
            <Circle size={14} />
            <span className="font-mono text-xs">{t('peers.direction', { from: entry.alias, to: hostName })}</span>
            <span className="text-xs">{returnSentence(t, row, self, counterpart)}</span>
            <RotationControls holder={{ hostId, alias: entry.alias }} row={entry} stale={row.gateStale.entry}
              evidenceDialled={false} push={inboundPush} label={inboundLabel}
              testId="peer-inbound" busy={busy} runFlow={runFlow} />
          </div>
        )}
      </div>

      <FlowNote flow={flow} flowKey={rowKey(entry.alias)} />

      {confirmUnpair && (
        <ConfirmDialog testIdPrefix="peer-unpair" busy={busy}
          title={t('peers.unpair_title', { x: hostName, y: counterpart?.name ?? entry.alias })}
          body={counterpart && returnEntry
            ? t('peers.unpair_body_both', { x: hostName, ex: entry.alias, y: counterpart.name, ey: returnEntry.alias })
            : t('peers.unpair_body_one', { x: hostName, ex: entry.alias, y: counterpart?.name ?? entry.alias })}
          confirmLabel={t('peers.unpair')} onCancel={() => setConfirmUnpair(false)} onConfirm={unpair} />
      )}
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
  /** Rename runs under the page's flow lock like every other write; the runner refreshes afterwards. */
  runFlow: BoundRunFlow
  /** The direction's rotation controls, rendered after the verify reading. */
  children?: ReactNode
}

function DirectionLine({ testId, from, to, side, note, drift, renameTarget, busy, runFlow, children }: LineProps) {
  const t = useI18nStore((s) => s.t)
  const [renaming, setRenaming] = useState(false)
  const [error, setError] = useState<string | null>(null)

  // A stale rename error must not survive past the drift it was about — a
  // Refresh that adopts the alias (or otherwise changes what Rename would
  // do) clears the old 409/400 text along with it.
  useEffect(() => { setError(null) }, [drift])

  const rename = () => {
    setError(null)
    void runFlow(async () => {
      setRenaming(true)
      try {
        await updatePeerHost(renameTarget.hostId, renameTarget.alias, { alias: drift })
      } catch (e) {
        // 400/409 carry the daemon's own sentence; nothing is retried or auto-suffixed (v4 §7.2).
        setError(e instanceof HostApiError ? e.detail : e instanceof Error ? e.message : String(e))
      } finally {
        setRenaming(false)
      }
      return {}
    })
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
      {children}
    </div>
  )
}
