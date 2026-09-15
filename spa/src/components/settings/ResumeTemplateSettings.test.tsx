// spa/src/components/settings/ResumeTemplateSettings.test.tsx
//
// Task 16 — the per-agent resume command template editor (spec §4.5), per host
// since host-launcher B2 (spec §4.2).
//
// `fetch` is stubbed rather than `host-api`, because two of the five contracts
// this component is the only place to honour live in the request itself: the
// body must carry the COMMAND WORD (`cld-yolo`, not the whole template), and a
// 404 from an older daemon must surface as `unverifiable` rather than as an
// error the user has to debug.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { render, screen, fireEvent, act, waitFor } from '@testing-library/react'
import { ResumeTemplateSettings } from './ResumeTemplateSettings'
import componentSource from './ResumeTemplateSettings.tsx?raw'
import { AGENT_NAMES } from '../../lib/agent-metadata'
import { useHostStore } from '../../stores/useHostStore'
import { useI18nStore } from '../../stores/useI18nStore'
import { DEFAULT_RESUME_TEMPLATES } from '../../lib/resume-templates'
import { emptyHostConfigEntry, useHostConfigStore } from '../../stores/useHostConfigStore'
import { HostConfigConflictError, type ResumeTemplateOverrides } from '../../lib/host-config-api'
import en from '../../locales/en.json'
import zhTW from '../../locales/zh-TW.json'

const H1 = 'host-1'
const H2 = 'host-2'

function host(id: string, name: string, order: number) {
  return { id, name, ip: '100.64.0.2', port: 7860 + order, token: 'purdex_t', order }
}

/** A fetch stub whose promise is settled by the test. */
function deferred<T>() {
  let resolve!: (v: T) => void
  let reject!: (e: unknown) => void
  const promise = new Promise<T>((res, rej) => { resolve = res; reject = rej })
  return { promise, resolve, reject }
}

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
}

function input(agent: string, field: 'exact' | 'fallback') {
  return screen.getByTestId(`resume-template-input-${agent}-${field}`) as HTMLInputElement
}

function testButton(agent: string, field: 'exact' | 'fallback') {
  return screen.getByTestId(`resume-template-test-${agent}-${field}`)
}

function verdict(agent: string, field: 'exact' | 'fallback') {
  return screen.queryByTestId(`resume-template-verdict-${agent}-${field}`)
}

function lastFetchBody(): Record<string, unknown> {
  const calls = vi.mocked(globalThis.fetch).mock.calls
  const init = calls[calls.length - 1][1] as RequestInit
  return JSON.parse(String(init.body))
}

/** H1's host config, ready, with a save that applies locally like a successful PUT. */
function seedTemplates(resumeTemplates: ResumeTemplateOverrides) {
  useHostConfigStore.setState({
    byHost: { [H1]: { ...emptyHostConfigEntry('ready'), resumeTemplates } },
    saveResumeTemplates: vi.fn(async (hostId: string, items: ResumeTemplateOverrides) => {
      useHostConfigStore.setState((s) => ({ byHost: { ...s.byHost, [hostId]: { ...s.byHost[hostId], resumeTemplates: items } } }))
    }),
  })
}
const overrides = () => useHostConfigStore.getState().byHost[H1].resumeTemplates
const saveMock = () => vi.mocked(useHostConfigStore.getState().saveResumeTemplates)

beforeEach(() => {
  vi.restoreAllMocks()
  seedTemplates({})
  useHostStore.setState({
    hosts: { [H1]: host(H1, 'mlab', 0), [H2]: host(H2, 'air', 1) },
    hostOrder: [H1, H2],
    activeHostId: H1,
  })
})

