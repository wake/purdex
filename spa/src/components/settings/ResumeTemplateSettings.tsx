// spa/src/components/settings/ResumeTemplateSettings.tsx — the per-agent
// resume command templates of ONE host and their save-time check (spec §4.5;
// per host since host-launcher spec §4.2).
//
// Five things live here and nowhere else:
//
//  1. **The probe gets the command word, not the template.** The daemon hands
//     what it receives to the shell as a single positional parameter, so
//     `cld-yolo --resume {id}` would be looked up verbatim and answer
//     `not_found`. Splitting off the first whitespace-separated token — with
//     `{id}` never substituted — is this component's job (spec §4.4).
//  2. **Nothing here can block a save.** The template is saved to the host the
//     moment the row commits; every verdict, including "could not check", is
//     advice arriving afterwards.
//  3. **Per host.** Templates are this host's daemon copy (host-launcher spec
//     §4.2); the Test runs against the same host.
//  4. **A 404 is `unverifiable`.** An older daemon has no such endpoint, and
//     that is not the user's problem to debug (spec §8) — as is a network
//     failure, which says nothing about the command either.
//  5. **The limits are on screen**: the test approximates the pane's shell
//     rather than reproducing it.
//
// A verdict is keyed by `(hostId, commandWord)` and shown only while both still
// match, so a verdict about a word the user has since edited can never sit
// beside the command being judged now. A response that lands after either
// changed is discarded rather than rendered.
//
// That pair is not enough on its own, because it can come back: editing a word
// and retyping it restores the exact pair an abandoned request was sent under.
// So each request also carries a REVISION, and anything that abandons a
// request — an edit, a revert, Reset all, or simply pressing Test again —
// drops the row's revision. A response is taken only when its revision is
// still the row's current one AND the pair still holds; otherwise two requests
// racing on one row could settle out of order and leave the older answer on
// screen.
import { useRef, useState } from 'react'
import { ArrowCounterClockwise, Warning } from '@phosphor-icons/react'
import { AGENT_NAMES } from '../../lib/agent-metadata'
import { resolveShellCommand, type ShellResolveVerdict } from '../../lib/host-api'
import { commandWordOf } from '../../lib/command-word'
import { HostConfigConflictError } from '../../lib/host-config-api'
import { useResumeTemplateLookup, type ResumeTemplatePair } from '../../lib/resume-templates'
import { useHostConfigStore } from '../../stores/useHostConfigStore'
import { useI18nStore } from '../../stores/useI18nStore'
import { ShellVerdict } from './ShellVerdict'

type Field = 'exact' | 'fallback'

const FIELDS: readonly Field[] = ['exact', 'fallback']

/** Literal keys, not interpolated ones — they must be greppable in this file. */
const FIELD_LABEL: Record<Field, string> = {
  exact: 'resume_template.field.exact',
  fallback: 'resume_template.field.fallback',
}

/** A verdict, plus the two things it is only true of. */
interface RowResult {
  hostId: string
  commandWord: string
  /** `'pending'` while the request is in flight. */
  verdict: ShellResolveVerdict | 'pending'
}

function rowKey(agentType: string, field: Field): string {
  return `${agentType}:${field}`
}

/** An agent nobody has a shape for: the user may still teach the host one. */
const BLANK: ResumeTemplatePair = { exact: '', fallback: '' }

