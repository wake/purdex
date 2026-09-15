// spa/src/lib/resume-templates.ts — per-agent resume command templates (spec
// §4.2 of the host-launcher design). Defaults and lookup semantics are carried
// over verbatim from the retired global (persisted) template store; the overrides now
// live on each host's daemon (`useHostConfigStore`), so every lookup is built
// FOR a host.
import { useMemo } from 'react'
import { useHostConfigStore } from '../stores/useHostConfigStore'

export interface ResumeTemplatePair {
  /** Used when the record has a usable session id. Should contain `{id}`. */
  exact: string
  /** Used when it does not — taken verbatim, `{id}` included if present. */
  fallback: string
}

/** How a consumer asks for an agent's pair; `undefined` means "no template". */
export type ResumeTemplateLookup = (agentType: string) => ResumeTemplatePair | undefined

/** The shapes that shipped hardcoded: a host with no overrides sees exactly these. */
export const DEFAULT_RESUME_TEMPLATES: Readonly<Record<string, ResumeTemplatePair>> = Object.freeze({
  cc: Object.freeze({ exact: 'claude --resume {id}', fallback: 'claude -c' }),
  codex: Object.freeze({ exact: 'codex resume {id}', fallback: 'codex resume --last' }),
  opencode: Object.freeze({ exact: 'opencode -s {id}', fallback: 'opencode -c' }),
})

/**
 * Own properties only: an agent type is an open string from a daemon payload,
 * so `overrides['constructor']` must not resolve up the prototype chain.
 */
function own<T>(map: Readonly<Record<string, T>>, key: string): T | undefined {
  return Object.prototype.hasOwnProperty.call(map, key) ? map[key] : undefined
}

export function lookupResumeTemplate(
  overrides: Readonly<Record<string, ResumeTemplatePair>>,
  agentType: string,
): ResumeTemplatePair | undefined {
  return own(overrides, agentType) ?? own(DEFAULT_RESUME_TEMPLATES, agentType)
}

export function buildResumeLookup(overrides: Readonly<Record<string, ResumeTemplatePair>>): ResumeTemplateLookup {
  return (agentType) => lookupResumeTemplate(overrides, agentType)
}

const NO_OVERRIDES: Readonly<Record<string, ResumeTemplatePair>> = Object.freeze({})

export const defaultResumeLookup: ResumeTemplateLookup = buildResumeLookup(NO_OVERRIDES)

function overridesOf(hostId: string): Readonly<Record<string, ResumeTemplatePair>> {
  const entry = useHostConfigStore.getState().byHost[hostId]
  return entry?.status === 'ready' ? entry.resumeTemplates : NO_OVERRIDES
}

/**
 * Live lookup for code outside React (engine, batch planner). Reads the store
 * on every call; callers that must not see later edits resolve once and pin
 * the resulting string, as the engine does.
 */
export function resumeLookupFor(hostId: string): ResumeTemplateLookup {
  return (agentType) => lookupResumeTemplate(overridesOf(hostId), agentType)
}

/**
 * The lookup for code that RENDERS a composed command. Subscribed to that
 * host's overrides so an edit in Host › Commands › Resume repaints without a
 * remount. Not-yet-loaded hosts answer from defaults (display only).
 */
export function useResumeTemplateLookup(hostId: string): ResumeTemplateLookup {
  const overrides = useHostConfigStore((s) => {
    const entry = s.byHost[hostId]
    return entry?.status === 'ready' ? entry.resumeTemplates : NO_OVERRIDES
  })
  return useMemo(() => buildResumeLookup(overrides), [overrides])
}
