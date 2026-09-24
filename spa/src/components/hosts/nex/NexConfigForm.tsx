// spa/src/components/hosts/nex/NexConfigForm.tsx — editor for the [nex]
// config section. Nothing here is applied live (I9): PUT /api/config
// persists it, and restartRequired() (nex-config-diff.ts) surfaces the
// daemon's `restart_required` (spec §4.4.2) so the user knows a
// `pdx stop && pdx start` is owed.
import { useEffect, useRef, useState } from 'react'
import { useI18nStore } from '../../../stores/useI18nStore'
import { hostLabel, useHostLook } from '../../../lib/host-look'
import { hostFetch } from '../../../lib/host-api'
import type { NexConfig, NexInfo, ConfigData } from '../../../lib/host-api'
import { Field } from '../form-fields'
import { emptyNexConfig, normalizeNexConfig, restartRequired, SANDBOX_PROFILES } from './nex-config-diff'
import NexListEditor from './NexListEditor'

export interface NexConfigFormProps {
  hostId: string
  config: NexConfig | undefined
  info: NexInfo | null
  onSaved: (cfg: ConfigData) => void
}

interface FieldError {
  field: string | null
  message: string
}

// The daemon's 400 body is plain text naming the offending key, e.g.
// `nex.timeouts.turn: time: invalid duration "soon"` or, for a list entry,
// `nex.repo_roots[0]: must be an absolute path (...)`. A list index is
// folded back onto its base field (there is no per-row error UI). Anything
// that does not match this shape (e.g. the compound
// "nex.repo_roots / nex.service_roots: ..." message) has no single field to
// attach to and becomes the general error line.
const CONFIG_ERROR = /^nex\.([a-zA-Z0-9_.]+)(?:\[\d+\])?:\s*([\s\S]*)$/

function parseConfigError(text: string): FieldError {
  const m = CONFIG_ERROR.exec(text.trim())
  if (!m) return { field: null, message: text.trim() }
  return { field: m[1], message: m[2] }
}

function trimList(list: string[]): string[] {
  return list.map((s) => s.trim()).filter((s) => s !== '')
}

// The daemon replaces the whole [nex] section on PUT, so every key must be
// sent even when empty — trimming only cleans up stray whitespace and drops
// blank list rows added by "Add" and never filled in.
function trimForSubmit(draft: NexConfig): NexConfig {
  return {
    enabled: draft.enabled,
    repo_roots: trimList(draft.repo_roots),
    service_roots: trimList(draft.service_roots),
    claude_bin: draft.claude_bin.trim(),
    path_prepend: trimList(draft.path_prepend),
    sandbox: {
      max_profile: draft.sandbox.max_profile,
      default_profile: draft.sandbox.default_profile,
    },
    timeouts: {
      lease_ttl: draft.timeouts.lease_ttl.trim(),
      interrupt: draft.timeouts.interrupt.trim(),
      turn: draft.timeouts.turn.trim(),
    },
  }
}

const inputClass = 'bg-surface-secondary border border-border-default rounded px-2 py-1 text-sm text-text-primary w-full max-w-xs'
const selectClass = 'bg-surface-secondary border border-border-default rounded px-2 py-1 text-sm text-text-primary'

function FieldErrorText({ field, message }: { field: string; message: string | null }) {
  if (!message) return null
  return <p data-testid={`field-error-${field}`} className="text-xs text-red-400 -mt-2 mb-2">{message}</p>
}