export function ResumeTemplateSettings({ hostId, busy = false }: { hostId: string; busy?: boolean }) {
  const t = useI18nStore((s) => s.t)
  const lookup = useResumeTemplateLookup(hostId)
  const ready = useHostConfigStore((s) => s.byHost[hostId]?.status === 'ready')
  const [saveError, setSaveError] = useState<string | null>(null)
  // Editing is only meaningful against a loaded copy: its revision is what the
  // PUT is compared against.
  const locked = busy || !ready

  // Uncommitted edits, and only those: a row is entered here by an edit and
  // leaves on the commit or the revert that ends it. Absent means "whatever the
  // host answers", so a reset — or a reload after another client's save —
  // repaints without any effect syncing state.
  const [drafts, setDrafts] = useState<Record<string, string>>({})
  const [results, setResults] = useState<Record<string, RowResult>>({})

  // The live request per row, and the counter that names them. Not state: no
  // render reads it, and it must be readable by a settle that started before
  // the render it is racing.
  const requestSeq = useRef(0)
  const liveRequest = useRef<Record<string, number>>({})

  /** Abandon whatever request `key` has out; its answer is no longer wanted. */
  const abandonRequest = (key: string) => { delete liveRequest.current[key] }

  /** Abandon every row's request — what Reset all does. */
  const abandonAllRequests = () => { liveRequest.current = {} }

  const liveValue = (agentType: string, field: Field): string => {
    const key = rowKey(agentType, field)
    return drafts[key] ?? lookup(agentType)?.[field] ?? ''
  }

  // What is on screen right now, for the async settle to compare against. A
  // read-only mirror of this render — never a source of truth.
  const liveRef = useRef<{ hostId: string; words: Record<string, string> }>({ hostId, words: {} })
  liveRef.current = {
    hostId,
    words: Object.fromEntries(
      Object.keys(AGENT_NAMES).flatMap((agentType) =>
        FIELDS.map((field) => [rowKey(agentType, field), commandWordOf(liveValue(agentType, field))]),
      ),
    ),
  }

  const dropResult = (key: string) =>
    setResults(({ [key]: _dropped, ...rest }) => rest)

  /** Hand the row back to the host copy, whether it was committed or discarded. */
  const dropDraft = (key: string) =>
    setDrafts(({ [key]: _dropped, ...rest }) => rest)

  const handleChange = (agentType: string, field: Field, value: string) => {
    const key = rowKey(agentType, field)
    setDrafts((d) => ({ ...d, [key]: value }))
    // Editing the row invalidates its verdict — including one still in flight,
    // and including one whose word the next keystroke happens to restore.
    dropResult(key)
    abandonRequest(key)
  }

  const persist = async (next: Record<string, ResumeTemplatePair>) => {
    setSaveError(null)
    try {
      await useHostConfigStore.getState().saveResumeTemplates(hostId, next)
    } catch (err) {
      setSaveError(err instanceof HostConfigConflictError
        ? t('host_config.conflict')
        : t('host_config.save_failed', { reason: err instanceof Error ? err.message : String(err) }))
    }
  }

  const handleCommit = (agentType: string, field: Field, value: string) => {
    const current = useHostConfigStore.getState().byHost[hostId]?.resumeTemplates ?? {}
    // The edit lands on top of whatever currently answers for this agent, so
    // editing one field never silently blanks the other.
    const base = lookup(agentType) ?? BLANK
    // The save carries this value, so the draft has nothing left to protect —
    // and a draft that outlives its commit PINS the row: a reload or a
    // conflict would repaint every panel except the one being edited here, and
    // Test would go on judging the stale word. A draft is uncommitted state only.
    dropDraft(rowKey(agentType, field))
    void persist({ ...current, [agentType]: { ...base, [field]: value } })
  }

  const handleRevert = (agentType: string, field: Field) => {
    const key = rowKey(agentType, field)
    dropDraft(key)
    dropResult(key)
    abandonRequest(key)
  }

  const handleResetAll = () => {
    setDrafts({})
    setResults({})
    abandonAllRequests()
    void persist({})
  }

  /**
   * Keep a verdict only while the request that asked for it is still the row's
   * live one AND the host and word it judged still stand.
   *
   * A superseded revision writes nothing at all: whatever abandoned it has
   * already dropped this row's result, and anything sitting there now belongs
   * to a later request this one must not touch.
   */
  const settle = (
    key: string,
    rev: number,
    forHost: string,
    word: string,
    verdict: ShellResolveVerdict,
  ) => {
    if (liveRequest.current[key] !== rev) return
    const live = liveRef.current
    if (live.hostId !== forHost || live.words[key] !== word) {
      setResults((r) => {
        const current = r[key]
        if (!current || current.hostId !== forHost || current.commandWord !== word) return r
        const { [key]: _dropped, ...rest } = r
        return rest
      })
      return
    }
    setResults((r) => ({ ...r, [key]: { hostId: forHost, commandWord: word, verdict } }))
  }

  const runTest = async (agentType: string, field: Field) => {
    const key = rowKey(agentType, field)
    const word = commandWordOf(liveValue(agentType, field))
    if (!word || !hostId) return
    const forHost = hostId
    // Pressing Test again abandons the previous request for this row: the newer
    // question is the one being asked, whatever order the answers arrive in.
    const rev = ++requestSeq.current
    liveRequest.current[key] = rev
    setResults((r) => ({ ...r, [key]: { hostId: forHost, commandWord: word, verdict: 'pending' } }))
    try {
      settle(key, rev, forHost, word, await resolveShellCommand(forHost, word))
    } catch {
      // Contract 4's other half: the daemon was unreachable, which says nothing
      // about the command. The template is already saved either way.
      settle(key, rev, forHost, word, { status: 'unverifiable' })
    }
  }

  /** A result is shown only while the pair it was taken for still holds. */
  const shownResult = (agentType: string, field: Field): RowResult | undefined => {
    const key = rowKey(agentType, field)
    const result = results[key]
    if (!result) return undefined
    if (result.hostId !== hostId) return undefined
    if (result.commandWord !== commandWordOf(liveValue(agentType, field))) return undefined
    return result
  }

  return (
    <div data-testid="resume-templates" className="mt-6">
      <h3 className="text-sm text-text-primary">{t('resume_template.title')}</h3>
      <p data-testid="resume-template-limits" className="mt-1 text-xs text-text-secondary">
        {t('resume_template.limit_host')}
        {' '}
        {t('resume_template.limit_probe')}
      </p>
      {saveError ? (
        <p data-testid="resume-template-save-error" className="mt-2 text-xs text-status-warning">{saveError}</p>
      ) : null}

      <div className="mt-3 flex flex-col gap-3">
        {Object.keys(AGENT_NAMES).map((agentType) => (
          <div key={agentType} data-testid={`resume-template-agent-${agentType}`}>
            <div className="text-xs text-text-primary">{AGENT_NAMES[agentType]}</div>
            {FIELDS.map((field) => {
              const value = liveValue(agentType, field)
              const result = shownResult(agentType, field)
              return (
                <TemplateRow
                  key={field}
                  agentType={agentType}
                  field={field}
                  value={value}
                  busy={locked}
                  pending={result?.verdict === 'pending'}
                  result={result}
                  t={t}
                  onChange={handleChange}
                  onCommit={handleCommit}
                  onRevert={handleRevert}
                  onTest={runTest}
                />
              )
            })}
          </div>
        ))}
      </div>

      <button
        type="button"
        data-testid="resume-template-reset"
        onClick={handleResetAll}
        disabled={locked}
        className="mt-3 flex items-center gap-1.5 rounded-md border border-border-default px-3 py-1.5 text-xs text-text-secondary hover:border-border-active hover:text-text-primary disabled:cursor-not-allowed disabled:opacity-50"
      >
        <ArrowCounterClockwise size={14} />
        {t('resume_template.reset_all')}
      </button>
    </div>
  )
}

