import { describe, it, expect, beforeEach } from 'vitest'
import { paneDisplayLabelNow } from './pane-display-label'
import { useSessionStore } from '../stores/useSessionStore'
import { useI18nStore } from '../stores/useI18nStore'
import type { Session } from './host-api'
import type { PaneContent } from '../types/tab'

const t = useI18nStore.getState().t

const terminal = (hostId: string, code: string, cachedName = ''): PaneContent => ({
  kind: 'tmux-session', hostId, sessionCode: code, mode: 'terminal', cachedName, tmuxInstance: '',
})
const session = (code: string, name: string) => ({ code, name, cwd: '/', mode: 'terminal' }) as Session

beforeEach(() => {
  useSessionStore.setState({ sessions: {} })
})

describe('paneDisplayLabelNow', () => {
  it('a terminal shows its session name, looked up on the pane\'s own host', () => {
    useSessionStore.setState({ sessions: { h1: [session('abc', 'api-server')], h2: [session('abc', 'other-host')] } })
    expect(paneDisplayLabelNow(terminal('h1', 'abc'), t)).toBe('api-server')
    expect(paneDisplayLabelNow(terminal('h2', 'abc'), t)).toBe('other-host')
  })

  it('a terminal whose session is not listed falls back to its cached name', () => {
    useSessionStore.setState({ sessions: { h2: [session('abc', 'other-host')] } })
    expect(paneDisplayLabelNow(terminal('h1', 'abc', 'cached'), t)).toBe('cached')
  })

  it('a worker shows its worker title, so two workers can be told apart', () => {
    const a: PaneContent = { kind: 'execution', executionId: 'exc_1', host: 'h1', fromTitle: 'fix login' }
    const b: PaneContent = { kind: 'execution', executionId: 'exc_2', host: 'h1', fromTitle: 'write docs' }
    expect(paneDisplayLabelNow(a, t)).toBe('fix login')
    expect(paneDisplayLabelNow(b, t)).toBe('write docs')
  })

  it('a worker with nothing to title it falls back to the kind label', () => {
    expect(paneDisplayLabelNow({ kind: 'execution', executionId: 'exc_9', host: 'h1' }, t)).toBe(t('page.pane.execution'))
  })

  it('an editor shows its file name', () => {
    expect(paneDisplayLabelNow({ kind: 'editor', source: { type: 'inapp' }, filePath: '/src/a.md' }, t)).toBe('a.md')
  })
})
