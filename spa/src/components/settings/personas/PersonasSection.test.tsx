import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor, cleanup } from '@testing-library/react'

vi.mock('../../../lib/names/persona-save', async (orig) => ({ ...(await orig<typeof import('../../../lib/names/persona-save')>()), savePersonas: vi.fn() }))

import { savePersonas, PersonaStaleError } from '../../../lib/names/persona-save'
import { PersonaApiError, PersonaConflictError } from '../../../lib/names/persona-api'
import { usePersonaStore } from '../../../stores/usePersonaStore'
import { useNexHostStore } from '../../../stores/useNexHostStore'
import { useHostStore } from '../../../stores/useHostStore'
import { clearDraftMemory } from '../../../lib/names/persona-draft-memory'
import { makeCollection, makePersona } from '../../../lib/names/__fixtures__/personas'
import { PersonasSection } from './PersonasSection'

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
    vi.mocked(savePersonas).mockResolvedValue({ hostId: 'h1', base: { hostId: 'h1', revision: 5, stamp: { at: 9, host: 'h1' } } })
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
    vi.mocked(savePersonas).mockResolvedValue({ hostId: 'h1', base: null })
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
      return { hostId: 'h1', base: null }
    })
    render(<PersonasSection />)
    fireEvent.click(screen.getByTestId('persona-row-hiro'))
    fireEvent.change(screen.getByTestId('persona-name').querySelector('input')!, { target: { value: 'Mine' } })
    fireEvent.click(screen.getByTestId('persona-save'))
    await waitFor(() => expect((screen.getByTestId('persona-name').querySelector('input') as HTMLInputElement).value).toBe('From elsewhere'))
    expect(screen.getByTestId('personas-note').textContent).toMatch(/重新載入|reloaded/)
  })

  it('keeps what was typed for a new persona when the save turns out stale', async () => {
    seed(['hiro'])
    vi.mocked(savePersonas).mockRejectedValueOnce(new PersonaStaleError())
    render(<PersonasSection />)
    fireEvent.click(screen.getByTestId('persona-new'))
    fireEvent.change(screen.getByTestId('persona-code').querySelector('input')!, { target: { value: 'newone' } })
    fireEvent.change(screen.getByTestId('persona-name').querySelector('input')!, { target: { value: 'New One' } })
    fireEvent.click(screen.getByTestId('persona-save'))
    expect((await screen.findByTestId('personas-note')).textContent).toMatch(/重新載入|reloaded/)
    expect((screen.getByTestId('persona-code').querySelector('input') as HTMLInputElement).value).toBe('newone')
    expect((screen.getByTestId('persona-name').querySelector('input') as HTMLInputElement).value).toBe('New One')
    expect(screen.queryByTestId('personas-error')).toBeNull()
  })

  it('keeps a new persona that the shown collection did not take after a save with no base', async () => {
    seed(['hiro'])
    vi.mocked(savePersonas).mockImplementation(async () => ({ hostId: 'h1', base: null })) // another copy won; it lacks "newone"
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
    expect((await screen.findByTestId('personas-note')).textContent).toMatch(/重新載入|reloaded/)
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