describe('ResumeTemplateSettings — rows', () => {
  it('renders a row pair per AGENT_NAMES agent, pre-filled with the defaults', () => {
    render(<ResumeTemplateSettings hostId={H1} />)
    for (const [agent, pair] of Object.entries(DEFAULT_RESUME_TEMPLATES)) {
      expect(input(agent, 'exact').value).toBe(pair.exact)
      expect(input(agent, 'fallback').value).toBe(pair.fallback)
    }
    expect(screen.getByTestId('resume-template-agent-cc').textContent).toContain('Claude Code')
  })

  it('Enter commits the edit to the store, and the trailing blur does not commit twice', async () => {
    render(<ResumeTemplateSettings hostId={H1} />)
    const el = input('cc', 'exact')
    fireEvent.change(el, { target: { value: 'cld-yolo --resume {id}' } })
    fireEvent.keyDown(el, { key: 'Enter' })
    fireEvent.blur(el)

    expect(saveMock()).toHaveBeenCalledTimes(1)
    await waitFor(() => expect(overrides().cc?.exact).toBe('cld-yolo --resume {id}'))
  })

  it('blur alone commits', async () => {
    render(<ResumeTemplateSettings hostId={H1} />)
    const el = input('codex', 'fallback')
    fireEvent.change(el, { target: { value: 'codex resume --last --yolo' } })
    fireEvent.blur(el)
    await waitFor(() => expect(overrides().codex?.fallback).toBe('codex resume --last --yolo'))
  })

  it('an IME Enter does not commit — the keystroke belongs to the candidate', async () => {
    render(<ResumeTemplateSettings hostId={H1} />)
    const el = input('cc', 'exact')
    fireEvent.compositionStart(el)
    fireEvent.change(el, { target: { value: '重新開始 {id}' } })
    fireEvent.keyDown(el, { key: 'Enter', isComposing: true })
    expect(overrides().cc).toBeUndefined()

    fireEvent.compositionEnd(el)
    fireEvent.keyDown(el, { key: 'Enter' })
    await waitFor(() => expect(overrides().cc?.exact).toBe('重新開始 {id}'))
  })

  it('an Enter carrying isComposing does not commit, even without a compositionstart', () => {
    // The other half of the guard: some IMEs fire the keydown with
    // `isComposing` set and no composition event we saw first.
    render(<ResumeTemplateSettings hostId={H1} />)
    const el = input('cc', 'exact')
    fireEvent.change(el, { target: { value: 'half {id}' } })
    fireEvent.keyDown(el, { key: 'Enter', isComposing: true })
    expect(overrides().cc).toBeUndefined()
  })

  it('Escape reverts the row and commits nothing', () => {
    render(<ResumeTemplateSettings hostId={H1} />)
    const el = input('cc', 'exact')
    fireEvent.change(el, { target: { value: 'nonsense' } })
    fireEvent.keyDown(el, { key: 'Escape' })
    expect(overrides().cc).toBeUndefined()
    expect(saveMock()).not.toHaveBeenCalled()
    expect(input('cc', 'exact').value).toBe(DEFAULT_RESUME_TEMPLATES.cc.exact)
  })

  it('`busy` disables every input and every Test button', () => {
    // Every row, not just the first: `busy` is a panel-level prop, and a row
    // left editable under it would be overwritten by the action's result.
    render(<ResumeTemplateSettings hostId={H1} busy />)
    for (const agent of Object.keys(AGENT_NAMES)) {
      for (const field of ['exact', 'fallback'] as const) {
        expect(input(agent, field).disabled, `input ${agent}/${field}`).toBe(true)
        expect(testButton(agent, field), `test button ${agent}/${field}`).toBeDisabled()
      }
    }
  })

  it('Reset all drops every customisation and repaints the defaults', async () => {
    seedTemplates({ cc: { exact: 'x {id}', fallback: 'y' } })
    render(<ResumeTemplateSettings hostId={H1} />)
    expect(input('cc', 'exact').value).toBe('x {id}')

    fireEvent.click(screen.getByTestId('resume-template-reset'))
    await waitFor(() => expect(overrides()).toEqual({}))
    expect(input('cc', 'exact').value).toBe(DEFAULT_RESUME_TEMPLATES.cc.exact)
  })
})

