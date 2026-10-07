// #1489 — a host config row edited by hand reaches the SPA as-is (the daemon's
// GET does not re-validate). Each section must render whatever the store made
// of it without throwing, keep the good rows, and say what it hid. These run
// the REAL store's load, so the parse is the one the app uses.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { act, render, screen, fireEvent, waitFor } from '@testing-library/react'

vi.mock('../../features/workspace/lib/icon-path-cache', () => ({
  isWeightLoaded: () => true, prefetchWeight: () => Promise.resolve(), getIconPath: () => 'M0,0',
}))
vi.mock('../../lib/host-config-api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../../lib/host-config-api')>()),
  fetchHostConfig: vi.fn(),
  putHostConfig: vi.fn(),
  checkHostPath: vi.fn(async () => ({ status: 'dir', resolved: '/x' })),
}))

import type { ReactElement } from 'react'
import { ProjectsSection } from './ProjectsSection'
import { CommandsSection } from './CommandsSection'
import { QuickReplySettings } from './QuickReplySettings'
import { RelaySection } from './RelaySection'
import { ResumeTemplateSettings } from '../settings/ResumeTemplateSettings'
import { fetchHostConfig, putHostConfig } from '../../lib/host-config-api'
import { useHostStore } from '../../stores/useHostStore'
import { useHostConfigStore } from '../../stores/useHostConfigStore'
import { useI18nStore } from '../../stores/useI18nStore'

const H = 'h1'
const P1 = { id: 'p1', name: 'Purdex', slug: 'purdex', path: '~/w/purdex' }
const C1 = { id: 'c1', name: 'Claude', command: 'claude', icon: { kind: 'agent', value: 'cc-bot' } }
const Q1 = { id: 'q1', text: 'go on' }
const GOOD = {
  projects: { items: [P1], revision: 1 },
  commands: { items: [C1], revision: 1 },
  resumeTemplates: { items: {}, revision: 0 },
  quickReplies: { items: [Q1], revision: 1 },
  relay: { items: { self_solo: true, self_lead: true }, revision: 1 },
}

/** Load H through the real store with one or more collections replaced. */
async function loadWith(collections: Record<string, unknown>) {
  vi.mocked(fetchHostConfig).mockResolvedValue({ ...GOOD, ...collections })
  await act(async () => { await useHostConfigStore.getState().load(H) })
}

/** Render and let the section's own mount-time load settle inside act. */
async function show(ui: ReactElement) {
  await act(async () => { render(ui) })
}

const problem = () => screen.getByTestId('host-config-problem')

beforeEach(() => {
  vi.spyOn(console, 'warn').mockImplementation(() => {})
  vi.mocked(fetchHostConfig).mockReset()
  vi.mocked(putHostConfig).mockReset()
  useI18nStore.getState().setLocale('en')
  useHostStore.setState({
    hosts: { [H]: { id: H, name: 'mlab', ip: '1.2.3.4', port: 7860, order: 0 } },
    hostOrder: [H], runtime: { [H]: { status: 'connected' } },
  })
  useHostConfigStore.getState().forget(H)
})
afterEach(() => { vi.mocked(console.warn).mockRestore() })

