import { useEffect, useState } from 'react'
import {
  X, LinkSimple, ArrowsClockwise, CheckCircle, Warning, ArrowCounterClockwise,
} from '@phosphor-icons/react'
import { useHostStore } from '../../stores/useHostStore'
import { useI18nStore } from '../../stores/useI18nStore'
import { decodePairingCode, cleanPairingInput, generatePurdexToken } from '../../lib/pairing-codec'
import { fetchPairVerify, fetchPairSetup, fetchTokenAuth, PairingError } from '../../lib/host-api'
import { deriveDaemonBase, hostEndpointKey } from '../../lib/host-endpoint'

interface Props {
  onClose: () => void
  initial?: { scheme?: 'http' | 'https'; ip?: string; port?: string; useToken?: boolean }
}

type Stage = 'idle' | 'pairing' | 'paired' | 'manual' | 'saving' | 'done' | 'error'

const portOrDefault = (p: string, s: 'http' | 'https') =>
  parseInt(p, 10) || (s === 'https' ? 443 : 7860)

export function AddHostDialog({ onClose, initial }: Props) {
  const t = useI18nStore((s) => s.t)
  const addHost = useHostStore((s) => s.addHost)

  const [pairingCode, setPairingCode] = useState('')
  const [ip, setIp] = useState(initial?.ip ?? '')
  const [port, setPort] = useState(initial?.port ?? '7860')
  const [scheme, setScheme] = useState<'http' | 'https'>(initial?.scheme ?? 'http')
  const [token, setToken] = useState('')
  const [stage, setStage] = useState<Stage>(initial ? 'manual' : 'idle')
  const [error, setError] = useState('')
  const [useToken, setUseToken] = useState(initial?.useToken ?? false)
  const [setupSecret, setSetupSecret] = useState('')
  const [healthMode, setHealthMode] = useState<'pairing' | 'pending' | 'normal' | null>(null)

  // Debounced health check in manual (token) mode
  useEffect(() => {
    if (!useToken || !ip || stage === 'saving' || stage === 'done') {
      setHealthMode(null)
      return
    }
    const timer = setTimeout(async () => {
      try {
        const res = await fetch(`${deriveDaemonBase({ scheme, ip, port: portOrDefault(port, scheme) })}/api/health`)
        const body = await res.json()
        const mode = body.mode ?? 'normal'
        setHealthMode(mode)
        // Auto-switch to pairing route if daemon is in pairing mode and no token entered yet
        if (mode === 'pairing' && !token) {
          handleToggleToken(false)
        }
      } catch {
        setHealthMode(null)
      }
    }, 300)
    return () => clearTimeout(timer)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [useToken, ip, port, scheme, stage])

  // Escape to close
  useEffect(() => {
    const handleKeyDown = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose()
    }
    document.addEventListener('keydown', handleKeyDown)
    return () => document.removeEventListener('keydown', handleKeyDown)
  }, [onClose])

  const handlePair = async () => {
    setError('')
    const cleaned = cleanPairingInput(pairingCode)
    const decoded = decodePairingCode(cleaned)
    if (!decoded) {
      setStage('error')
      setError(t('hosts.invalid_pairing_code'))
      return
    }

    setStage('pairing')
    const base = `http://${decoded.ip}:${decoded.port}`

    try {
      const res = await fetchPairVerify(base, decoded.secret)
      setSetupSecret(res.setupSecret)
      setIp(decoded.ip)
      setPort(String(decoded.port))
      setToken(generatePurdexToken())
      setStage('paired')
    } catch (err) {
      setStage('idle')
      setPairingCode('')
      if (err instanceof PairingError) {
        setError(`${t('hosts.pairing_failed')}: HTTP ${err.status}`)
      } else {
        setError(err instanceof Error ? err.message : t('hosts.pairing_failed'))
      }
    }
  }

  const handleConfirm = async () => {
    const trimmedIp = ip.trim()
    const trimmedToken = token.trim()
    const portNum = portOrDefault(port.trim(), scheme)
    const mode: 'token' | 'pairing' | 'direct' =
      useToken ? 'token' : stage === 'paired' ? 'pairing' : 'direct'
    setStage('saving')
    setError('')

    const upsertHost = (effScheme: 'http' | 'https', effToken: string | undefined) => {
      const draftKey = hostEndpointKey({ scheme: effScheme, ip: trimmedIp, port: portNum })
      const hosts = useHostStore.getState().hosts
      const existingId = Object.keys(hosts).find((id) => hostEndpointKey(hosts[id]) === draftKey)
      let hostId: string
      if (existingId) {
        useHostStore.getState().updateHost(existingId, { scheme: effScheme, token: effToken || undefined })
        hostId = existingId
      } else {
        hostId = addHost({ name: trimmedIp, ip: trimmedIp, port: portNum, scheme: effScheme, token: effToken || undefined })
      }
      useHostStore.getState().setActiveHost(hostId)
    }

    try {
      if (mode === 'direct') {
        upsertHost(scheme, undefined)
      } else if (mode === 'token') {
        await fetchTokenAuth(deriveDaemonBase({ scheme, ip: trimmedIp, port: portNum }), trimmedToken)
        upsertHost(scheme, trimmedToken)
      } else {
        // pairing — always http (LAN/tailnet)
        await fetchPairSetup(deriveDaemonBase({ scheme: 'http', ip: trimmedIp, port: portNum }), setupSecret, trimmedToken)
        upsertHost('http', trimmedToken)
      }
      setStage('done')
      onClose()
    } catch (err) {
      if (mode === 'token') {
        setStage('manual')
      } else {
        setStage('idle')
        setPairingCode('')
        setSetupSecret('')
      }
      if (err instanceof PairingError) {
        setError(`HTTP ${err.status}`)
      } else {
        setError(err instanceof Error ? err.message : t('hosts.connection_failed'))
      }
    }
  }

  const handleToggleToken = (checked: boolean) => {
    setUseToken(checked)
    if (checked) {
      setStage('manual')
      setPairingCode('')
      setSetupSecret('')
    } else {
      setStage('idle')
    }
  }

  const handleGenerateToken = () => {
    setToken(generatePurdexToken())
  }

  const isPairingRoute = !useToken
  const isSaving = stage === 'saving'
  const pairingDisabled = stage === 'pairing' || stage === 'paired' || isSaving || useToken
  const fieldsEnabled = stage === 'paired' || stage === 'manual'
  const confirmDisabled = stage !== 'paired' && stage !== 'manual'
  const tokenValid = token.length >= 20

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/50" role="dialog" aria-modal="true" aria-labelledby="add-host-dialog-title" onClick={onClose}>
      <div className="bg-surface-primary border border-border-default rounded-lg shadow-xl w-full max-w-md" onClick={(e) => e.stopPropagation()}>
        {/* Header */}
        <div className="flex items-center justify-between px-4 py-3 border-b border-border-subtle">
          <h2 id="add-host-dialog-title" className="text-sm font-semibold">{t('hosts.add_host')}</h2>
          <button onClick={onClose} className="text-text-muted hover:text-text-primary cursor-pointer">
            <X size={16} />
          </button>
        </div>

        {/* Body */}
        <div className="p-4 space-y-3">
          {/* Pairing Code Section */}
          <div>
            <label className="text-xs text-text-secondary block mb-1">{t('hosts.pairing_code')}</label>
            <div className="flex gap-2">
              <input
                value={pairingCode}
                onChange={(e) => setPairingCode(e.target.value)}
                placeholder="XXXX-XXXX-XXXXX"
                disabled={pairingDisabled}
                className="flex-1 bg-surface-secondary border border-border-default rounded px-3 py-2 text-sm text-text-primary font-mono disabled:opacity-50"
              />
              <button
                onClick={handlePair}
                disabled={pairingDisabled || cleanPairingInput(pairingCode).length < 13}
                className="px-4 py-2 rounded text-xs bg-accent text-white cursor-pointer disabled:opacity-50 flex items-center gap-1.5"
              >
                {stage === 'pairing' && <ArrowsClockwise size={14} className="animate-spin" />}
                <LinkSimple size={14} />
                {t('hosts.pair_button')}
              </button>
            </div>
          </div>

          {/* Pairing status */}
          {stage === 'paired' && isPairingRoute && (
            <div className="flex items-center gap-2 text-xs text-green-400">
              <CheckCircle size={14} />
              {t('hosts.pairing_success')}
            </div>
          )}

          {/* Divider */}
          <div className="border-t border-border-subtle" />

          {/* Token checkbox */}
          <label className="flex items-center gap-2 text-xs text-text-secondary cursor-pointer">
            <input
              type="checkbox"
              checked={useToken}
              onChange={(e) => handleToggleToken(e.target.checked)}
              disabled={isSaving}
              className="rounded"
            />
            {t('hosts.use_token')}
          </label>

          {/* Mode badge */}
          {healthMode && useToken && (
            <div className={`flex items-start gap-2 px-2 py-2 rounded text-xs ${
              healthMode === 'pairing' ? 'bg-yellow-500/10 border border-yellow-500/20 text-yellow-400'
                : healthMode === 'pending' ? 'bg-blue-500/10 border border-blue-500/20 text-blue-400'
                : 'bg-green-500/10 border border-green-500/20 text-green-400'
            }`}>
              {healthMode === 'pairing' && t('hosts.mode_pairing_hint')}
              {healthMode === 'pending' && t('hosts.mode_pending_hint')}
              {healthMode === 'normal' && t('hosts.mode_normal_hint')}
            </div>
          )}

          {/* Scheme selector — manual stage only (pairing confirm is always http) */}
          {stage === 'manual' && (
            <div>
              <label htmlFor="host-scheme" className="text-xs text-text-secondary block mb-1">{t('hosts.scheme')}</label>
              <select
                id="host-scheme"
                aria-label={t('hosts.scheme')}
                value={scheme}
                onChange={(e) => setScheme(e.target.value as 'http' | 'https')}
                className="w-full bg-surface-secondary border border-border-default rounded px-3 py-2 text-sm text-text-primary"
              >
                <option value="http">http</option>
                <option value="https">https</option>
              </select>
            </div>
          )}

          {/* Host / Port / Token fields */}
          <div className="grid grid-cols-3 gap-2">
            <div className="col-span-2">
              <label className="text-xs text-text-secondary block mb-1">{t('hosts.ip')}</label>
              <input
                value={ip}
                onChange={(e) => setIp(e.target.value)}
                placeholder="100.64.0.1"
                disabled={!fieldsEnabled}
                className="w-full bg-surface-secondary border border-border-default rounded px-3 py-2 text-sm text-text-primary font-mono disabled:opacity-50"
              />
            </div>
            <div>
              <label className="text-xs text-text-secondary block mb-1">{t('hosts.port')}</label>
              <input
                value={port}
                onChange={(e) => setPort(e.target.value)}
                placeholder="7860"
                disabled={!fieldsEnabled}
                className="w-full bg-surface-secondary border border-border-default rounded px-3 py-2 text-sm text-text-primary font-mono disabled:opacity-50"
              />
            </div>
          </div>

          {!(stage === 'manual' && !useToken) && (
            <div>
              <label className="text-xs text-text-secondary block mb-1">{t('hosts.token')}</label>
              <div className="flex gap-2">
                <input
                  value={token}
                  onChange={(e) => setToken(e.target.value)}
                  placeholder="purdex_..."
                  type="password"
                  disabled={!fieldsEnabled}
                  className="flex-1 bg-surface-secondary border border-border-default rounded px-3 py-2 text-sm text-text-primary font-mono disabled:opacity-50"
                />
                {!useToken && (
                  <button
                    onClick={handleGenerateToken}
                    disabled={!fieldsEnabled}
                    title={t('hosts.token_generate_hint')}
                    className="px-2 py-2 rounded text-xs text-text-muted hover:text-text-primary cursor-pointer disabled:opacity-50 flex items-center gap-1"
                  >
                    <ArrowCounterClockwise size={14} />
                  </button>
                )}
              </div>
              {fieldsEnabled && token && !tokenValid && (
                <p className="text-xs text-yellow-400 mt-1">{t('hosts.token_too_short')}</p>
              )}
            </div>
          )}

          {/* Error feedback */}
          {error && (
            <div className="flex items-center gap-2 text-xs text-red-400">
              <Warning size={14} />
              <span>{error}</span>
              {isPairingRoute && stage !== 'paired' && (
                <span className="text-text-muted ml-1">— {t('hosts.pairing_retry')}</span>
              )}
            </div>
          )}
        </div>

        {/* Footer */}
        <div className="flex items-center justify-end gap-2 px-4 py-3 border-t border-border-subtle">
          <button
            onClick={onClose}
            className="px-4 py-2 rounded text-xs text-text-secondary hover:text-text-primary cursor-pointer"
          >
            {t('common.cancel')}
          </button>
          <button
            onClick={handleConfirm}
            disabled={confirmDisabled || (useToken && !tokenValid)}
            className="px-4 py-2 rounded text-xs bg-accent text-white cursor-pointer disabled:opacity-50 flex items-center gap-1.5"
          >
            {isSaving && <ArrowsClockwise size={14} className="animate-spin" />}
            {t('hosts.confirm')}
          </button>
        </div>
      </div>
    </div>
  )
}
