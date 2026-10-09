// Pair a phone by QR code (QR pairing spec §2 steps 1–4, §6). The dialog is a thin view over a `PairingSession`
// (lib/pairing.ts): it picks the profile and the relay, starts the session, shows its state, and closes it. Closing —
// by any route, or by unmounting — calls `session.close()` once and never waits for it; the session itself decides
// whether the minted tokens are revoked (spec §4.3). The QR carries a credential, so the url is only ever handed to
// <QrCode>, never printed.
import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { ArrowsClockwise, X } from '@phosphor-icons/react'
import { QrCode } from '../QrCode'
import { useHostStore, type HostConfig } from '../../stores/useHostStore'
import { useI18nStore } from '../../stores/useI18nStore'
import { useProfileStore } from '../../stores/useProfileStore'
import { formatTransferCode } from '../../lib/host-transfer-api'
import { useHostLookResolver } from '../../lib/host-look'
import { createPairingSession, type PairingSession, type PairingState } from '../../lib/pairing'
import { listProfiles } from '../../lib/profile/api'

interface Props {
  onClose: () => void
}

interface ProfileChoice {
  id: string
  name: string
}
type ProfileList = { kind: 'loading' } | { kind: 'ok'; profiles: ProfileChoice[] } | { kind: 'failed' }

function hasToken(h: HostConfig): boolean {
  return typeof h.token === 'string' && h.token !== ''
}

/** mm:ss until `deadline`, re-read every second. Mounted only while the code is shown, so `now` starts fresh. */
function Countdown({ deadline }: { deadline: number }) {
  const t = useI18nStore((s) => s.t)
  const [now, setNow] = useState(() => Date.now())
  useEffect(() => {
    const id = setInterval(() => setNow(Date.now()), 1000)
    return () => clearInterval(id)
  }, [])
  const left = Math.max(0, Math.ceil((deadline - now) / 1000))
  return <p data-testid="pair-countdown" className="text-xs text-text-secondary">{t('hosts.pair.countdown', { time: mmss(left) })}</p>
}

function mmss(totalS: number): string {
  const m = Math.floor(totalS / 60)
  const s = totalS % 60
  return `${String(m).padStart(2, '0')}:${String(s).padStart(2, '0')}`
}