describe('ResumeTemplateSettings — warnings never block the save', () => {
  it('an `exact` without {id} warns and still saves', async () => {
    render(<ResumeTemplateSettings hostId={H1} />)
    const el = input('cc', 'exact')
    fireEvent.change(el, { target: { value: 'claude -c' } })
    fireEvent.keyDown(el, { key: 'Enter' })

    expect(screen.getByTestId('resume-template-warning-cc-exact')).toBeTruthy()
    await waitFor(() => expect(overrides().cc?.exact).toBe('claude -c'))
  })

  it('a `fallback` with {id} warns and still saves', async () => {
    render(<ResumeTemplateSettings hostId={H1} />)
    const el = input('cc', 'fallback')
    fireEvent.change(el, { target: { value: 'claude --resume {id}' } })
    fireEvent.keyDown(el, { key: 'Enter' })

    expect(screen.getByTestId('resume-template-warning-cc-fallback')).toBeTruthy()
    await waitFor(() => expect(overrides().cc?.fallback).toBe('claude --resume {id}'))
  })

  it('the defaults raise no warning', () => {
    render(<ResumeTemplateSettings hostId={H1} />)
    expect(screen.queryByTestId('resume-template-warning-cc-exact')).toBeNull()
    expect(screen.queryByTestId('resume-template-warning-cc-fallback')).toBeNull()
  })
})

