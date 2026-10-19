import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor, cleanup, act } from '@testing-library/react'

vi.mock('../../../lib/names/persona-save', async (orig) => ({ ...(await orig<typeof import('../../../lib/names/persona-save')>()), savePersonas: vi.fn() }))

import { savePersonas, PersonaStaleError, PersonaValidationError } from '../../../lib/names/persona-save'
import { PersonaApiError, PersonaConflictError } from '../../../lib/names/persona-api'
import { usePersonaStore } from '../../../stores/usePersonaStore'
import { useNexHostStore } from '../../../stores/useNexHostStore'
import { useHostStore } from '../../../stores/useHostStore'
import { usePersonaSourceStore } from '../../../stores/usePersonaSourceStore'
import { clearDraftMemory } from '../../../lib/names/persona-draft-memory'
import { makeCollection, makePersona } from '../../../lib/names/__fixtures__/personas'
import { PersonasSection } from './PersonasSection'

const NO_MIRROR = { mirrored: [], failed: [], pulled: [] }

function seed(codes: string[], over: Record<string, Partial<ReturnType<typeof makePersona>>> = {}) {
  const c = makeCollection(codes, { at: 5, host: 'h1' }, 4)
  c.items = c.items.map((p) => makePersona(p.code, over[p.code] ?? {}))
  usePersonaStore.setState({ byHost: { h1: { status: 'ready', collection: c } } })
}

