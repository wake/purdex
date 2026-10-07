// spa/src/components/hosts/RelayPromptEditor.tsx — one relay prompt on Hosts › 接力 (lead-team-relay spec §8.8, U21;
// plan v3 P9a-3): the fixed head and tail read-only around the editable body, the variables, a byte counter, 儲存
// and 還原預設. The client check mirrors the daemon's; the daemon stays the authority (a 400 shows its detail).
//
// The unsaved text lives in `relay-prompt-draft-memory`, not in component state: a tab switch or a Hosts sub-page
// switch unmounts this (the tab-hosted checklist in the repo CLAUDE.md).
import { useState } from 'react'
import { HostConfigConflictError } from '../../lib/host-config-api'
import { checkRelayPromptBody, RELAY_PROMPT_MAX_BYTES, relayPromptBytes } from '../../lib/relay-prompt-check'
import {
  normalizeRelayPromptBody,
  relayPromptValueToStore,
  type RelayPromptFixed,
  type RelayPromptKind,
} from '../../lib/relay-prompts-api'
import { forgetRelayPromptDraft, readRelayPromptDraft, relayPromptDraftKey, writeRelayPromptDraft } from '../../lib/relay-prompt-draft-memory'
import { useI18nStore } from '../../stores/useI18nStore'

export interface RelayPromptEditorProps {
  hostId: string
  kind: RelayPromptKind
  fixed: RelayPromptFixed
  defaultBody: string
  /** The `relay` row's value: `""` is unset, i.e. the default. */
  stored: string
  /** The `{{name}}`s a body may use, as the daemon reports them. */
  variables: string[]
  locked: boolean
  /** Saves the value to store (`""` = the default); rejects when the save failed. */
  onSave: (value: string) => Promise<void>
  /** Saves `""`. */
  onRestore: () => Promise<void>
}

const PLACEHOLDER = /\{\{([a-z_]+)\}\}/g
const FIXED_CLASS = 'whitespace-pre-wrap break-words rounded border border-dashed border-border-default bg-surface-secondary px-2 py-1 font-mono text-xs text-text-secondary'
const BUTTON_CLASS = 'px-3 py-1.5 rounded text-xs cursor-pointer disabled:opacity-50 disabled:cursor-not-allowed'