describe('ResumeTemplateSettings — the probe', () => {
  it('POSTs only the command word, with {id} left unsubstituted', async () => {
    const fetchSpy = vi.spyOn(globalThis, 'fetch')
      .mockResolvedValue(jsonResponse({ resolved: true, detail: '/Users/wake/.local/bin/cld-yolo' }))
    render(<ResumeTemplateSettings hostId={H1} />)
    const el = input('cc', 'exact')
    fireEvent.change(el, { target: { value: 'cld-yolo --resume {id}' } })
    fireEvent.keyDown(el, { key: 'Enter' })

    await act(async () => { fireEvent.click(testButton('cc', 'exact')) })

    const [url, init] = fetchSpy.mock.calls[0] as [string, RequestInit]
    expect(url).toBe('http://100.64.0.2:7860/api/shell/resolve-command')
    expect(init.method).toBe('POST')
    expect(lastFetchBody()).toEqual({ command: 'cld-yolo' })
  })

  it('skips the leading variable assignments a template may carry', async () => {
    const fetchSpy = vi.spyOn(globalThis, 'fetch')
      .mockResolvedValue(jsonResponse({ resolved: true, detail: '/opt/homebrew/bin/opencode' }))
    render(<ResumeTemplateSettings hostId={H1} />)
    const el = input('opencode', 'exact')
    fireEvent.change(el, { target: { value: 'OPENCODE_YOLO=true opencode -s {id}' } })
    fireEvent.keyDown(el, { key: 'Enter' })

    await act(async () => { fireEvent.click(testButton('opencode', 'exact')) })

    expect(lastFetchBody()).toEqual({ command: 'opencode' })
    expect(fetchSpy).toHaveBeenCalledTimes(1)
  })

  it('skips several assignments, and one whose value itself contains =', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(jsonResponse({ resolved: true, detail: 'x' }))
    render(<ResumeTemplateSettings hostId={H1} />)
    fireEvent.change(input('opencode', 'exact'), {
      target: { value: 'A=1 B=k=v opencode -s {id}' },
    })
    await act(async () => { fireEvent.click(testButton('opencode', 'exact')) })
    expect(lastFetchBody()).toEqual({ command: 'opencode' })
  })

  it('does not mistake a flag or a path for an assignment', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(jsonResponse({ resolved: true, detail: 'x' }))
    render(<ResumeTemplateSettings hostId={H1} />)
    fireEvent.change(input('cc', 'exact'), { target: { value: '/usr/local/bin/cld --resume {id}' } })
    await act(async () => { fireEvent.click(testButton('cc', 'exact')) })
    expect(lastFetchBody()).toEqual({ command: '/usr/local/bin/cld' })
  })

  it('disables Test when a template is nothing but assignments', () => {
    render(<ResumeTemplateSettings hostId={H1} />)
    fireEvent.change(input('cc', 'exact'), { target: { value: 'FOO=1 BAR=2' } })
    expect(testButton('cc', 'exact')).toBeDisabled()
  })

  it('probes the uncommitted draft word too, so Test judges what is on screen', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(jsonResponse({ resolved: true, detail: 'x' }))
    render(<ResumeTemplateSettings hostId={H1} />)
    fireEvent.change(input('cc', 'exact'), { target: { value: 'wrapper --resume {id}' } })
    await act(async () => { fireEvent.click(testButton('cc', 'exact')) })
    expect(lastFetchBody()).toEqual({ command: 'wrapper' })
  })

  it('renders a resolved verdict with the detail the daemon printed', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(jsonResponse({ resolved: true, detail: "alias cld='cld-yolo'" }))
    render(<ResumeTemplateSettings hostId={H1} />)
    await act(async () => { fireEvent.click(testButton('cc', 'exact')) })

    const el = verdict('cc', 'exact')!
    expect(el.getAttribute('data-status')).toBe('resolved')
    expect(el.textContent).toContain("alias cld='cld-yolo'")
  })

  it.each(['not_found', 'shell_metacharacters', 'too_long', 'timeout', 'shell_failed'])(
    'renders the %s verdict',
    async (reason) => {
      vi.spyOn(globalThis, 'fetch').mockResolvedValue(jsonResponse({ resolved: false, reason }))
      render(<ResumeTemplateSettings hostId={H1} />)
      await act(async () => { fireEvent.click(testButton('cc', 'exact')) })

      const el = verdict('cc', 'exact')!
      expect(el.getAttribute('data-status')).toBe('unresolved')
      expect(el.getAttribute('data-reason')).toBe(reason)
      expect(el.textContent).toBe(en[`resume_template.verdict.${reason}` as keyof typeof en])
    },
  )

  it('a 404 from an older daemon renders as unverifiable, and the template stays saved', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(new Response('not found', { status: 404 }))
    render(<ResumeTemplateSettings hostId={H1} />)
    const el = input('cc', 'exact')
    fireEvent.change(el, { target: { value: 'cld-yolo --resume {id}' } })
    fireEvent.keyDown(el, { key: 'Enter' })

    await act(async () => { fireEvent.click(testButton('cc', 'exact')) })

    expect(verdict('cc', 'exact')!.getAttribute('data-status')).toBe('unverifiable')
    await waitFor(() => expect(overrides().cc?.exact).toBe('cld-yolo --resume {id}'))
  })

  it('a network rejection renders as unverifiable, and the template stays saved', async () => {
    vi.spyOn(globalThis, 'fetch').mockRejectedValue(new TypeError('Failed to fetch'))
    render(<ResumeTemplateSettings hostId={H1} />)
    const el = input('cc', 'fallback')
    fireEvent.change(el, { target: { value: 'cld-yolo -c' } })
    fireEvent.keyDown(el, { key: 'Enter' })

    await act(async () => { fireEvent.click(testButton('cc', 'fallback')) })

    expect(verdict('cc', 'fallback')!.getAttribute('data-status')).toBe('unverifiable')
    await waitFor(() => expect(overrides().cc?.fallback).toBe('cld-yolo -c'))
  })
})

