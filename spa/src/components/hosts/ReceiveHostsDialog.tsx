// Receive hosts through a transfer code (host ownership spec §6.3 / §6.4; H4b): redeem on a relay, verify EVERY row
// at its own endpoint with its own (payload) token, preview a status per row, then ONE `applyHostTransfer`.
// Nothing is written before the confirm. Replace-all is shown disabled (plan R1, issue #1395).
import { useEffect, useRef, useState } from 'react'
import { ArrowsClockwise, X } from '@phosphor-icons/react'
import { useHostStore, type HostConfig } from '../../stores/useHostStore'
import { useI18nStore } from '../../stores/useI18nStore'
import { fetchInfoAt } from '../../lib/host-api'
import { hostLabel, useHostLookResolver } from '../../lib/host-look'
import { redeemTransfer, transferFailureText, type TransferFailure, type TransferRow } from '../../lib/host-transfer-api'
import {
  commitSet,
  isPickable,
  parseTransferRows,
  planReceive,
  type Observation,
  type ReceiveMode,
  type ReceiveStatus,
} from '../../lib/host-transfer-plan'

interface Props {
  onClose: () => void
}

type Phase =
  | { kind: 'input' }
  | { kind: 'redeeming' }
  | { kind: 'failed'; failure: TransferFailure; relayName: string }
  | { kind: 'review' }
  | { kind: 'done'; added: number; updated: number }

const STATUS_KEY: Record<ReceiveStatus, string> = {
  new: 'hosts.transfer.status.new',
  existing: 'hosts.transfer.status.existing',
  mismatch: 'hosts.transfer.status.mismatch',
  unverified: 'hosts.transfer.status.unverified',
  duplicate: 'hosts.transfer.status.duplicate',
  'local-conflict': 'hosts.transfer.status.local_conflict',
}