export function PairPhoneDialog({ onClose }: Props) {
  const t = useI18nStore((s) => s.t)
  const hosts = useHostStore((s) => s.hosts)
  const hostOrder = useHostStore((s) => s.hostOrder)
  const runtime = useHostStore((s) => s.runtime)
  const activeHostId = useHostStore((s) => s.activeHostId)
  const masterHostId = useProfileStore((s) => s.masterHostId)
  const masterProfileId = useProfileStore((s) => s.masterProfileId)
  const lookOf = useHostLookResolver()

  const list = hostOrder.map((id) => hosts[id]).filter((h): h is HostConfig => h !== undefined)
  const withToken = list.filter(hasToken)
  const tokenless = list.filter((h) => !hasToken(h))
  const connected = list.filter((h) => runtime[h.id]?.status === 'connected')

  // The profile's SOT host: where this Mac's profile lives when it is attached, else the host in use.
  const sotHostId = (masterHostId !== null && hosts[masterHostId] ? masterHostId : null) ?? activeHostId
  const sotName = (sotHostId && lookOf(sotHostId).name) || ''

  const [loaded, setLoaded] = useState<{ hostId: string; list: ProfileList } | null>(null)
  const [profilePick, setProfilePick] = useState<string | null>(null)
  const [relayPick, setRelayPick] = useState<string | null>(null)
  const [state, setState] = useState<PairingState | null>(null)

  useEffect(() => {
    if (!sotHostId) return
    let live = true
    void listProfiles(sotHostId).then((r) => {
      if (!live) return
      const list: ProfileList = r.kind === 'ok' ? { kind: 'ok', profiles: r.value.map((p) => ({ id: p.id, name: p.name })) } : { kind: 'failed' }
      setLoaded({ hostId: sotHostId, list })
    })
    return () => {
      live = false
    }
  }, [sotHostId])
  const profiles: ProfileList = !sotHostId ? { kind: 'failed' } : loaded?.hostId === sotHostId ? loaded.list : { kind: 'loading' }

  const profileList = profiles.kind === 'ok' ? profiles.profiles : []
  const attachedHere = masterHostId === sotHostId ? masterProfileId : null
  const profile =
    profileList.find((p) => p.id === profilePick) ??
    profileList.find((p) => p.id === attachedHere) ??
    profileList[0] ??
    null
  const relay =
    connected.find((h) => h.id === relayPick) ?? connected.find((h) => h.id === activeHostId) ?? connected[0] ?? null

  // ── session lifecycle: close exactly once, never awaited ──
  const sessionRef = useRef<PairingSession | null>(null)
  const closedRef = useRef(false)
  const unsubRef = useRef<(() => void) | null>(null)
  const closeSession = useCallback(() => {
    if (closedRef.current) return
    closedRef.current = true
    unsubRef.current?.()
    const s = sessionRef.current
    if (s) void s.close()
  }, [])
  useEffect(() => closeSession, [closeSession])

  const handleClose = useCallback(() => {
    closeSession()
    onClose()
  }, [closeSession, onClose])
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') handleClose()
    }
    document.addEventListener('keydown', onKey)
    return () => document.removeEventListener('keydown', onKey)
  }, [handleClose])

  const phase = state?.phase ?? 'idle'
  const minting = phase === 'minting' || (state !== null && phase === 'idle')
  const setupVisible = phase === 'idle' || phase === 'minting'
  const blocked = !profile || !relay || minting || profiles.kind !== 'ok' || withToken.length === 0

  const handleCreate = () => {
    if (blocked || !profile || !relay || !sotHostId || sessionRef.current) return
    const session = createPairingSession({
      profile: { sotHostId, profileId: profile.id, profileName: profile.name },
      relay,
      hosts: withToken,
      label: t('hosts.pair.phone_label_default'),
    })
    sessionRef.current = session
    setState(session.getState())
    unsubRef.current = session.subscribe(setState)
    void session.start()
  }

  const result = state?.result
  const leftOutNow = useMemo(() => (phase === 'ready' || phase === 'claimed' ? (state?.leftOut ?? []) : []), [phase, state?.leftOut])
  const nameOfHost = (id: string) => (hosts[id] ? (lookOf(id).name ?? id) : id)

  const profileNote =
    profiles.kind === 'loading'
      ? t('hosts.pair.profile_loading')
      : profiles.kind === 'failed'
        ? t('hosts.pair.profile_failed', { host: sotName })
        : profileList.length === 0
          ? t('hosts.pair.profile_none', { host: sotName })
          : null

  const endedText =
    phase === 'expired'
      ? t('hosts.pair.ended.expired')
      : phase === 'closed'
        ? t('hosts.pair.ended.closed')
        : phase === 'gone'
          ? t(state?.unknownOutcome ? 'hosts.pair.ended.gone_unknown' : 'hosts.pair.ended.gone')
          : null

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/50" role="dialog" aria-modal="true" aria-labelledby="pair-phone-title" onClick={handleClose}>
      <div className="bg-surface-primary border border-border-default rounded-lg shadow-xl w-full max-w-md" onClick={(e) => e.stopPropagation()}>
        <div className="flex items-center justify-between px-4 py-3 border-b border-border-subtle">
          <h2 id="pair-phone-title" className="text-sm font-semibold">{t('hosts.pair.title')}</h2>
          <button onClick={handleClose} aria-label={t('common.close')} className="text-text-muted hover:text-text-primary cursor-pointer">
            <X size={16} />
          </button>
        </div>

        <div className="p-4 space-y-3">
          {setupVisible && (
            <>
              <div>
                <label htmlFor="pair-phone-profile" className="text-xs text-text-secondary block mb-1">{t('hosts.pair.profile_label')}</label>
                <select
                  id="pair-phone-profile"
                  value={profile?.id ?? ''}
                  disabled={minting || profileList.length === 0}
                  onChange={(e) => setProfilePick(e.target.value)}
                  className="w-full bg-surface-secondary border border-border-default rounded px-3 py-2 text-sm text-text-primary"
                >
                  {profileList.map((p) => (
                    <option key={p.id} value={p.id}>{p.name}</option>
                  ))}
                </select>
                {profileNote !== null && <p className="text-xs text-text-muted mt-1">{profileNote}</p>}
              </div>

              <div>
                <label htmlFor="pair-phone-relay" className="text-xs text-text-secondary block mb-1">{t('hosts.pair.relay_label')}</label>
                {connected.length === 0 ? (
                  <p className="text-xs text-text-muted">{t('hosts.pair.no_relay')}</p>
                ) : (
                  <select
                    id="pair-phone-relay"
                    value={relay?.id ?? ''}
                    disabled={minting}
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
                <span className="text-xs text-text-secondary block mb-1">{t('hosts.pair.included_label')}</span>
                {withToken.length === 0 ? (
                  <p className="text-xs text-text-muted">{t('hosts.pair.included_none')}</p>
                ) : (
                  <ul data-testid="pair-included" className="space-y-0.5 text-sm text-text-primary">
                    {withToken.map((h) => (
                      <li key={h.id} className="truncate">{lookOf(h.id).name}</li>
                    ))}
                  </ul>
                )}
                {tokenless.length > 0 && (
                  <div data-testid="pair-leftout" className="mt-2">
                    <span className="text-xs text-text-secondary block">{t('hosts.pair.leftout_label')}</span>
                    <ul className="space-y-0.5 text-xs text-text-muted">
                      {tokenless.map((h) => (
                        <li key={h.id}>{t('hosts.pair.leftout_item', { host: lookOf(h.id).name ?? h.id, reason: t('hosts.pair.leftout.no_token') })}</li>
                      ))}
                    </ul>
                  </div>
                )}
              </div>

              <p data-testid="pair-trust" className="text-xs text-yellow-400">{t('hosts.pair.trust')}</p>
            </>
          )}

          {phase === 'ready' && result && (
            <div className="space-y-3 flex flex-col items-center">
              <div className="bg-white p-2 rounded">
                <QrCode value={result.qrUrl} size={224} label={t('hosts.pair.qr_label')} />
              </div>
              <p className="text-xs text-text-secondary text-center">{t('hosts.pair.scan')}</p>
              <div className="text-center">
                <span className="text-xs text-text-secondary block">{t('hosts.pair.code_label')}</span>
                <span data-testid="pair-code" className="font-mono text-lg tracking-widest text-text-primary">{formatTransferCode(result.code)}</span>
              </div>
              <Countdown deadline={result.deadline} />
              <p data-testid="pair-trust" className="text-xs text-yellow-400">{t('hosts.pair.trust')}</p>
            </div>
          )}

          {phase === 'claimed' && (
            <div className="py-6 text-center space-y-1">
              <p className="text-2xl font-semibold text-text-primary">{t('hosts.pair.paired')}</p>
              <p className="text-xs text-text-secondary">{t('hosts.pair.paired_hint')}</p>
            </div>
          )}

          {endedText !== null && <p data-testid="pair-ended" className="text-sm text-text-primary">{endedText}</p>}

          {phase === 'failed' && state?.failure && (
            <p role="alert" className="text-xs text-red-400">{t(`hosts.pair.error.${state.failure.reason}`)}</p>
          )}

          {leftOutNow.length > 0 && (
            <div data-testid="pair-leftout-ready">
              <span className="text-xs text-text-secondary block">{t('hosts.pair.leftout_label')}</span>
              <ul className="space-y-0.5 text-xs text-text-muted">
                {leftOutNow.map((l) => (
                  <li key={l.hostId}>{t('hosts.pair.leftout_item', { host: nameOfHost(l.hostId), reason: t(`hosts.pair.leftout.${l.reason}`) })}</li>
                ))}
              </ul>
            </div>
          )}

          {(state?.revokeFailed.length ?? 0) > 0 && (
            <p data-testid="pair-revoke-failed" className="text-xs text-yellow-400">{t('hosts.pair.revoke_failed')}</p>
          )}
        </div>

        <div className="flex justify-end gap-2 px-4 py-3 border-t border-border-subtle">
          {setupVisible ? (
            <>
              <button onClick={handleClose} className="px-4 py-2 rounded text-xs text-text-secondary hover:text-text-primary cursor-pointer">
                {t('common.cancel')}
              </button>
              <button
                onClick={handleCreate}
                disabled={blocked}
                className="px-4 py-2 rounded text-xs bg-accent text-white cursor-pointer disabled:opacity-50 flex items-center gap-1.5"
              >
                {minting && <ArrowsClockwise size={14} className="animate-spin" />}
                {t('hosts.pair.create')}
              </button>
            </>
          ) : (
            <button onClick={handleClose} className="px-4 py-2 rounded text-xs bg-accent text-white cursor-pointer">
              {t('common.close')}
            </button>
          )}
        </div>
      </div>
    </div>
  )
}