describe('ResumeTemplateSettings — request revisions', () => {
  it('a response that lands after the row was edited is discarded', async () => {
    const d = deferred<Response>()
    vi.spyOn(globalThis, 'fetch').mockReturnValue(d.promise)
    render(<ResumeTemplateSettings hostId={H1} />)
    await act(async () => { fireEvent.click(testButton('cc', 'exact')) })

    fireEvent.change(input('cc', 'exact'), { target: { value: 'cld-yolo --resume {id}' } })
    await act(async () => {
      d.resolve(jsonResponse({ resolved: true, detail: '/usr/bin/claude' }))
      await d.promise
    })

    expect(verdict('cc', 'exact')).toBeNull()
  })

  it('a verdict from a superseded request never overwrites a newer one', async () => {
    // Away and back leaves the word EXACTLY as the first request sent it, so the
    // (host, word) pair cannot tell the two requests apart. Only the request's
    // own identity can. (A pending row's Test button is disabled, so the second
    // Test is reached through the edit that abandons the first request.)
    const first = deferred<Response>()
    const second = deferred<Response>()
    vi.spyOn(globalThis, 'fetch')
      .mockReturnValueOnce(first.promise)
      .mockReturnValueOnce(second.promise)
    render(<ResumeTemplateSettings hostId={H1} />)
    const original = input('cc', 'exact').value
    await act(async () => { fireEvent.click(testButton('cc', 'exact')) })

    fireEvent.change(input('cc', 'exact'), { target: { value: 'other --resume {id}' } })
    fireEvent.change(input('cc', 'exact'), { target: { value: original } })
    await act(async () => { fireEvent.click(testButton('cc', 'exact')) })

    // Reverse order: the newer answer lands first, the abandoned one after.
    await act(async () => {
      second.resolve(jsonResponse({ resolved: false, reason: 'not_found' }))
      await second.promise
    })
    await act(async () => {
      first.resolve(jsonResponse({ resolved: true, detail: 'from the abandoned request' }))
      await first.promise
    })

    const el = verdict('cc', 'exact')!
    expect(el.getAttribute('data-status')).toBe('unresolved')
    expect(el.textContent).not.toContain('abandoned')
  })

  it('editing a row cancels its in-flight request, even if the word is retyped', async () => {
    // The stated contract is "editing the row invalidates its verdict —
    // including one still in flight". Retyping the original value restores the
    // word, but not the request the user abandoned by editing.
    const d = deferred<Response>()
    vi.spyOn(globalThis, 'fetch').mockReturnValue(d.promise)
    render(<ResumeTemplateSettings hostId={H1} />)
    const original = input('cc', 'exact').value
    await act(async () => { fireEvent.click(testButton('cc', 'exact')) })

    fireEvent.change(input('cc', 'exact'), { target: { value: 'other --resume {id}' } })
    fireEvent.change(input('cc', 'exact'), { target: { value: original } })
    await act(async () => {
      d.resolve(jsonResponse({ resolved: true, detail: '/usr/bin/claude' }))
      await d.promise
    })

    expect(verdict('cc', 'exact')).toBeNull()
  })

  it('a verdict for one row does not leak onto the other row of the same agent', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(jsonResponse({ resolved: true, detail: 'x' }))
    render(<ResumeTemplateSettings hostId={H1} />)
    await act(async () => { fireEvent.click(testButton('cc', 'exact')) })
    expect(verdict('cc', 'exact')).toBeTruthy()
    expect(verdict('cc', 'fallback')).toBeNull()
  })

  it('a pending row disables its own Test button until the verdict lands', async () => {
    const d = deferred<Response>()
    vi.spyOn(globalThis, 'fetch').mockReturnValue(d.promise)
    render(<ResumeTemplateSettings hostId={H1} />)
    await act(async () => { fireEvent.click(testButton('cc', 'exact')) })

    expect(testButton('cc', 'exact')).toBeDisabled()
    expect(testButton('cc', 'fallback')).not.toBeDisabled()

    await act(async () => { d.resolve(jsonResponse({ resolved: true, detail: 'x' })); await d.promise })
    await waitFor(() => expect(testButton('cc', 'exact')).not.toBeDisabled())
  })
})

