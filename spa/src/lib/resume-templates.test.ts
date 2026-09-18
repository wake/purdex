import { describe, it, expect, beforeEach } from 'vitest'
import { renderHook, act } from '@testing-library/react'
import {
  buildResumeLookup,
  DEFAULT_RESUME_TEMPLATES,
  defaultResumeLookup,
  resumeLookupFor,
  resumeTemplateFor,
  useResumeTemplateLookup,
} from './resume-templates'
import { emptyHostConfigEntry, useHostConfigStore } from '../stores/useHostConfigStore'

function ready(resumeTemplates: Record<string, { exact: string; fallback: string }>) {
  return { ...emptyHostConfigEntry('ready'), resumeTemplates }
}

beforeEach(() => useHostConfigStore.setState({ byHost: {} }))

describe('resume template lookups', () => {
  it('defaults are frozen and match the shipped shapes', () => {
    expect(DEFAULT_RESUME_TEMPLATES.cc).toEqual({ exact: 'claude --resume {id}', fallback: 'claude -c' })
    expect(Object.isFrozen(DEFAULT_RESUME_TEMPLATES)).toBe(true)
    expect(Object.isFrozen(DEFAULT_RESUME_TEMPLATES.cc)).toBe(true)
  })

  it('a sparse override wins for its agent only; own properties only', () => {
    const lookup = buildResumeLookup({ cc: { exact: 'cld --resume {id}', fallback: 'cld -c' } })
    expect(lookup('cc')?.exact).toBe('cld --resume {id}')
    expect(lookup('codex')).toEqual(DEFAULT_RESUME_TEMPLATES.codex)
    expect(lookup('constructor')).toBeUndefined()
    expect(defaultResumeLookup('aider')).toBeUndefined()
  })

  it('resumeLookupFor answers from defaults until the host is ready', () => {
    useHostConfigStore.setState({ byHost: { h1: { ...ready({ cc: { exact: 'a {id}', fallback: 'a' } }), status: 'loading' } } })
    expect(resumeLookupFor('h1')('cc')).toEqual(DEFAULT_RESUME_TEMPLATES.cc)
    useHostConfigStore.setState({ byHost: { h1: ready({ cc: { exact: 'a {id}', fallback: 'a' } }) } })
    expect(resumeLookupFor('h1')('cc')?.exact).toBe('a {id}')
    expect(resumeLookupFor('h2')('cc')).toEqual(DEFAULT_RESUME_TEMPLATES.cc)
  })

  it('two hosts with different overrides for the same agent stay apart', () => {
    useHostConfigStore.setState({ byHost: {
      h1: ready({ cc: { exact: 'one {id}', fallback: 'one' } }),
      h2: ready({ cc: { exact: 'two {id}', fallback: 'two' } }),
    } })
    expect(resumeLookupFor('h1')('cc')?.exact).toBe('one {id}')
    expect(resumeLookupFor('h2')('cc')?.exact).toBe('two {id}')
  })

  it('useResumeTemplateLookup re-renders when that host\'s templates change', () => {
    const { result } = renderHook(() => useResumeTemplateLookup('h1'))
    expect(result.current('cc')).toEqual(DEFAULT_RESUME_TEMPLATES.cc)
    act(() => useHostConfigStore.setState({ byHost: { h1: ready({ cc: { exact: 'x {id}', fallback: 'x' } }) } }))
    expect(result.current('cc')?.exact).toBe('x {id}')
  })
})

describe('resumeTemplateFor', () => {
  it('returns the exact template with {id} intact for cc', () => {
    expect(resumeTemplateFor(defaultResumeLookup, 'cc')).toBe('claude --resume {id}')
  })

  it('returns undefined for an unknown agent', () => {
    expect(resumeTemplateFor(defaultResumeLookup, 'aider')).toBeUndefined()
    expect(resumeTemplateFor(defaultResumeLookup, 'constructor')).toBeUndefined()
  })

  it('honours a host override lookup', () => {
    useHostConfigStore.setState({ byHost: { h1: ready({ cc: { exact: 'cld-yolo --resume {id}', fallback: 'cld-yolo -c' } }) } })
    expect(resumeTemplateFor(resumeLookupFor('h1'), 'cc')).toBe('cld-yolo --resume {id}')
    expect(resumeTemplateFor(resumeLookupFor('h2'), 'cc')).toBe('claude --resume {id}')
  })
})
