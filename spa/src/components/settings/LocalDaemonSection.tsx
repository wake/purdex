import { useCallback, useEffect, useRef, useState } from 'react'
import { Copy, Eye, EyeSlash } from '@phosphor-icons/react'
import { useI18nStore } from '../../stores/useI18nStore'
import { findHostByEndpoint, useHostStore } from '../../stores/useHostStore'
import { hostLabel, useHostLook } from '../../lib/host-look'
import { copyText } from '../../lib/copy-text'

interface Props {
  daemonBase: string | null
  token?: string
  latestHash: string | null
  /** The parent's latest daemonCheck object; a new reference re-queries status. */
  refreshKey: unknown
}

type Busy = null | 'install' | 'start' | 'restart' | 'path-link' | 'path-add-to-shell'

const btnSecondary = 'px-3 py-1.5 text-xs rounded-md bg-surface-input border border-border-default text-text-primary hover:bg-surface-hover disabled:opacity-50 cursor-pointer disabled:cursor-default'
const btnPrimary = 'px-3 py-1.5 text-xs rounded-md bg-accent text-text-inverse hover:bg-accent-hover disabled:opacity-50 cursor-pointer disabled:cursor-default'

// Settings → Development → "Local daemon": install / update / start /
// restart the daemon on the machine the app runs on (spec 2026-09-14 §3.4).
export function LocalDaemonSection({ daemonBase, token, latestHash, refreshKey }: Props) {
  const t = useI18nStore((s) => s.t)
  const registerLocalHost = useHostStore((s) => s.registerLocalHost)
  const api = window.electronAPI
  const [status, setStatus] = useState<ElectronLocalDaemonStatus | null>(null)
  const [busy, setBusy] = useState<Busy>(null)
  const [step, setStep] = useState<string | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [notice, setNotice] = useState<string | null>(null)
  // The command's own stdout/stderr, shown verbatim: a refusal's whole value
  // is the path it names, so it must be readable, not reduced to "failed".
  const [cliOutput, setCliOutput] = useState<string | null>(null)
  const hosts = useHostStore((s) => s.hosts)
  const [revealed, setRevealed] = useState(false)
  const [copied, setCopied] = useState(false)
  const [copyError, setCopyError] = useState<string | null>(null)
  const copiedTimer = useRef<ReturnType<typeof setTimeout> | null>(null)
  const cfg = status?.config ?? null
  const cfgUrl = cfg ? `http://${cfg.bind}:${cfg.port}` : null
  // Spec §3.3: exact-endpoint membership only, via the same helper registerLocalHost uses.
  const registeredAs = cfg ? findHostByEndpoint(hosts, cfg.bind, cfg.port) : undefined
  const registeredLook = useHostLook(registeredAs?.id ?? null)

  useEffect(() => () => { if (copiedTimer.current) clearTimeout(copiedTimer.current) }, [])

  const copyToken = useCallback(async () => {
    if (!cfg?.token) return
    setCopied(false)
    setCopyError(null)
    try {
      await copyText(cfg.token)
    } catch {
      setCopyError(t('settings.dev.local.copy_failed'))
      return
    }
    setCopied(true)
    if (copiedTimer.current) clearTimeout(copiedTimer.current)
    copiedTimer.current = setTimeout(() => { setCopied(false); copiedTimer.current = null }, 1500)
  }, [cfg, t])

  const addToHosts = useCallback(() => {
    if (!status || !cfg?.token || !cfgUrl) return
    registerLocalHost({ url: cfgUrl, token: cfg.token, hostname: status.hostname })
  }, [status, cfg, cfgUrl, registerLocalHost])

  const refresh = useCallback(async () => {
    if (!api?.localDaemonStatus) return
    try {
      setStatus(await api.localDaemonStatus())
      setRevealed(false)
      setCopied(false)
      setCopyError(null)
    } catch (err) {
      setError(String(err))
    }
  }, [api])

  useEffect(() => { void refresh() }, [refresh, refreshKey])
  useEffect(() => api?.onLocalDaemonProgress?.((s) => setStep(s)), [api])

  const run = useCallback(async (kind: Exclude<Busy, null>, op: () => Promise<ElectronLocalDaemonResult> | undefined) => {
    setBusy(kind); setError(null); setNotice(null); setStep(null)
    try {
      const res = await op()
      if (res) {
        registerLocalHost({ url: res.url, token: res.token, hostname: res.hostname })
        setNotice([t('settings.dev.local.registered', { name: res.hostname }), res.bindNote].filter(Boolean).join(' — '))
      }
    } catch (err) {
      setError(String(err))
    } finally {
      setBusy(null); setStep(null)
      void refresh()
    }
  }, [refresh, registerLocalHost, t])

  const runPath = useCallback(async (kind: 'path-link' | 'path-add-to-shell') => {
    const op = kind === 'path-link' ? api?.localDaemonPathLink : api?.localDaemonPathAddToShell
    if (!op) return
    setBusy(kind); setError(null); setNotice(null); setCliOutput(null)
    try {
      const r = await op()
      setCliOutput([r.stdout, r.stderr].map((x) => x.trimEnd()).filter(Boolean).join('\n') || `exit ${r.code}`)
    } catch (err) {
      setError(String(err))
    } finally {
      setBusy(null)
      void refresh()
    }
  }, [api, refresh])

  if (!api?.localDaemonStatus) return null

  const installed = status?.installed ?? null
  const cli = status?.cli ?? null
  const running = status?.running ?? null
  const alive = status?.alive ?? null
  const updateAvailable = !!installed && !!latestHash && installed.hash !== latestHash
  const restartPending = !!installed && !!running && running.hash !== installed.hash
  // Spec §3.4: Update, else Restart — never both.
  const showRestart = !!alive && !updateAvailable && (!running || restartPending)
  const disabled = busy !== null
  const externalUrl = running?.url ?? (status?.config ? `http://${status.config.bind}:${status.config.port}` : '')

  return (
    <div className="pt-6 border-t border-border-default">
      <h3 className="text-sm font-semibold text-text-primary mb-3">{t('settings.dev.local.heading')}</h3>

      {status && (
        <div className="space-y-1 mb-3 text-xs text-text-secondary">
          <div className="flex items-center justify-between">
            <span>{t('settings.dev.local.target')}</span>
            <span className="font-mono text-text-primary">{status.target.goos}/{status.target.goarch}</span>
          </div>
          {status.managed === 'none' && <div>{t('settings.dev.local.none')}</div>}
          {status.managed === 'external' && (
            <div className="text-status-warning">
              {t('settings.dev.local.external', { url: externalUrl })}
              {status.reason && <div>{t('settings.dev.local.external_reason', { reason: status.reason })}</div>}
            </div>
          )}
          {status.managed === 'managed' && installed && (
            <>
              <div className="flex items-center justify-between">
                <span>{t('settings.dev.local.installed')}</span>
                <span className="font-mono text-text-primary">{installed.version} ({installed.hash})</span>
              </div>
              <div className="flex items-center justify-between">
                <span>{running ? t('settings.dev.local.running') : t('settings.dev.local.stopped')}</span>
                <span className="font-mono">{running ? `${running.version} (${running.hash}) ${running.url}` : '-'}</span>
              </div>
              {alive && !running && <div className="text-status-warning">{t('settings.dev.local.alive_unhealthy', { pid: alive.pid })}</div>}
              {restartPending && <div className="text-status-warning">{t('settings.dev.local.restart_pending', { hash: installed.hash })}</div>}
              {updateAvailable
                ? <div className="text-status-warning">{t('settings.dev.local.update_available')}</div>
                : (running && !restartPending && <div>{t('settings.dev.local.up_to_date')}</div>)}
            </>
          )}
          {cfg && (
            <>
              <div className="flex items-center justify-between">
                <span>{t('settings.dev.local.url')}</span>
                <span className="font-mono text-text-primary">{cfgUrl}</span>
              </div>
              <div className="flex items-center justify-between">
                <span>{t('settings.dev.local.token')}</span>
                {cfg.token === null ? (
                  <span className="text-status-warning">{t('settings.dev.local.token_missing')}</span>
                ) : (
                  <span className="flex items-center gap-1">
                    <span className="font-mono text-text-primary">{revealed ? cfg.token : '••••••••••••'}</span>
                    <button type="button" onClick={() => setRevealed((v) => !v)} aria-label={revealed ? t('settings.dev.local.btn.hide') : t('settings.dev.local.btn.reveal')} className="p-0.5 rounded hover:bg-surface-hover cursor-pointer">
                      {revealed ? <EyeSlash size={14} /> : <Eye size={14} />}
                    </button>
                    <button type="button" onClick={() => void copyToken()} aria-label={t('settings.dev.local.btn.copy')} className="p-0.5 rounded hover:bg-surface-hover cursor-pointer">
                      <Copy size={14} />
                    </button>
                    {copied && <span>{t('settings.dev.local.copied')}</span>}
                    {copyError && <span className="text-status-error">{copyError}</span>}
                  </span>
                )}
              </div>
              <div className="flex items-center justify-between">
                <span>{t('settings.dev.local.host_list')}</span>
                {registeredAs ? (
                  <span className="text-text-primary">{t('settings.dev.local.in_hosts', { name: hostLabel(registeredAs.id, registeredLook) })}</span>
                ) : (
                  <button type="button" onClick={addToHosts} disabled={disabled || cfg.token === null} className={btnSecondary}>
                    {t('settings.dev.local.btn.add_host')}
                  </button>
                )}
              </div>
            </>
          )}
          {status.tools.tmux === null && <div className="text-status-warning">{t('settings.dev.local.tmux_missing')}</div>}
        </div>
      )}

      {status && cli && (
        <div className="space-y-1 mb-3 text-xs text-text-secondary">
          <div className="font-semibold text-text-primary">{t('settings.dev.local.cli.heading')}</div>
          {cli.resolved === null ? (
            <div className="text-status-warning">{t('settings.dev.local.cli.unresolved')}</div>
          ) : (
            <>
              <div className="flex items-center justify-between gap-2">
                <span>{t('settings.dev.local.cli.resolved')}</span>
                <span className="font-mono text-text-primary select-text break-all">{cli.resolved}</span>
              </div>
              {!cli.isManagedBinary && <div className="text-status-warning">{t('settings.dev.local.cli.not_managed')}</div>}
            </>
          )}
          {cli.pathSource === 'fallback' && <div className="text-status-warning">{t('settings.dev.local.cli.fallback')}</div>}
          <div className="flex gap-2 pt-1">
            <button type="button" onClick={() => void runPath('path-link')} disabled={disabled} className={btnSecondary}>
              {t('settings.dev.local.cli.btn.link')}
            </button>
            <button type="button" onClick={() => void runPath('path-add-to-shell')} disabled={disabled} className={btnSecondary}>
              {t('settings.dev.local.cli.btn.add_to_shell')}
            </button>
          </div>
          <div className="pt-1">{t('settings.dev.local.cli.commands')}</div>
          <div className="font-mono text-text-primary select-text break-all">{`${status.binPath} path link`}</div>
          <div className="font-mono text-text-primary select-text break-all">{`${status.binPath} path add-to-shell`}</div>
          {cliOutput && <pre className="mt-2 p-2 rounded-md bg-surface-input border border-border-default font-mono text-text-primary whitespace-pre-wrap select-text">{cliOutput}</pre>}
        </div>
      )}

      {error && <div className="text-xs text-status-error mb-3 whitespace-pre-wrap">{error}</div>}
      {notice && <div className="text-xs text-text-secondary mb-3">{notice}</div>}
      {busy && <div className="text-xs text-accent font-mono mb-3">{step ? t(`settings.dev.local.step.${step}`) : '…'}</div>}

      <div className="flex gap-2">
        <button onClick={() => void refresh()} disabled={disabled} className={btnSecondary}>{t('settings.dev.local.btn.refresh')}</button>
        {status?.managed === 'none' && (
          <button
            onClick={() => void run('install', () => daemonBase ? api.localDaemonInstall?.(daemonBase, token) : undefined)}
            disabled={disabled || daemonBase === null}
            title={daemonBase === null ? t('settings.dev.host.required') : undefined}
            className={btnPrimary}
          >{t('settings.dev.local.btn.install')}</button>
        )}
        {status?.managed === 'managed' && (
          <>
            {!alive && (
              <button onClick={() => void run('start', () => api.localDaemonStart?.())} disabled={disabled} className={btnSecondary}>{t('settings.dev.local.btn.start')}</button>
            )}
            {showRestart && (
              <button onClick={() => void run('restart', () => api.localDaemonRestart?.())} disabled={disabled} className={btnSecondary}>{t('settings.dev.local.btn.restart')}</button>
            )}
            {updateAvailable && (
              <button
                onClick={() => void run('install', () => daemonBase ? api.localDaemonInstall?.(daemonBase, token) : undefined)}
                disabled={disabled || daemonBase === null}
                title={daemonBase === null ? t('settings.dev.host.required') : undefined}
                className={btnPrimary}
              >{t('settings.dev.local.btn.update')}</button>
            )}
          </>
        )}
      </div>
    </div>
  )
}