describe('PersonasSection', () => {
  beforeEach(() => {
    cleanup()
    clearDraftMemory()
    vi.mocked(savePersonas).mockReset()
    useHostStore.setState({ hosts: { h1: { id: 'h1', name: 'mlab', ip: '1.2.3.4', port: 7860 } as never }, hostOrder: ['h1'], runtime: { h1: { status: 'connected' } as never } })
    useNexHostStore.setState({ byHost: { h1: { phase: 'ready', daemonCapabilities: ['personas.v1'], fetchedAt: 1 } as never } })
    usePersonaStore.setState({ byHost: {}, load: vi.fn(async () => {}) })
    usePersonaSourceStore.setState({ hostId: null })
  })

  it('lists active personas first, retired last and dimmed, with the lead mark', () => {
    seed(['hiro', 'emma', 'olga'], { hiro: { retired: true }, emma: { lead: true } })
    render(<PersonasSection />)
    const rows = screen.getAllByTestId(/^persona-row-/).map((r) => r.getAttribute('data-testid'))
    expect(rows).toEqual(['persona-row-emma', 'persona-row-olga', 'persona-row-hiro'])
    expect(screen.getByTestId('persona-row-hiro').className).toMatch(/opacity-45/)
    expect(screen.getByTestId('persona-row-emma').querySelector('[aria-label]')).not.toBeNull()
  })

  it('has no delete button anywhere', () => {
    seed(['hiro'])
    render(<PersonasSection />)
    fireEvent.click(screen.getByTestId('persona-row-hiro'))
    expect(screen.queryByText(/刪除|delete/i)).toBeNull()
    expect(screen.getByTestId('persona-retire')).toBeTruthy()
  })

  it('says when no host supports personas, and offers no way to create one', () => {
    useNexHostStore.setState({ byHost: { h1: { phase: 'ready', daemonCapabilities: [], fetchedAt: 1 } as never } })
    usePersonaStore.setState({ byHost: {} })
    render(<PersonasSection />)
    expect(screen.getByTestId('personas-unsupported')).toBeTruthy()
    expect((screen.getByTestId('persona-new') as HTMLButtonElement).disabled).toBe(true)
  })

  it('does not offer editing while the source is offline and another copy stands in', () => {
    useHostStore.setState({
      hosts: { h1: { id: 'h1' } as never, h2: { id: 'h2' } as never }, hostOrder: ['h1', 'h2'],
      runtime: { h1: { status: 'disconnected' } as never, h2: { status: 'connected' } as never },
    })
    usePersonaSourceStore.setState({ hostId: 'h1' })
    usePersonaStore.setState({ byHost: { h2: { status: 'ready', collection: makeCollection(['hiro'], { at: 5, host: 'h2' }, 4) } } })
    render(<PersonasSection />)
    expect(screen.getByTestId('persona-row-hiro')).toBeTruthy()
    expect((screen.getByTestId('persona-new') as HTMLButtonElement).disabled).toBe(true)
    fireEvent.click(screen.getByTestId('persona-row-hiro'))
    expect(screen.queryByTestId('persona-save')).toBeNull()
  })

  it('takes an open form away when the source drops, instead of leaving it submittable', () => {
    useHostStore.setState({
      hosts: { h1: { id: 'h1' } as never, h2: { id: 'h2' } as never }, hostOrder: ['h1', 'h2'],
      runtime: { h1: { status: 'connected' } as never, h2: { status: 'connected' } as never },
    })
    usePersonaSourceStore.setState({ hostId: 'h1' })
    const copy = makeCollection(['hiro'], { at: 5, host: 'h1' }, 4)
    usePersonaStore.setState({ byHost: { h1: { status: 'ready', collection: copy }, h2: { status: 'ready', collection: copy } } })
    render(<PersonasSection />)
    fireEvent.click(screen.getByTestId('persona-row-hiro'))
    expect(screen.getByTestId('persona-save')).toBeTruthy()
    act(() => { usePersonaStore.setState({ byHost: { h1: { status: 'error', collection: copy }, h2: { status: 'ready', collection: copy } } }) })
    expect(screen.queryByTestId('persona-save')).toBeNull()
  })

  it('shows counters against the limits while typing', () => {
    seed(['hiro'])
    render(<PersonasSection />)
    fireEvent.click(screen.getByTestId('persona-row-hiro'))
    const name = screen.getByTestId('persona-name').querySelector('input')!
    fireEvent.change(name, { target: { value: 'Hiro Tanaka' } })
    expect(screen.getByTestId('persona-name-counter').textContent).toBe('11/24')
    const zh = screen.getByTestId('persona-personality-zh').querySelector('textarea')!
    fireEvent.change(zh, { target: { value: '冷靜又細心' } })
    expect(screen.getByTestId('persona-personality-zh-counter').textContent).toBe('5/50')
    const en = screen.getByTestId('persona-personality-en').querySelector('textarea')!
    fireEvent.change(en, { target: { value: 'calm and careful' } })
    expect(screen.getByTestId('persona-personality-en-counter').textContent).toBe('3/50')
  })

  it('saves the edited collection on the base the form opened with, and says where it went', async () => {
    seed(['hiro', 'emma'])
    vi.mocked(savePersonas).mockResolvedValue({ hostId: 'h1', mirror: NO_MIRROR, base: { hostId: 'h1', revision: 5, stamp: { at: 9, host: 'h1' } } })
    render(<PersonasSection />)
    fireEvent.click(screen.getByTestId('persona-row-hiro'))
    fireEvent.change(screen.getByTestId('persona-name').querySelector('input')!, { target: { value: 'Hiro Two' } })
    fireEvent.click(screen.getByTestId('persona-save'))
    await waitFor(() => expect(savePersonas).toHaveBeenCalledTimes(1))
    const [items, base, fallback] = vi.mocked(savePersonas).mock.calls[0]
    expect(items.map((p) => [p.code, p.name])).toEqual([['hiro', 'Hiro Two'], ['emma', 'Emma']])
    expect(base).toEqual({ hostId: 'h1', revision: 4, stamp: { at: 5, host: 'h1' } })
    expect(fallback).toBe('h1')
    expect((await screen.findByTestId('personas-note')).textContent).toContain('mlab')
  })

  it('refuses a bad code before any request and points at the field', async () => {
    seed([])
    render(<PersonasSection />)
    fireEvent.click(screen.getByTestId('persona-new'))
    fireEvent.change(screen.getByTestId('persona-code').querySelector('input')!, { target: { value: 'Bad1' } })
    fireEvent.change(screen.getByTestId('persona-name').querySelector('input')!, { target: { value: 'Bad' } })
    fireEvent.click(screen.getByTestId('persona-save'))
    expect(savePersonas).not.toHaveBeenCalled()
    expect(screen.getByTestId('persona-code').textContent).toMatch(/3-12|3–12/)
  })

  it('retires and restores with a save, never a delete', async () => {
    seed(['hiro'])
    vi.mocked(savePersonas).mockResolvedValue({ hostId: 'h1', mirror: NO_MIRROR, base: null })
    render(<PersonasSection />)
    fireEvent.click(screen.getByTestId('persona-row-hiro'))
    fireEvent.click(screen.getByTestId('persona-retire'))
    await waitFor(() => expect(savePersonas).toHaveBeenCalledTimes(1))
    expect(vi.mocked(savePersonas).mock.calls[0][0][0]).toMatchObject({ code: 'hiro', retired: true })
  })

  it('rebuilds the form from the shown collection when the save returns no base', async () => {
    seed(['hiro'])
    vi.mocked(savePersonas).mockImplementation(async () => {
      seed(['hiro'], { hiro: { name: 'From elsewhere' } })
      return { hostId: 'h1', mirror: NO_MIRROR, base: null }
    })
    render(<PersonasSection />)
    fireEvent.click(screen.getByTestId('persona-row-hiro'))
    fireEvent.change(screen.getByTestId('persona-name').querySelector('input')!, { target: { value: 'Mine' } })
    fireEvent.click(screen.getByTestId('persona-save'))
    await waitFor(() => expect((screen.getByTestId('persona-name').querySelector('input') as HTMLInputElement).value).toBe('From elsewhere'))
    expect(screen.getByTestId('personas-note').textContent).toMatch(/重新載入|reloaded/)
  })

  it('keeps what was typed for an existing persona when the save turns out stale or conflicting, on the new base', async () => {
    seed(['hiro'])
    vi.mocked(savePersonas).mockImplementationOnce(async () => {
      seed(['hiro'], { hiro: { name: 'Their Edit' } }) // another host won meanwhile
      throw new PersonaStaleError()
    })
    render(<PersonasSection />)
    fireEvent.click(screen.getByTestId('persona-row-hiro'))
    fireEvent.change(screen.getByTestId('persona-name').querySelector('input')!, { target: { value: 'My Edit' } })
    fireEvent.click(screen.getByTestId('persona-save'))
    expect((await screen.findByTestId('personas-note')).textContent).toMatch(/保留|kept/)
    expect((screen.getByTestId('persona-name').querySelector('input') as HTMLInputElement).value).toBe('My Edit')
    // the next save goes out on the NEW base, not the one the form was opened with
    vi.mocked(savePersonas).mockResolvedValueOnce({ hostId: 'h1', mirror: NO_MIRROR, base: { hostId: 'h1', revision: 9, stamp: { at: 9, host: 'h1' } } })
    fireEvent.click(screen.getByTestId('persona-save'))
    await waitFor(() => expect(savePersonas).toHaveBeenCalledTimes(2))
    expect(vi.mocked(savePersonas).mock.calls[1][1]).toEqual({ hostId: 'h1', revision: 4, stamp: { at: 5, host: 'h1' } })
    expect(vi.mocked(savePersonas).mock.calls[1][0][0].name).toBe('My Edit')
  })

  it('stops editing, switching and a second save while a save is in flight, and applies nothing stale', async () => {
    seed(['hiro', 'emma'])
    let finish!: (r: { hostId: string; mirror: typeof NO_MIRROR; base: null }) => void
    vi.mocked(savePersonas).mockImplementation(() => new Promise((r) => { finish = r as typeof finish }))
    render(<PersonasSection />)
    fireEvent.click(screen.getByTestId('persona-row-hiro'))
    fireEvent.click(screen.getByTestId('persona-save'))
    await waitFor(() => expect(savePersonas).toHaveBeenCalledTimes(1))
    expect(screen.getByTestId('persona-name').querySelector('input')!.matches(':disabled')).toBe(true) // via the fieldset
    expect((screen.getByTestId('persona-new') as HTMLButtonElement).disabled).toBe(true)
    expect((screen.getByTestId('persona-row-emma') as HTMLButtonElement).disabled).toBe(true)
    fireEvent.submit(screen.getByTestId('persona-form')) // a second submit while the first is pending
    expect(savePersonas).toHaveBeenCalledTimes(1)
    finish({ hostId: 'h1', mirror: NO_MIRROR, base: null })
    await waitFor(() => expect(screen.getByTestId('persona-name').querySelector('input')!.matches(':disabled')).toBe(false))
  })

  it('stays locked when the page is left and re-entered while a save is in flight, and picks up the result', async () => {
    seed(['hiro'])
    let finish!: (r: { hostId: string; mirror: typeof NO_MIRROR; base: null }) => void
    vi.mocked(savePersonas).mockImplementation(() => new Promise((r) => { finish = r as typeof finish }))
    const first = render(<PersonasSection />)
    fireEvent.click(screen.getByTestId('persona-row-hiro'))
    fireEvent.change(screen.getByTestId('persona-name').querySelector('input')!, { target: { value: 'Saved Name' } })
    fireEvent.click(screen.getByTestId('persona-save'))
    await waitFor(() => expect(savePersonas).toHaveBeenCalledTimes(1))
    first.unmount() // another settings section was chosen
    render(<PersonasSection />) // and the user came back
    const input = () => screen.getByTestId('persona-name').querySelector('input')!
    expect(input().matches(':disabled')).toBe(true)
    fireEvent.submit(screen.getByTestId('persona-form'))
    expect(savePersonas).toHaveBeenCalledTimes(1) // no second write
    seed(['hiro'], { hiro: { name: 'Saved Name' } }) // what the daemon now holds
    finish({ hostId: 'h1', mirror: NO_MIRROR, base: null })
    await waitFor(() => expect(input().matches(':disabled')).toBe(false))
    expect((input() as HTMLInputElement).value).toBe('Saved Name')
  })

  it.each([
    ['an API failure', () => new PersonaApiError(400, 'too long', { index: 0, field: 'name' }), 'personas-error', /too long/],
    ['a whole-collection failure (index -1)', () => new PersonaApiError(400, 'cannot be removed', { index: -1 }), 'personas-error', /cannot be removed/],
    ['a stale save', () => new PersonaStaleError(), 'personas-note', /保留|kept/],
    ['a conflict', () => new PersonaConflictError(makeCollection(['hiro'], { at: 9, host: 'h1' }, 6)), 'personas-note', /保留|kept/],
  ])('still tells the user about %s that came back while the page was away', async (_why, make, testId, text) => {
    seed(['hiro'])
    let fail!: (e: Error) => void
    vi.mocked(savePersonas).mockImplementation(() => new Promise((_r, rej) => { fail = rej }))
    const first = render(<PersonasSection />)
    fireEvent.click(screen.getByTestId('persona-row-hiro'))
    fireEvent.click(screen.getByTestId('persona-save'))
    await waitFor(() => expect(savePersonas).toHaveBeenCalledTimes(1))
    first.unmount()
    render(<PersonasSection />)
    fail(make())
    expect((await screen.findByTestId(testId)).textContent).toMatch(text)
  })

  it('shows the "saved" note on the page that was re-entered, and drops it when the user starts something new', async () => {
    seed(['hiro', 'emma'])
    let done!: (r: { hostId: string; mirror: typeof NO_MIRROR; base: { hostId: string; revision: number; stamp: { at: number; host: string } } }) => void
    vi.mocked(savePersonas).mockImplementation(() => new Promise((r) => { done = r as typeof done }))
    const first = render(<PersonasSection />)
    fireEvent.click(screen.getByTestId('persona-row-hiro'))
    fireEvent.click(screen.getByTestId('persona-save'))
    await waitFor(() => expect(savePersonas).toHaveBeenCalledTimes(1))
    first.unmount()
    render(<PersonasSection />)
    done({ hostId: 'h1', mirror: NO_MIRROR, base: { hostId: 'h1', revision: 5, stamp: { at: 9, host: 'h1' } } })
    expect((await screen.findByTestId('personas-note')).textContent).toContain('mlab')
    fireEvent.click(screen.getByTestId('persona-row-emma'))
    expect(screen.queryByTestId('personas-note')).toBeNull()
  })

  it('stops "New persona" at 64 personas (retired ones count) and says why', () => {
    const codes = Array.from({ length: 64 }, (_, i) => 'a' + String.fromCharCode(97 + (i % 26)) + String.fromCharCode(97 + Math.floor(i / 26)))
    seed(codes, { [codes[0]]: { retired: true } })
    render(<PersonasSection />)
    expect((screen.getByTestId('persona-new') as HTMLButtonElement).disabled).toBe(true)
    expect(screen.getByTestId('personas-cap').textContent).toMatch(/64/)
  })

  it('shows a collection-level validation failure from savePersonas in words, not as an unknown error', async () => {
    seed(['hiro'])
    vi.mocked(savePersonas).mockRejectedValue(new PersonaValidationError([{ field: 'olga.nationality.zh', message: 'over 200 characters' }]))
    render(<PersonasSection />)
    fireEvent.click(screen.getByTestId('persona-row-hiro'))
    fireEvent.click(screen.getByTestId('persona-save'))
    const err = (await screen.findByTestId('personas-error')).textContent!
    expect(err).toMatch(/不合規定|break the rules/)
    expect(err).toContain('olga.nationality.zh')
  })

  it('keeps what was typed for a new persona when the save turns out stale', async () => {
    seed(['hiro'])
    vi.mocked(savePersonas).mockRejectedValueOnce(new PersonaStaleError())
    render(<PersonasSection />)
    fireEvent.click(screen.getByTestId('persona-new'))
    fireEvent.change(screen.getByTestId('persona-code').querySelector('input')!, { target: { value: 'newone' } })
    fireEvent.change(screen.getByTestId('persona-name').querySelector('input')!, { target: { value: 'New One' } })
    fireEvent.click(screen.getByTestId('persona-save'))
    expect((await screen.findByTestId('personas-note')).textContent).toMatch(/保留|kept/)
    expect((screen.getByTestId('persona-code').querySelector('input') as HTMLInputElement).value).toBe('newone')
    expect((screen.getByTestId('persona-name').querySelector('input') as HTMLInputElement).value).toBe('New One')
    expect(screen.queryByTestId('personas-error')).toBeNull()
  })

  it('keeps a new persona that the shown collection did not take after a save with no base', async () => {
    seed(['hiro'])
    vi.mocked(savePersonas).mockImplementation(async () => ({ hostId: 'h1', mirror: NO_MIRROR, base: null })) // another copy won; it lacks "newone"
    render(<PersonasSection />)
    fireEvent.click(screen.getByTestId('persona-new'))
    fireEvent.change(screen.getByTestId('persona-code').querySelector('input')!, { target: { value: 'newone' } })
    fireEvent.change(screen.getByTestId('persona-name').querySelector('input')!, { target: { value: 'New One' } })
    fireEvent.click(screen.getByTestId('persona-save'))
    await waitFor(() => expect(screen.getByTestId('personas-note')).toBeTruthy())
    expect((screen.getByTestId('persona-name').querySelector('input') as HTMLInputElement).value).toBe('New One')
  })

  it('shows a nationality text that is over 200 characters on its own field', () => {
    seed(['hiro'])
    render(<PersonasSection />)
    fireEvent.click(screen.getByTestId('persona-row-hiro'))
    fireEvent.change(screen.getByTestId('persona-nationality-zh').querySelector('input')!, { target: { value: '字'.repeat(201) } })
    fireEvent.click(screen.getByTestId('persona-save'))
    expect(savePersonas).not.toHaveBeenCalled()
    expect(screen.getByTestId('persona-nationality-zh').textContent).toMatch(/200/)
  })

  it('lists a problem in a field the form does not show (a template text) in the error', () => {
    seed(['hiro'], { hiro: { template: { name: 'x'.repeat(201), name_zh: '', field_zh: '', field_en: '' } } })
    render(<PersonasSection />)
    fireEvent.click(screen.getByTestId('persona-row-hiro'))
    fireEvent.click(screen.getByTestId('persona-save'))
    expect(savePersonas).not.toHaveBeenCalled()
    expect(screen.getByTestId('personas-error').textContent).toContain('template.name')
  })

  it('a stale or conflicting save reloads the persona and says so, without losing the page', async () => {
    seed(['hiro'])
    vi.mocked(savePersonas).mockRejectedValueOnce(new PersonaStaleError())
    render(<PersonasSection />)
    fireEvent.click(screen.getByTestId('persona-row-hiro'))
    fireEvent.click(screen.getByTestId('persona-save'))
    expect((await screen.findByTestId('personas-note')).textContent).toMatch(/保留|kept/)
    vi.mocked(savePersonas).mockRejectedValueOnce(new PersonaConflictError(makeCollection(['hiro'], { at: 9, host: 'h1' }, 6)))
    fireEvent.click(screen.getByTestId('persona-save'))
    await waitFor(() => expect(savePersonas).toHaveBeenCalledTimes(2))
    expect(screen.getByTestId('persona-form')).toBeTruthy()
  })

  it('shows an error that belongs to the whole collection (index -1) as a whole, not on a row', async () => {
    seed(['hiro'])
    vi.mocked(savePersonas).mockRejectedValue(new PersonaApiError(400, 'stored code "olga" cannot be removed', { index: -1 }))
    render(<PersonasSection />)
    fireEvent.click(screen.getByTestId('persona-row-hiro'))
    fireEvent.click(screen.getByTestId('persona-save'))
    const err = await screen.findByTestId('personas-error')
    expect(err.textContent).toContain('cannot be removed')
    expect(err.textContent).not.toMatch(/「|“/) // no field is named
  })

  it('names the field of an error that belongs to one item', async () => {
    seed(['hiro'])
    vi.mocked(savePersonas).mockRejectedValue(new PersonaApiError(400, 'too long', { index: 0, field: 'name' }))
    render(<PersonasSection />)
    fireEvent.click(screen.getByTestId('persona-row-hiro'))
    fireEvent.click(screen.getByTestId('persona-save'))
    expect((await screen.findByTestId('personas-error')).textContent).toContain('name')
  })

  it('keeps an unsaved draft across leaving the page and coming back', () => {
    seed(['hiro'])
    const first = render(<PersonasSection />)
    fireEvent.click(screen.getByTestId('persona-row-hiro'))
    fireEvent.change(screen.getByTestId('persona-name').querySelector('input')!, { target: { value: 'Half typed' } })
    first.unmount() // the settings tab was switched away
    render(<PersonasSection />)
    expect((screen.getByTestId('persona-name').querySelector('input') as HTMLInputElement).value).toBe('Half typed')
  })
})