export function ReceiveHostsDialog({ onClose }: Props) {
  const t = useI18nStore((s) => s.t)
  const hosts = useHostStore((s) => s.hosts)
  const hostOrder = useHostStore((s) => s.hostOrder)
  const runtime = useHostStore((s) => s.runtime)
  const activeHostId = useHostStore((s) => s.activeHostId)
  const lookOf = useHostLookResolver()

  const connected = hostOrder
    .map((id) => hosts[id])
    .filter((h): h is HostConfig => h !== undefined && runtime[h.id]?.status === 'connected')
  const [relayPick, setRelayPick] = useState<string | null>(null)
  const [code, setCode] = useState('')
  const [phase, setPhase] = useState<Phase>({ kind: 'input' })
  const [rows, setRows] = useState<TransferRow[]>([])
  const [dropped, setDropped] = useState(0)
  // null = verifying
  const [observations, setObservations] = useState<(Observation | null)[]>([])
  const [mode, setMode] = useState<ReceiveMode>('add-only')
  const [unticked, setUnticked] = useState<ReadonlySet<number>>(new Set())
  const [stale, setStale] = useState(false)
  // Every request of this dialog ends with it (created per mount, so StrictMode's re-mount gets a live one).
  const probes = useRef<AbortController>(new AbortController())

  useEffect(() => {
    const ctl = new AbortController()
    probes.current = ctl
    return () => ctl.abort()
  }, [])

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose()
    }
    document.addEventListener('keydown', onKey)
    return () => document.removeEventListener('keydown', onKey)
  }, [onClose])

  const relay =
    connected.find((h) => h.id === relayPick) ?? connected.find((h) => h.id === activeHostId) ?? connected[0] ?? null
  const busy = phase.kind === 'redeeming'

  // The payload's own token, never a local one: the receiver checks what it was given (spec §6.4.2).
  const verify = (row: TransferRow, index: number) => {
    setObservations((prev) => prev.map((o, i) => (i === index ? null : o)))
    const signal = probes.current.signal
    fetchInfoAt(`http://${row.ip}:${row.port}`, row.token, signal)
      .then((info): Observation => ({ kind: 'ok', hostId: typeof info?.host_id === 'string' ? info.host_id : '' }))
      .catch((): Observation => ({ kind: 'failed' }))
      .then((obs) => {
        if (signal.aborted) return
        setObservations((prev) => prev.map((o, i) => (i === index ? obs : o)))
      })
  }

  const handleRedeem = async () => {
    if (!relay || code.trim() === '' || busy) return
    const relayName = hostLabel(relay.id, lookOf(relay.id))
    setPhase({ kind: 'redeeming' })
    const signal = probes.current.signal
    const res = await redeemTransfer(relay.id, code)
    if (signal.aborted) return
    if (res.kind !== 'ok') {
      setPhase({ kind: 'failed', failure: res, relayName })
      return
    }
    const parsed = parseTransferRows(res.hosts)
    setRows(parsed.rows)
    setDropped(parsed.dropped)
    setObservations(parsed.rows.map(() => null))
    setPhase({ kind: 'review' })
    parsed.rows.forEach((row, i) => verify(row, i))
  }

  const settled = observations.every((o) => o !== null)
  const preview = phase.kind === 'review' && settled ? planReceive(rows, observations as Observation[], hosts) : null
  const picks = new Set((preview ?? []).filter((p) => isPickable(p.status, mode) && !unticked.has(p.index)).map((p) => p.index))
  const change = preview ? commitSet(preview, picks, mode) : null
  const canConfirm = change !== null && change.create.length + change.overwrite.length > 0

  const toggle = (index: number) =>
    setUnticked((prev) => {
      const next = new Set(prev)
      if (next.has(index)) next.delete(index)
      else next.add(index)
      return next
    })

  const handleConfirm = () => {
    if (!change || !canConfirm) return
    const res = useHostStore.getState().applyHostTransfer(change)
    if (res.kind === 'stale') {
      setStale(true)
      return
    }
    setPhase({ kind: 'done', added: res.created.length, updated: res.overwritten.length })
  }

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/50" role="dialog" aria-modal="true" aria-labelledby="receive-hosts-title" onClick={onClose}>
      <div className="bg-surface-primary border border-border-default rounded-lg shadow-xl w-full max-w-lg" onClick={(e) => e.stopPropagation()}>
        <div className="flex items-center justify-between px-4 py-3 border-b border-border-subtle">
          <h2 id="receive-hosts-title" className="text-sm font-semibold">{t('hosts.transfer.receive_title')}</h2>
          <button onClick={onClose} aria-label={t('common.close')} className="text-text-muted hover:text-text-primary cursor-pointer">
            <X size={16} />
          </button>
        </div>

        <div className="p-4 space-y-3">
          {phase.kind === 'done' ? (
            <p className="text-sm text-green-400">{t('hosts.transfer.done', { added: phase.added, updated: phase.updated })}</p>
          ) : phase.kind === 'review' ? (
            <>
              {dropped > 0 && <p className="text-xs text-yellow-400">{t('hosts.transfer.dropped', { count: dropped })}</p>}
              {!settled && (
                <p className="text-xs text-text-secondary flex items-center gap-1.5">
                  <ArrowsClockwise size={12} className="animate-spin" />
                  {t('hosts.transfer.verifying')}
                </p>
              )}
              {rows.length === 0 && <p className="text-xs text-text-muted">{t('hosts.transfer.empty')}</p>}
              <ul className="space-y-1 max-h-56 overflow-y-auto">
                {rows.map((row, index) => {
                  const p = preview?.[index]
                  const pickable = p !== undefined && isPickable(p.status, mode)
                  return (
                    <li key={index} data-testid={`transfer-row-${row.name}`} className="flex items-center gap-2 text-sm">
                      <input
                        type="checkbox"
                        aria-label={row.name}
                        checked={pickable && !unticked.has(index)}
                        disabled={!pickable}
                        onChange={() => toggle(index)}
                      />
                      <span className="truncate text-text-primary">{row.name}</span>
                      <span className="text-xs text-text-muted font-mono">{row.ip}:{row.port}</span>
                      <span className="ml-auto text-xs text-text-secondary">
                        {p ? t(STATUS_KEY[p.status]) : observations[index] === null ? t('hosts.transfer.status.checking') : ''}
                      </span>
                      {p?.status === 'unverified' && (
                        <button onClick={() => verify(row, index)} className="text-xs text-accent cursor-pointer">
                          {t('hosts.transfer.retry')}
                        </button>
                      )}
                    </li>
                  )
                })}
              </ul>

              <fieldset className="space-y-1">
                <legend className="text-xs text-text-secondary mb-1">{t('hosts.transfer.mode_label')}</legend>
                <label className="flex items-center gap-2 text-sm">
                  <input type="radio" name="transfer-mode" checked={mode === 'add-only'} onChange={() => setMode('add-only')} />
                  {t('hosts.transfer.mode.add_only')}
                </label>
                <label className="flex items-center gap-2 text-sm">
                  <input type="radio" name="transfer-mode" checked={mode === 'overwrite'} onChange={() => setMode('overwrite')} />
                  {t('hosts.transfer.mode.overwrite')}
                </label>
                <label className="flex items-center gap-2 text-sm text-text-muted">
                  <input type="radio" name="transfer-mode" disabled checked={false} readOnly />
                  {t('hosts.transfer.mode.replace_all')}
                  <span className="text-xs">({t('hosts.transfer.mode.replace_all_disabled')})</span>
                </label>
              </fieldset>

              {stale && <p role="alert" className="text-xs text-red-400">{t('hosts.transfer.stale')}</p>}
            </>
          ) : (
            <>
              <div>
                <label htmlFor="receive-hosts-relay" className="text-xs text-text-secondary block mb-1">{t('hosts.transfer.relay_label')}</label>
                {connected.length === 0 ? (
                  <p className="text-xs text-text-muted">{t('hosts.transfer.no_relay')}</p>
                ) : (
                  <select
                    id="receive-hosts-relay"
                    value={relay?.id ?? ''}
                    disabled={busy}
                    onChange={(e) => setRelayPick(e.target.value)}
                    className="w-full bg-surface-secondary border border-border-default rounded px-3 py-2 text-sm text-text-primary"
                  >
                    {connected.map((h) => (
                      <option key={h.id} value={h.id}>{lookOf(h.id).name}</option>
                    ))}
                  </select>
                )}
              </div>
              <div>
                <label htmlFor="receive-hosts-code" className="text-xs text-text-secondary block mb-1">{t('hosts.transfer.code_label')}</label>
                <input
                  id="receive-hosts-code"
                  value={code}
                  onChange={(e) => setCode(e.target.value)}
                  placeholder="ABCD-2345"
                  disabled={busy}
                  autoComplete="off"
                  className="w-full bg-surface-secondary border border-border-default rounded px-3 py-2 text-sm text-text-primary font-mono"
                />
              </div>
              {phase.kind === 'failed' && (
                <p role="alert" className="text-xs text-red-400">{transferFailureText(t, phase.failure, phase.relayName)}</p>
              )}
            </>
          )}
        </div>

        <div className="flex justify-end gap-2 px-4 py-3 border-t border-border-subtle">
          {phase.kind === 'done' ? (
            <button onClick={onClose} className="px-4 py-2 rounded text-xs bg-accent text-white cursor-pointer">
              {t('common.close')}
            </button>
          ) : (
            <>
              <button onClick={onClose} className="px-4 py-2 rounded text-xs text-text-secondary hover:text-text-primary cursor-pointer">
                {t('common.cancel')}
              </button>
              {phase.kind === 'review' ? (
                <button
                  onClick={handleConfirm}
                  disabled={!canConfirm}
                  className="px-4 py-2 rounded text-xs bg-accent text-white cursor-pointer disabled:opacity-50"
                >
                  {t('hosts.transfer.confirm')}
                </button>
              ) : (
                <button
                  onClick={() => void handleRedeem()}
                  disabled={!relay || code.trim() === '' || busy}
                  className="px-4 py-2 rounded text-xs bg-accent text-white cursor-pointer disabled:opacity-50 flex items-center gap-1.5"
                >
                  {busy && <ArrowsClockwise size={14} className="animate-spin" />}
                  {t('hosts.transfer.redeem')}
                </button>
              )}
            </>
          )}
        </div>
      </div>
    </div>
  )
}