export function RelayPromptEditor({ hostId, kind, fixed, defaultBody, stored, variables, locked, onSave, onRestore }: RelayPromptEditorProps) {
  const t = useI18nStore((s) => s.t)
  const key = relayPromptDraftKey(hostId, kind)
  // The memory is the draft's home; this state only re-renders when it changes.
  const [draft, setDraft] = useState(() => readRelayPromptDraft(key))
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const base = stored || defaultBody
  const text = draft ?? base
  const value = relayPromptValueToStore(text, defaultBody)
  const problem = value === '' ? null : checkRelayPromptBody(value)
  const canSave = !locked && !busy && problem === null && value !== stored
  const canRestore = !locked && !busy && (stored !== '' || draft !== undefined)

  // The mod's own placeholders in the fixed parts: the others are in the variable list.
  const modVars = [...new Set([...`${fixed.head}${fixed.tail}`.matchAll(PLACEHOLDER)].map((m) => m[1]))]
    .filter((name) => !variables.includes(name))

  const setText = (next: string) => {
    setError(null)
    if (normalizeRelayPromptBody(next) === base) {
      forgetRelayPromptDraft(key)
      setDraft(undefined)
    } else {
      writeRelayPromptDraft(key, next)
      setDraft(next)
    }
  }

  const run = async (action: () => Promise<void>) => {
    const sent = readRelayPromptDraft(key)
    setBusy(true)
    setError(null)
    try {
      await action()
      // Text typed while the save was in flight is a newer draft: it stays.
      if (readRelayPromptDraft(key) === sent) {
        forgetRelayPromptDraft(key)
        setDraft(undefined)
      }
    } catch (err) {
      setError(err instanceof HostConfigConflictError
        ? t('host_config.conflict')
        : t('host_config.save_failed', { reason: err instanceof Error ? err.message : String(err) }))
    } finally {
      setBusy(false)
    }
  }

  const restore = () => {
    // Nothing stored to clear: only an unsaved draft to throw away.
    if (stored === '') {
      setText(base)
      return
    }
    void run(onRestore)
  }

  const id = `relay-prompt-${kind}`
  return (
    <section data-testid={id} className="border border-border-subtle rounded-lg p-3">
      <div className="flex items-center gap-2">
        <h4 className="text-sm text-text-primary">{t(`hosts.relay.prompts.${kind}`)}</h4>
        <span data-testid={`${id}-badge`} className="rounded bg-surface-tertiary px-1.5 py-0.5 text-[10px] text-text-secondary">
          {t(stored === '' ? 'hosts.relay.prompts.default' : 'hosts.relay.prompts.custom')}
        </span>
      </div>
      <p className="mt-0.5 mb-2 text-xs text-text-muted">{t(`hosts.relay.prompts.${kind}_desc`)}</p>

      <div className="mb-0.5 text-[11px] text-text-muted">{t('hosts.relay.prompts.fixed')}</div>
      <pre data-testid={`${id}-head`} className={FIXED_CLASS}>{fixed.head}</pre>
      <textarea
        data-testid={`${id}-box`}
        aria-label={t(`hosts.relay.prompts.${kind}`)}
        value={text}
        readOnly={locked}
        spellCheck={false}
        rows={kind === 'fix' ? 3 : 8}
        onChange={(e) => setText(e.target.value)}
        className="my-1 w-full resize-y rounded border border-border-default bg-surface-input px-2 py-1.5 font-mono text-xs text-text-primary focus:border-border-active focus:outline-none"
      />
      {fixed.tail !== '' && (
        <>
          <div className="mb-0.5 text-[11px] text-text-muted">{t('hosts.relay.prompts.fixed')}</div>
          <pre data-testid={`${id}-tail`} className={FIXED_CLASS}>{fixed.tail}</pre>
        </>
      )}
      {modVars.length > 0 && (
        <p data-testid={`${id}-mod-vars`} className="mt-1 text-[11px] text-text-muted">
          {t('hosts.relay.prompts.fixed_vars', { names: modVars.map((n) => `{{${n}}}`).join(' ') })}
        </p>
      )}

      <div data-testid={`${id}-variables`} className="mt-2 flex flex-wrap gap-x-3 gap-y-1 text-[11px] text-text-muted">
        <span>{t('hosts.relay.prompts.variables')}</span>
        {variables.map((name) => {
          const descKey = `hosts.relay.prompts.var.${name}`
          const desc = t(descKey)
          return (
            <span key={name}>
              <code className="font-mono text-text-secondary">{`{{${name}}}`}</code>
              {desc !== descKey && ` ${desc}`}
            </span>
          )
        })}
      </div>

      {problem && (
        <p data-testid={`${id}-problem`} className="mt-2 text-xs text-status-warning">
          {t(`hosts.relay.prompts.${problem}`, { max: RELAY_PROMPT_MAX_BYTES })}
        </p>
      )}
      {error && <p data-testid={`${id}-error`} className="mt-2 text-xs text-status-warning whitespace-pre-wrap">{error}</p>}

      <div className="mt-2 flex items-center gap-2">
        <button type="button" data-testid={`${id}-save`} disabled={!canSave} onClick={() => void run(() => onSave(value))}
          className={`${BUTTON_CLASS} bg-accent text-white`}>{t('hosts.relay.prompts.save')}</button>
        <button type="button" data-testid={`${id}-restore`} disabled={!canRestore} onClick={restore}
          className={`${BUTTON_CLASS} bg-surface-tertiary text-text-secondary`}>{t('hosts.relay.prompts.restore_default')}</button>
        <span data-testid={`${id}-counter`} className="ml-auto text-[11px] tabular-nums text-text-muted">
          {`${relayPromptBytes(normalizeRelayPromptBody(text))} / ${RELAY_PROMPT_MAX_BYTES}`}
        </span>
      </div>
    </section>
  )
}
