// spa/src/components/hosts/RelaySection.tsx — Hosts › 接力 (lead-team-relay spec §8.7 (a), §8.8): the two per-host
// self-relay switches the daemon reads when a session asks to relay, the one line that says a member has none, and
// the three relay prompt editors (U21). Same gate and queued CAS saves as the other host config sections. The
// switches and the bodies are one `relay` row with one revision, so every write — a toggle or a body — is one PUT of
// the whole object on the one queue key, built from the store when its task RUNS: two quick actions serialize
// instead of racing on the revision, and neither drops what the other wrote.
import { useCallback, useEffect, useState } from 'react'
import { HostConfigConflictError, type RelaySwitches } from '../../lib/host-config-api'
import { hostConfigQueueKey, queueHostConfigSave } from '../../lib/host-config-queue'
import { fetchRelayPrompts, RELAY_PROMPT_KINDS, type RelayPromptKind, type RelayPrompts } from '../../lib/relay-prompts-api'
import { useHostConfigStore } from '../../stores/useHostConfigStore'
import { useHostStore } from '../../stores/useHostStore'
import { useI18nStore } from '../../stores/useI18nStore'
import { ToggleSwitch } from '../settings/ToggleSwitch'
import { HostConfigNotice, HostConfigProblemNotice, useHostConfigGate } from './HostConfigNotice'
import { RelayPromptEditor } from './RelayPromptEditor'

const PROMPT_FIELD = { write: 'prompt_write', fix: 'prompt_fix', seed: 'prompt_seed' } as const satisfies Record<RelayPromptKind, keyof RelaySwitches>

type PromptsState =
  | { status: 'loading' }
  | { status: 'ready'; prompts: RelayPrompts }
  | { status: 'unsupported' }
  | { status: 'error'; reason: string }