/**
 * One template field. The three mechanics are `EditableCwdCell`'s (alpha.324),
 * and they are here for the same reasons they are there: `committedRef` so the
 * blur that follows an Enter cannot commit a second time, `disabled` so a row
 * cannot be edited under an action whose result would overwrite it, and
 * `composingRef` + `isComposing` so an IME Enter confirms a candidate instead
 * of committing a half-composed value.
 */
function TemplateRow({
  agentType,
  field,
  value,
  busy,
  pending,
  result,
  t,
  onChange,
  onCommit,
  onRevert,
  onTest,
}: {
  agentType: string
  field: Field
  value: string
  busy: boolean
  pending: boolean
  result?: RowResult
  t: (key: string, params?: Record<string, string | number>) => string
  onChange: (agentType: string, field: Field, value: string) => void
  onCommit: (agentType: string, field: Field, value: string) => void
  onRevert: (agentType: string, field: Field) => void
  onTest: (agentType: string, field: Field) => void
}) {
  const committedRef = useRef(false)
  const composingRef = useRef(false)

  const commit = () => {
    if (committedRef.current) return
    committedRef.current = true
    onCommit(agentType, field, value)
  }

  const handleKeyDown = (e: React.KeyboardEvent<HTMLInputElement>) => {
    if (composingRef.current || e.nativeEvent.isComposing) return
    if (e.key === 'Enter') {
      e.preventDefault()
      commit()
    } else if (e.key === 'Escape') {
      e.preventDefault()
      // Latch so the trailing blur cannot commit the value being discarded.
      committedRef.current = true
      onRevert(agentType, field)
    }
  }

  const warning = warningFor(field, value)

  return (
    <div className="mt-1 flex flex-wrap items-center gap-2 text-xs">
      <span className="w-28 shrink-0 text-text-secondary">{t(FIELD_LABEL[field])}</span>
      <input
        type="text"
        data-testid={`resume-template-input-${agentType}-${field}`}
        value={value}
        disabled={busy}
        spellCheck={false}
        onChange={(e) => {
          committedRef.current = false
          onChange(agentType, field, e.target.value)
        }}
        onKeyDown={handleKeyDown}
        onCompositionStart={() => { composingRef.current = true }}
        onCompositionEnd={() => { composingRef.current = false }}
        onBlur={commit}
        className="min-w-56 flex-1 rounded border border-border-default bg-bg-input px-2 py-1 font-mono text-text-primary outline-none focus:border-border-active disabled:opacity-50"
      />
      <button
        type="button"
        data-testid={`resume-template-test-${agentType}-${field}`}
        onClick={() => onTest(agentType, field)}
        disabled={busy || pending || !commandWordOf(value)}
        className="rounded-md border border-border-default px-2 py-1 text-text-secondary hover:border-border-active hover:text-text-primary disabled:cursor-not-allowed disabled:opacity-50"
      >
        {t('resume_template.test')}
      </button>
      {result ? <ShellVerdict testId={`resume-template-verdict-${agentType}-${field}`} verdict={result.verdict} t={t} /> : null}
      {warning ? (
        <span
          data-testid={`resume-template-warning-${agentType}-${field}`}
          className="flex w-full items-center gap-1 text-status-warning"
        >
          <Warning size={14} />
          {t(warning)}
        </span>
      ) : null}
    </div>
  )
}

/**
 * The two shapes that still save (spec §4.5): an `exact` without `{id}` resolves
 * to the literal template, and a `{id}` in `fallback` stays literal because
 * there is no id to put there.
 */
function warningFor(field: Field, value: string): string | undefined {
  if (!value.trim()) return undefined
  if (field === 'exact' && !value.includes('{id}')) return 'resume_template.warning.exact_missing_id'
  if (field === 'fallback' && value.includes('{id}')) return 'resume_template.warning.fallback_has_id'
  return undefined
}
