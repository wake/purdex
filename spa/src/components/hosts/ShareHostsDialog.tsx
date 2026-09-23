// Share hosts through a transfer code (host ownership spec §6.1 / §6.2; H4b). The trust sentence is on screen the
// whole time before a code exists: the relay holds the shared tokens in plain text until the code is used or expires.
import { useEffect, useRef, useState } from 'react'
import { ArrowsClockwise, Copy, X } from '@phosphor-icons/react'
import { useHostStore, type HostConfig } from '../../stores/useHostStore'
import { useI18nStore } from '../../stores/useI18nStore'
import {
  MAX_TRANSFER_BODY_BYTES,
  MAX_TRANSFER_ROWS,
  createTransfer,
  formatTransferCode,
  transferBodyBytes,
  transferFailureText,
  type TransferFailure,
} from '../../lib/host-transfer-api'
import { payloadRowsOf } from '../../lib/host-transfer-plan'

interface Props {
  onClose: () => void
}

type Phase =
  | { kind: 'idle' }
  | { kind: 'creating' }
  | { kind: 'created'; code: string; expiresAt: number; relayName: string }
  | { kind: 'failed'; failure: TransferFailure; relayName: string }

function hasToken(h: HostConfig): boolean {
  return typeof h.token === 'string' && h.token !== ''
}