export default function NexConfigForm({ hostId, config, info, onSaved }: NexConfigFormProps) {
  const t = useI18nStore((s) => s.t)
  const hostName = hostLabel(hostId, useHostLook(hostId))

  const [draft, setDraft] = useState<NexConfig>(config ?? emptyNexConfig())
  const [saving, setSaving] = useState(false)
  const [justSaved, setJustSaved] = useState(false)
  const [fieldError, setFieldError] = useState<FieldError | null>(null)
  // A re-sync from a changed `config` prop is skipped while the user has
  // unsaved edits, same pattern as EditorHomePathHostSection.
  const dirtyRef = useRef(false)
  // Bumped by every update() call. handleSave snapshots this when a save
  // starts; if it has moved by the time the response comes back, the user
  // edited a field while the PUT was in flight and that newer draft must
  // win — dirty stays true so the next save resubmits it.
  const editCounterRef = useRef(0)
  // The host the form currently edits, and whether it is still mounted: a
  // PUT response that comes back after either changed belongs to a form
  // that no longer exists and must not touch this one's draft or notify
  // the parent.
  const hostIdRef = useRef(hostId)
  const mountedRef = useRef(false)
  useEffect(() => {
    hostIdRef.current = hostId
  }, [hostId])
  useEffect(() => {
    mountedRef.current = true
    return () => {
      mountedRef.current = false
    }
  }, [])

  useEffect(() => {
    if (dirtyRef.current) return
    setDraft(config ?? emptyNexConfig())
  }, [config])

  const update = (patch: Partial<NexConfig>) => {
    dirtyRef.current = true
    editCounterRef.current += 1
    setJustSaved(false)
    setDraft((d) => ({ ...d, ...patch }))
  }

  const errorFor = (field: string): string | null => (fieldError?.field === field ? fieldError.message : null)

  const handleSave = async () => {
    setSaving(true)
    setJustSaved(false)
    setFieldError(null)
    const body = trimForSubmit(draft)
    const startCounter = editCounterRef.current
    const startHostId = hostId
    const stale = () => !mountedRef.current || hostIdRef.current !== startHostId
    try {
      const res = await hostFetch(hostId, '/api/config', {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ nex: body }),
      })
      if (res.ok) {
        const data = await res.json()
        if (stale()) return
        const nextNex: NexConfig = data.nex ? normalizeNexConfig(data.nex) : body
        // Always notify the caller — but only replace the draft (and clear
        // dirty) if nothing changed it while this request was in flight.
        if (editCounterRef.current === startCounter) {
          dirtyRef.current = false
          setDraft(nextNex)
          setJustSaved(true)
        }
        onSaved(data)
      } else {
        const text = await res.text()
        if (stale()) return
        setFieldError(parseConfigError(text))
      }
    } catch (err) {
      if (stale()) return
      setFieldError({ field: null, message: err instanceof Error ? err.message : String(err) })
    } finally {
      if (!stale()) setSaving(false)
    }
  }

  const needsRestart = restartRequired(config, info)

  return (
    <div className="max-w-2xl">
      <h3 className="text-sm font-semibold text-text-primary mb-3">{t('hosts.nex.config.title')}</h3>

      {needsRestart && (
        <div data-testid="nex-restart-required" className="text-xs text-amber-400 bg-amber-500/10 rounded p-2 mb-3">
          {t('hosts.nex.config.restart_required', { host: hostName })}
        </div>
      )}

      <Field label={t('hosts.nex.config.enabled')}>
        <input
          type="checkbox"
          aria-label={t('hosts.nex.config.enabled')}
          checked={draft.enabled}
          onChange={(e) => update({ enabled: e.target.checked })}
        />
      </Field>

      <NexListEditor
        label={t('hosts.nex.config.repo_roots')}
        values={draft.repo_roots}
        onChange={(v) => update({ repo_roots: v })}
        addLabel={t('hosts.nex.config.add')}
        removeLabel={t('hosts.nex.config.remove')}
      />
      <FieldErrorText field="repo_roots" message={errorFor('repo_roots')} />

      <NexListEditor
        label={t('hosts.nex.config.service_roots')}
        values={draft.service_roots}
        onChange={(v) => update({ service_roots: v })}
        addLabel={t('hosts.nex.config.add')}
        removeLabel={t('hosts.nex.config.remove')}
      />
      <FieldErrorText field="service_roots" message={errorFor('service_roots')} />

      <NexListEditor
        label={t('hosts.nex.config.path_prepend')}
        values={draft.path_prepend}
        onChange={(v) => update({ path_prepend: v })}
        addLabel={t('hosts.nex.config.add')}
        removeLabel={t('hosts.nex.config.remove')}
      />
      <FieldErrorText field="path_prepend" message={errorFor('path_prepend')} />

      <Field label={t('hosts.nex.config.claude_bin')}>
        <input
          type="text"
          aria-label={t('hosts.nex.config.claude_bin')}
          value={draft.claude_bin}
          onChange={(e) => update({ claude_bin: e.target.value })}
          className={inputClass}
        />
      </Field>
      <FieldErrorText field="claude_bin" message={errorFor('claude_bin')} />

      <Field label={t('hosts.nex.config.max_profile')}>
        <select
          aria-label={t('hosts.nex.config.max_profile')}
          value={draft.sandbox.max_profile}
          onChange={(e) => update({ sandbox: { ...draft.sandbox, max_profile: e.target.value } })}
          className={selectClass}
        >
          {SANDBOX_PROFILES.map((p) => (
            <option key={p} value={p}>{p === '' ? t('hosts.nex.config.nexen_default') : p}</option>
          ))}
        </select>
      </Field>
      <FieldErrorText field="sandbox.max_profile" message={errorFor('sandbox.max_profile')} />

      <Field label={t('hosts.nex.config.default_profile')}>
        <select
          aria-label={t('hosts.nex.config.default_profile')}
          value={draft.sandbox.default_profile}
          onChange={(e) => update({ sandbox: { ...draft.sandbox, default_profile: e.target.value } })}
          className={selectClass}
        >
          {SANDBOX_PROFILES.map((p) => (
            <option key={p} value={p}>{p === '' ? t('hosts.nex.config.nexen_default') : p}</option>
          ))}
        </select>
      </Field>
      <FieldErrorText field="sandbox.default_profile" message={errorFor('sandbox.default_profile')} />

      <Field label={t('hosts.nex.config.lease_ttl')}>
        <input
          type="text"
          aria-label={t('hosts.nex.config.lease_ttl')}
          placeholder={t('hosts.nex.config.nexen_default')}
          value={draft.timeouts.lease_ttl}
          onChange={(e) => update({ timeouts: { ...draft.timeouts, lease_ttl: e.target.value } })}
          className={inputClass}
        />
      </Field>
      <FieldErrorText field="timeouts.lease_ttl" message={errorFor('timeouts.lease_ttl')} />

      <Field label={t('hosts.nex.config.interrupt')}>
        <input
          type="text"
          aria-label={t('hosts.nex.config.interrupt')}
          placeholder={t('hosts.nex.config.nexen_default')}
          value={draft.timeouts.interrupt}
          onChange={(e) => update({ timeouts: { ...draft.timeouts, interrupt: e.target.value } })}
          className={inputClass}
        />
      </Field>
      <FieldErrorText field="timeouts.interrupt" message={errorFor('timeouts.interrupt')} />

      <Field label={t('hosts.nex.config.turn')}>
        <input
          type="text"
          aria-label={t('hosts.nex.config.turn')}
          placeholder={t('hosts.nex.config.nexen_default')}
          value={draft.timeouts.turn}
          onChange={(e) => update({ timeouts: { ...draft.timeouts, turn: e.target.value } })}
          className={inputClass}
        />
      </Field>
      <FieldErrorText field="timeouts.turn" message={errorFor('timeouts.turn')} />

      <div className="flex items-center gap-3 mt-3">
        <button
          type="button"
          onClick={handleSave}
          disabled={saving}
          className="px-3 py-1.5 rounded-md bg-accent text-white text-sm hover:bg-accent/90 disabled:opacity-50 cursor-pointer"
        >
          {saving ? t('hosts.nex.config.saving') : t('hosts.nex.config.save')}
        </button>
        {justSaved && <span className="text-xs text-green-400">{t('hosts.nex.config.saved')}</span>}
        {fieldError?.field === null && (
          <span data-testid="nex-config-error" className="text-xs text-red-400">
            {t('hosts.nex.config.error', { message: fieldError.message })}
          </span>
        )}
      </div>
    </div>
  )
}
