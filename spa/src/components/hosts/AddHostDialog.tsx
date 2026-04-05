import { useEffect, useState } from 'react'
import {
  X, LinkSimple, ArrowsClockwise, CheckCircle, Warning, ArrowCounterClockwise,
} from '@phosphor-icons/react'
import { useHostStore } from '../../stores/useHostStore'
import { useI18nStore } from '../../stores/useI18nStore'
import { decodePairingCode, cleanPairingInput, generatePurdexToken } from '../../lib/pairing-codec'
import { fetchPairVerify, fetchPairSetup, fetchTokenAuth, PairingError } from '../../lib/host-api'

interface Props {
  onClose: () => void
}

type Stage = 'idle' | 'pairing' | 'paired' | 'manual' | 'saving' | 'done' | 'error'

export function AddHostDialog({ onClose }: Props) {
  const t = useI18nStore((s) => s.t)
  const addHost = useHostStore((s) => s.addHost)

  const [pairingCode, setPairingCode] = useState('')
  const [ip, setIp] = useState('')
  const [port, setPort] = useState('7860')
  const [token, setToken] = useState('')
  const [stage, setStage] = useState<Stage>('idle')
  const [error, setError] = useState('')
  const [useToken, setUseToken] = useState(false)
  const [setupSecret, setSetupSecret] = useState('')

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
    setStage('saving')
    setError('')

    try {
      if (useToken) {
        const base = `http://${ip}:${port || '7860'}`
        await fetchTokenAuth(base, token)
      } else {
        const base = `http://${ip}:${port || '7860'}`
        await fetchPairSetup(base, setupSecret, token)
      }

      // Check for existing host with same IP:port
      const portNum = parseInt(port, 10) || 7860
      const existingHosts = useHostStore.getState().hosts
      const existingId = Object.keys(existingHosts).find((id) => {
        const h = existingHosts[id]
        return h.ip === ip && h.port === portNum
      })

      if (existingId) {
        // Update existing host's token instead of creating a duplicate
        useHostStore.getState().updateHost(existingId, { token: token || undefined })
      } else {
        addHost({
          name: ip,
          ip,
          port: portNum,
          token: token || undefined,
        })
      }
      setStage('done')
      onClose()
    } catch (err) {
      if (useToken) {
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
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/50" role="dialog" aria-modal="true" onClick={onClose}>
      <div className="bg-surface-primary border border-border-default rounded-lg shadow-xl w-full max-w-md" onClick={(e) => e.stopPropagation()}>
        {/* Header */}
        <div className="flex items-center justify-between px-4 py-3 border-b border-border-subtle">
          <h2 className="text-sm font-semibold">{t('hosts.add_host')}</h2>
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
            disabled={confirmDisabled || !ip || !tokenValid || isSaving}
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