describe('ResumeTemplateSettings — a draft is uncommitted state only', () => {
  it('a committed row follows a later store change instead of pinning the saved value', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(jsonResponse({ resolved: true, detail: 'x' }))
    render(<ResumeTemplateSettings hostId={H1} />)
    const el = input('cc', 'exact')
    fireEvent.change(el, { target: { value: 'cld-yolo --resume {id}' } })
    fireEvent.keyDown(el, { key: 'Enter' })
    await waitFor(() => expect(input('cc', 'exact').value).toBe('cld-yolo --resume {id}'))

    // Another client saves the same row, and a reload lands it here.
    act(() => {
      seedTemplates({ cc: { exact: 'other-wrapper --resume {id}', fallback: DEFAULT_RESUME_TEMPLATES.cc.fallback } })
    })

    // A committed row has nothing left to protect: the store is the value.
    expect(input('cc', 'exact').value).toBe('other-wrapper --resume {id}')
    // And Test judges what a rebuild would actually run, not what this window
    // last typed.
    await act(async () => { fireEvent.click(testButton('cc', 'exact')) })
    expect(lastFetchBody()).toEqual({ command: 'other-wrapper' })
  })

  it('a committed row follows a reset performed elsewhere', async () => {
    render(<ResumeTemplateSettings hostId={H1} />)
    const el = input('cc', 'fallback')
    fireEvent.change(el, { target: { value: 'cld-yolo -c' } })
    fireEvent.blur(el)
    await waitFor(() => expect(input('cc', 'fallback').value).toBe('cld-yolo -c'))

    act(() => { seedTemplates({}) })
    expect(input('cc', 'fallback').value).toBe(DEFAULT_RESUME_TEMPLATES.cc.fallback)
  })

  it('an edit that has NOT been committed still wins over a store change', () => {
    // The other side of the same rule: a draft exists to protect what the user
    // is still typing, and only that.
    render(<ResumeTemplateSettings hostId={H1} />)
    fireEvent.change(input('cc', 'exact'), { target: { value: 'half-typed' } })

    act(() => {
      seedTemplates({ cc: { exact: 'from-elsewhere {id}', fallback: DEFAULT_RESUME_TEMPLATES.cc.fallback } })
    })
    expect(input('cc', 'exact').value).toBe('half-typed')
  })
})