export function RelaySection({ hostId }: { hostId: string }) {
  const t = useI18nStore((s) => s.t)
  const { entry, editable, notice } = useHostConfigGate(hostId)
  const online = useHostStore((s) => s.runtime[hostId]?.status === 'connected')
  const [saveError, setSaveError] = useState<string | null>(null)
  const [pending, setPending] = useState(false)
  const [prompts, setPrompts] = useState<PromptsState>({ status: 'loading' })

  const known = entry.status === 'ready' || entry.status === 'unsupported'
  const unsupported = known && !entry.relaySupported

  // The defaults, fixed parts and variables come from the daemon (one source); the bodies come from the host config
  // row, which a save updates. Fetched once per mount, again only after a reconnect.
  useEffect(() => {
    if (!online) return
    const abort = new AbortController()
    fetchRelayPrompts(hostId, abort.signal).then(
      (r) => { if (!abort.signal.aborted) setPrompts(r === 'unsupported' ? { status: 'unsupported' } : { status: 'ready', prompts: r }) },
      (err: unknown) => { if (!abort.signal.aborted) setPrompts({ status: 'error', reason: err instanceof Error ? err.message : String(err) }) },
    )
    return () => abort.abort()
  }, [hostId, online])

  const toggle = useCallback((field: 'self_solo' | 'self_lead') => {
    setPending(true)
    setSaveError(null)
    // A click is a TOGGLE of the value stored when its task RUNS (PR #1742 R1): two quick clicks queue two tasks
    // that both captured the same rendered `checked`; flipping the store's current value makes the second undo
    // the first, as the person meant, instead of writing the same value twice.
    void queueHostConfigSave(hostConfigQueueKey(hostId, 'relay'), async () => {
      try {
        const current = useHostConfigStore.getState().byHost[hostId]?.relay
        if (!current) return
        await useHostConfigStore.getState().saveRelay(hostId, { ...current, [field]: !current[field] })
      } catch (err) {
        setSaveError(err instanceof HostConfigConflictError
          ? t('host_config.conflict')
          : t('host_config.save_failed', { reason: err instanceof Error ? err.message : String(err) }))
      } finally {
        setPending(false)
      }
    })
  }, [hostId, t])

  // A body save keeps the switches and the other two bodies: the whole stored row, one field replaced. It rejects
  // on failure, so the editor keeps the draft and shows why (a 409 included).
  const savePrompt = useCallback((kind: RelayPromptKind, value: string) =>
    queueHostConfigSave(hostConfigQueueKey(hostId, 'relay'), async () => {
      const current = useHostConfigStore.getState().byHost[hostId]?.relay
      if (!current) throw new Error(`host config for ${hostId} is not loaded`)
      await useHostConfigStore.getState().saveRelay(hostId, { ...current, [PROMPT_FIELD[kind]]: value })
    }), [hostId])

  // Not locked while a save is in flight: the queue serializes a second toggle behind the first (and the task reads
  // the store when it runs), so a fast second click is kept, not dropped.
  const locked = !editable

  return (
    <div className="max-w-3xl" data-testid="relay-section" aria-busy={pending || undefined}>
      <h2 className="text-lg font-semibold mb-4">{t('hosts.relay')}</h2>
      <HostConfigNotice notice={notice} />
      {/* An unreadable value shows both switches off, as the daemon reads it; a toggle rewrites it. */}
      <HostConfigProblemNotice problem={entry.problems.relay} title="hosts.relay" />
      {unsupported ? (
        <p data-testid="relay-unsupported" className="text-xs text-text-muted">{t('hosts.relay.unsupported')}</p>
      ) : (
        <>
          <p className="mb-4 text-xs text-text-muted">{t('hosts.relay.desc')}</p>
          <div className="border border-border-subtle rounded-lg divide-y divide-border-subtle">
            <div className="flex items-center justify-between gap-4 px-3 py-2">
              <span className="text-sm text-text-primary">{t('hosts.relay.self_solo')}</span>
              <span className={locked ? 'opacity-50 pointer-events-none' : ''}>
                <ToggleSwitch testId="relay-self-solo" label={t('hosts.relay.self_solo')} checked={entry.relay.self_solo}
                  onChange={() => { if (!locked) toggle('self_solo') }} />
              </span>
            </div>
            <div className="flex items-center justify-between gap-4 px-3 py-2">
              <span className="text-sm text-text-primary">{t('hosts.relay.self_lead')}</span>
              <span className={locked ? 'opacity-50 pointer-events-none' : ''}>
                <ToggleSwitch testId="relay-self-lead" label={t('hosts.relay.self_lead')} checked={entry.relay.self_lead}
                  onChange={() => { if (!locked) toggle('self_lead') }} />
              </span>
            </div>
            <div className="flex items-center justify-between gap-4 px-3 py-2">
              <span className="text-sm text-text-primary">{t('hosts.relay.member')}</span>
              <span data-testid="relay-member-note" className="text-xs text-text-muted">{t('hosts.relay.member_note')}</span>
            </div>
          </div>
          {saveError && <p data-testid="relay-save-error" className="mt-3 text-xs text-status-warning whitespace-pre-wrap">{saveError}</p>}

          {prompts.status === 'unsupported' && (
            <p data-testid="relay-prompts-unsupported" className="mt-6 text-xs text-text-muted">{t('hosts.relay.prompts.unsupported')}</p>
          )}
          {(prompts.status === 'ready' || prompts.status === 'error') && (
            <div data-testid="relay-prompts" className="mt-8">
              <h3 className="text-base font-semibold mb-1">{t('hosts.relay.prompts.title')}</h3>
              <p className="mb-3 text-xs text-text-muted">{t('hosts.relay.prompts.desc')}</p>
              {prompts.status === 'error' ? (
                <p data-testid="relay-prompts-error" className="text-xs text-status-warning">
                  {t('hosts.relay.prompts.load_failed', { reason: prompts.reason })}
                </p>
              ) : (
                <div className="space-y-3">
                  {RELAY_PROMPT_KINDS.map((kind) => (
                    <RelayPromptEditor key={`${hostId}:${kind}`} hostId={hostId} kind={kind}
                      fixed={prompts.prompts.fixed[kind]} defaultBody={prompts.prompts.defaults[kind]}
                      stored={entry.relay[PROMPT_FIELD[kind]] ?? ''} variables={prompts.prompts.variables} locked={locked}
                      onSave={(value) => savePrompt(kind, value)} onRestore={() => savePrompt(kind, '')} />
                  ))}
                </div>
              )}
            </div>
          )}
        </>
      )}
    </div>
  )
}