describe('a malformed host config collection', () => {
  it('Projects: `{}` instead of an array reads as empty, says so, and stays editable', async () => {
    await loadWith({ projects: { items: {}, revision: 2 } })
    await show(<ProjectsSection hostId={H} />)
    expect(problem()).toHaveAttribute('data-problem', 'shape')
    expect(problem()).toHaveTextContent('The stored Projects on this host are malformed and read as empty')
    expect(screen.getByText('No projects on this host yet.')).toBeInTheDocument()
    expect(screen.getByTestId('project-add')).toBeEnabled()
  })

  it('Projects: an item missing `name` is hidden and counted; the good row stays', async () => {
    await loadWith({ projects: { items: [P1, { id: 'p2', slug: 'x', path: '/x' }], revision: 2 } })
    await show(<ProjectsSection hostId={H} />)
    expect(screen.getAllByTestId(/^project-row-/).map((r) => r.dataset.testid)).toEqual(['project-row-p1'])
    expect(problem()).toHaveTextContent('1 stored Projects item(s) on this host are malformed and are hidden')
  })

  it('Commands: a command with no icon is hidden and counted; the good row stays', async () => {
    await loadWith({ commands: { items: [{ id: 'c0', name: 'Bare', command: 'bare' }, C1], revision: 2 } })
    await show(<CommandsSection hostId={H} />)
    expect(screen.getAllByTestId(/^command-row-/).map((r) => r.dataset.testid)).toEqual(['command-row-c1'])
    expect(problem()).toHaveTextContent('1 stored Commands item(s)')
  })

  it('Quick replies: a reply whose text is a number is hidden and counted; the good row stays', async () => {
    await loadWith({ quickReplies: { items: [Q1, { id: 'q2', text: 42 }], revision: 3 } })
    await show(<QuickReplySettings hostId={H} />)
    expect(screen.getAllByTestId(/^quick-reply-row-/).map((r) => r.dataset.testid)).toEqual(['quick-reply-row-q1'])
    expect(problem()).toHaveTextContent('1 stored Quick replies item(s)')
  })

  it('Resume templates: a non-object value and a non-string field are dropped; the good override stays', async () => {
    const cc = { exact: 'cld --resume {id}', fallback: 'cld -c' }
    await loadWith({ resumeTemplates: { items: { cc, codex: 'oops', opencode: { exact: 5, fallback: 'oc -c' } }, revision: 2 } })
    await show(<ResumeTemplateSettings hostId={H} />)
    expect(screen.getByTestId('resume-template-input-cc-exact')).toHaveValue(cc.exact)
    // A dropped override answers from the defaults, as if never written.
    expect(screen.getByTestId('resume-template-input-codex-exact')).toHaveValue('codex resume {id}')
    expect(problem()).toHaveTextContent('2 stored Resume command templates item(s)')
  })

  it('Relay: an unreadable value shows both switches off (as the daemon reads it) and a toggle rewrites it', async () => {
    await loadWith({ relay: { items: { self_solo: true, bogus: true }, revision: 4 } })
    await show(<RelaySection hostId={H} />)
    expect(problem()).toHaveAttribute('data-problem', 'relay')
    expect(problem()).toHaveTextContent('the daemon refuses self relay')
    expect(screen.getByTestId('relay-self-solo').getAttribute('aria-checked')).toBe('false')
    expect(screen.getByTestId('relay-self-lead').getAttribute('aria-checked')).toBe('false')

    vi.mocked(putHostConfig).mockResolvedValue({ items: { self_solo: true, self_lead: false }, revision: 5 })
    fireEvent.click(screen.getByTestId('relay-self-solo'))
    await waitFor(() => expect(screen.queryByTestId('host-config-problem')).toBeNull())
    expect(putHostConfig).toHaveBeenCalledWith(H, 'relay', { self_solo: true, self_lead: false }, 4)
    expect(screen.getByTestId('relay-self-solo').getAttribute('aria-checked')).toBe('true')
  })

  it('Relay: a toggle keeps the stored prompt bodies (the PUT replaces the whole row)', async () => {
    const prompts = { prompt_write: 'write it', prompt_seed: 'seed it' }
    await loadWith({ relay: { items: { self_solo: true, self_lead: true, ...prompts }, revision: 6 } })
    await show(<RelaySection hostId={H} />)
    expect(screen.queryByTestId('host-config-problem')).toBeNull()

    vi.mocked(putHostConfig).mockResolvedValue({ items: { self_solo: false, self_lead: true, ...prompts }, revision: 7 })
    fireEvent.click(screen.getByTestId('relay-self-solo'))
    await waitFor(() => expect(putHostConfig).toHaveBeenCalled())
    expect(putHostConfig).toHaveBeenCalledWith(H, 'relay', { self_solo: false, self_lead: true, ...prompts }, 6)
  })

  it('Relay: a prompt body the daemon would refuse is dropped and said, so a toggle is not refused (400)', async () => {
    await loadWith({ relay: { items: { self_solo: true, prompt_write: 'ok', prompt_fix: 'see [pdx-relay fix]' }, revision: 8 } })
    await show(<RelaySection hostId={H} />)
    expect(problem()).toHaveAttribute('data-problem', 'rows')
    expect(problem()).toHaveTextContent('1 stored')
    expect(screen.getByTestId('relay-self-solo').getAttribute('aria-checked')).toBe('true')

    vi.mocked(putHostConfig).mockResolvedValue({ items: { self_solo: false, self_lead: true, prompt_write: 'ok' }, revision: 9 })
    fireEvent.click(screen.getByTestId('relay-self-solo'))
    await waitFor(() => expect(screen.queryByTestId('host-config-problem')).toBeNull())
    expect(putHostConfig).toHaveBeenCalledWith(H, 'relay', { self_solo: false, self_lead: true, prompt_write: 'ok' }, 8)
  })
})