describe('ResumeTemplateSettings — host scoped', () => {
  it('shows this host\'s overrides, not another host\'s', () => {
    useHostConfigStore.setState({ byHost: {
      [H1]: { ...emptyHostConfigEntry('ready'), resumeTemplates: { cc: { exact: 'one --resume {id}', fallback: 'one -c' } } },
      [H2]: { ...emptyHostConfigEntry('ready'), resumeTemplates: { cc: { exact: 'two --resume {id}', fallback: 'two -c' } } },
    } })
    render(<ResumeTemplateSettings hostId={H2} />)
    expect(input('cc', 'exact').value).toBe('two --resume {id}')
    expect(screen.queryByTestId('resume-template-host')).toBeNull()
  })

  it('a commit saves the whole sparse map with the edited field merged onto the current pair', async () => {
    seedTemplates({ codex: { exact: 'cx {id}', fallback: 'cx' } })
    render(<ResumeTemplateSettings hostId={H1} />)
    fireEvent.change(input('cc', 'fallback'), { target: { value: 'cld -c' } })
    fireEvent.blur(input('cc', 'fallback'))
    await waitFor(() => expect(saveMock()).toHaveBeenCalledWith(H1, {
      codex: { exact: 'cx {id}', fallback: 'cx' },
      cc: { exact: DEFAULT_RESUME_TEMPLATES.cc.exact, fallback: 'cld -c' },
    }))
  })

  it('Test probes THIS host', async () => {
    useHostConfigStore.setState((s) => ({ byHost: { ...s.byHost, [H2]: emptyHostConfigEntry('ready') } }))
    const fetchSpy = vi.spyOn(globalThis, 'fetch').mockResolvedValue(jsonResponse({ resolved: true, detail: 'x' }))
    render(<ResumeTemplateSettings hostId={H2} />)
    fireEvent.click(testButton('cc', 'exact'))
    await waitFor(() => expect(fetchSpy).toHaveBeenCalled())
    expect(String(fetchSpy.mock.calls[0][0])).toContain(':7861/api/shell/resolve-command')
  })

  it('Reset all saves an empty map', async () => {
    seedTemplates({ cc: { exact: 'x {id}', fallback: 'x' } })
    render(<ResumeTemplateSettings hostId={H1} />)
    fireEvent.click(screen.getByTestId('resume-template-reset'))
    await waitFor(() => expect(saveMock()).toHaveBeenCalledWith(H1, {}))
  })

  it('a conflict shows the changed-elsewhere notice and the reloaded values', async () => {
    seedTemplates({})
    useHostConfigStore.setState({ saveResumeTemplates: vi.fn(async () => {
      useHostConfigStore.setState((s) => ({ byHost: { ...s.byHost, [H1]: { ...s.byHost[H1], resumeTemplates: { cc: { exact: 'server {id}', fallback: 'server' } } } } }))
      throw new HostConfigConflictError({ items: {}, revision: 5 })
    }) })
    render(<ResumeTemplateSettings hostId={H1} />)
    fireEvent.change(input('cc', 'exact'), { target: { value: 'mine {id}' } })
    fireEvent.blur(input('cc', 'exact'))
    expect(await screen.findByTestId('resume-template-save-error')).toHaveTextContent('Changed elsewhere')
    expect(input('cc', 'exact').value).toBe('server {id}')
  })

  it('a host whose config is not ready renders defaults read-only', () => {
    useHostConfigStore.setState({ byHost: { [H1]: emptyHostConfigEntry('unsupported') } })
    render(<ResumeTemplateSettings hostId={H1} />)
    expect(input('cc', 'exact').value).toBe(DEFAULT_RESUME_TEMPLATES.cc.exact)
    expect(input('cc', 'exact')).toBeDisabled()
  })
})

describe('ResumeTemplateSettings — i18n and the limits copy', () => {
  const originalT = useI18nStore.getState().t
  afterEach(() => { useI18nStore.setState({ t: originalT }) })

  it('states both limits: templates are this host\'s, and the test only approximates the pane', () => {
    render(<ResumeTemplateSettings hostId={H1} />)
    const limits = screen.getByTestId('resume-template-limits').textContent ?? ''
    expect(limits).toContain(en['resume_template.limit_host'])
    expect(limits).toContain(en['resume_template.limit_probe'])
  })

  it('every key the component uses exists in BOTH en and zh-TW', () => {
    const keys = [...componentSource.matchAll(/'((?:resume_template|host_config)\.[a-z_.]+)'/g)].map((m) => m[1])
    expect(keys.length).toBeGreaterThan(8)
    for (const key of new Set(keys)) {
      expect(en, `en.json missing ${key}`).toHaveProperty(key)
      expect(zhTW, `zh-TW.json missing ${key}`).toHaveProperty(key)
    }
  })

  it('renders no literal English — every string is a translation or an agent name', () => {
    // With `t` echoing its key, anything left that is not a known dynamic value
    // is hardcoded copy.
    useI18nStore.setState({ t: (key: string) => `«${key}»` })
    const { container } = render(<ResumeTemplateSettings hostId={H1} />)

    const allowed = new Set(['Claude Code', 'Codex', 'OpenCode'])
    const walker = document.createTreeWalker(container, NodeFilter.SHOW_TEXT)
    const offenders: string[] = []
    for (let n = walker.nextNode(); n; n = walker.nextNode()) {
      const text = (n.textContent ?? '').trim()
      if (!text) continue
      if (text.startsWith('«') || allowed.has(text)) continue
      offenders.push(text)
    }
    expect(offenders).toEqual([])
  })
})