export function ShareHostsDialog({ onClose }: Props) {
  const t = useI18nStore((s) => s.t)
  const hosts = useHostStore((s) => s.hosts)
  const hostOrder = useHostStore((s) => s.hostOrder)
  const runtime = useHostStore((s) => s.runtime)
  const activeHostId = useHostStore((s) => s.activeHostId)

  const list = hostOrder.map((id) => hosts[id]).filter((h): h is HostConfig => h !== undefined)
  const connected = list.filter((h) => runtime[h.id]?.status === 'connected')
  // Everything shareable starts ticked — but only up to the relay's row limit: past it the create could only fail.
  const [unticked, setUnticked] = useState<ReadonlySet<string>>(
    () => new Set(list.filter(hasToken).slice(MAX_TRANSFER_ROWS).map((h) => h.id)),
  )
  const [relayPick, setRelayPick] = useState<string | null>(null)
  const [phase, setPhase] = useState<Phase>({ kind: 'idle' })
  const mounted = useRef(true)
  useEffect(() => {
    mounted.current = true
    return () => {
      mounted.current = false
    }
  }, [])

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose()
    }
    document.addEventListener('keydown', onKey)
    return () => document.removeEventListener('keydown', onKey)
  }, [onClose])

  const relay =
    connected.find((h) => h.id === relayPick) ??
    connected.find((h) => h.id === activeHostId) ??
    connected[0] ??
    null
  const relayName = relay?.name ?? t('hosts.transfer.relay_fallback')
  const picked = list.filter((h) => hasToken(h) && !unticked.has(h.id))
  const creating = phase.kind === 'creating'
  const rows = payloadRowsOf(picked)
  const overRows = rows.length > MAX_TRANSFER_ROWS
  const overBytes = !overRows && transferBodyBytes(rows) > MAX_TRANSFER_BODY_BYTES
  const blocked = !relay || rows.length === 0 || overRows || overBytes || creating
  const limitNote = overRows
    ? t('hosts.transfer.limit_rows_over', { max: MAX_TRANSFER_ROWS, count: rows.length - MAX_TRANSFER_ROWS })
    : overBytes
      ? t('hosts.transfer.limit_bytes')
      : list.filter(hasToken).length > MAX_TRANSFER_ROWS
        ? t('hosts.transfer.limit_rows', { max: MAX_TRANSFER_ROWS })
        : null

  const toggle = (id: string) =>
    setUnticked((prev) => {
      const next = new Set(prev)
      if (next.has(id)) next.delete(id)
      else next.add(id)
      return next
    })

  const handleCreate = async () => {
    if (blocked || !relay) return
    const name = relay.name
    setPhase({ kind: 'creating' })
    const res = await createTransfer(relay.id, rows)
    if (!mounted.current) return
    setPhase(res.kind === 'ok' ? { kind: 'created', code: res.code, expiresAt: res.expiresAt, relayName: name } : { kind: 'failed', failure: res, relayName: name })
  }

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/50" role="dialog" aria-modal="true" aria-labelledby="share-hosts-title" onClick={onClose}>
      <div className="bg-surface-primary border border-border-default rounded-lg shadow-xl w-full max-w-md" onClick={(e) => e.stopPropagation()}>
        <div className="flex items-center justify-between px-4 py-3 border-b border-border-subtle">
          <h2 id="share-hosts-title" className="text-sm font-semibold">{t('hosts.transfer.share_title')}</h2>
          <button onClick={onClose} aria-label={t('common.close')} className="text-text-muted hover:text-text-primary cursor-pointer">
            <X size={16} />
          </button>
        </div>

        <div className="p-4 space-y-3">
          {phase.kind === 'created' ? (
            <div className="space-y-2">
              <label className="text-xs text-text-secondary block">{t('hosts.transfer.code_label')}</label>
              <div className="flex items-center gap-2">
                <span data-testid="transfer-code" className="flex-1 font-mono text-lg tracking-widest text-text-primary">
                  {formatTransferCode(phase.code)}
                </span>
                <button
                  onClick={() => void navigator.clipboard?.writeText(formatTransferCode(phase.code)).catch(() => {})}
                  className="px-2 py-1 rounded text-xs text-text-secondary hover:text-text-primary cursor-pointer flex items-center gap-1"
                >
                  <Copy size={14} />
                  {t('hosts.transfer.copy')}
                </button>
              </div>
              <p data-testid="transfer-code-meta" className="text-xs text-text-secondary">
                {t('hosts.transfer.code_meta', {
                  time: new Date(phase.expiresAt).toLocaleTimeString(),
                  relay: phase.relayName,
                })}
              </p>
            </div>
          ) : (
            <>
              <div>
                <span className="text-xs text-text-secondary block mb-1">{t('hosts.transfer.hosts_label')}</span>
                <ul className="space-y-1 max-h-48 overflow-y-auto">
                  {list.map((h) => {
                    const usable = hasToken(h)
                    return (
                      <li key={h.id}>
                        <label className={`flex items-center gap-2 text-sm ${usable ? 'text-text-primary' : 'text-text-muted'}`}>
                          <input
                            type="checkbox"
                            checked={usable && !unticked.has(h.id)}
                            disabled={!usable || creating}
                            onChange={() => toggle(h.id)}
                          />
                          <span className="truncate">{h.name}</span>
                          <span className="text-xs text-text-muted font-mono">{h.ip}:{h.port}</span>
                          {!usable && <span className="text-xs text-text-muted">{t('hosts.transfer.no_token')}</span>}
                        </label>
                      </li>
                    )
                  })}
                </ul>
              </div>

              <div>
                <label htmlFor="share-hosts-relay" className="text-xs text-text-secondary block mb-1">{t('hosts.transfer.relay_label')}</label>
                {connected.length === 0 ? (
                  <p className="text-xs text-text-muted">{t('hosts.transfer.no_relay')}</p>
                ) : (
                  <select
                    id="share-hosts-relay"
                    value={relay?.id ?? ''}
                    disabled={creating}
                    onChange={(e) => setRelayPick(e.target.value)}
                    className="w-full bg-surface-secondary border border-border-default rounded px-3 py-2 text-sm text-text-primary"
                  >
                    {connected.map((h) => (
                      <option key={h.id} value={h.id}>{h.name}</option>
                    ))}
                  </select>
                )}
              </div>

              {limitNote !== null && (
                <p data-testid="transfer-limit" className={`text-xs ${overRows || overBytes ? 'text-red-400' : 'text-text-secondary'}`}>
                  {limitNote}
                </p>
              )}

              <p data-testid="transfer-trust" className="text-xs text-yellow-400">
                {t('hosts.transfer.trust', { relay: relayName })}
              </p>

              {phase.kind === 'failed' && (
                <p role="alert" className="text-xs text-red-400">{transferFailureText(t, phase.failure, phase.relayName)}</p>
              )}
            </>
          )}
        </div>

        <div className="flex justify-end gap-2 px-4 py-3 border-t border-border-subtle">
          {phase.kind === 'created' ? (
            <button onClick={onClose} className="px-4 py-2 rounded text-xs bg-accent text-white cursor-pointer">
              {t('common.close')}
            </button>
          ) : (
            <>
              <button onClick={onClose} className="px-4 py-2 rounded text-xs text-text-secondary hover:text-text-primary cursor-pointer">
                {t('common.cancel')}
              </button>
              <button
                onClick={() => void handleCreate()}
                disabled={blocked}
                className="px-4 py-2 rounded text-xs bg-accent text-white cursor-pointer disabled:opacity-50 flex items-center gap-1.5"
              >
                {creating && <ArrowsClockwise size={14} className="animate-spin" />}
                {t('hosts.transfer.create')}
              </button>
            </>
          )}
        </div>
      </div>
    </div>
  )
}
