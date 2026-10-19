import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor, cleanup, act } from '@testing-library/react'

vi.mock('../../../lib/names/persona-save', async (orig) => ({ ...(await orig<typeof import('../../../lib/names/persona-save')>()), savePersonas: vi.fn() }))
vi.mock('../../../lib/names/persona-draw', async (orig) => ({ ...(await orig<typeof import('../../../lib/names/persona-draw')>()), drawRecipe: vi.fn() }))

import { drawRecipe } from '../../../lib/names/persona-draw'
import { savePersonas, PersonaStaleError } from '../../../lib/names/persona-save'
import { clearDraftMemory, getOutcome, savePending } from '../../../lib/names/persona-draft-memory'
import { usePersonaStore } from '../../../stores/usePersonaStore'
import { usePersonaSourceStore } from '../../../stores/usePersonaSourceStore'
import { usePeerStore, emptyPeerHostEntry } from '../../../stores/usePeerStore'
import { useHostStore } from '../../../stores/useHostStore'
import { makeCollection, makePersona } from '../../../lib/names/__fixtures__/personas'
import { PersonaImport, MAX_IMPORT_BYTES } from './PersonaImport'

const WEBP = 'data:image/webp;base64,UklGRgAAAABXRUJQ'
const NO_MIRROR = { mirrored: [], failed: [], pulled: [] }
const recipe = (seed: string) => ({ source: 'lorelei', seed })

function seed(codes: string[]) {
  const c = makeCollection(codes, { at: 5, host: 'h1' }, 4)
  c.items = c.items.map((p) => makePersona(p.code, { avatar: recipe('old'), avatar_data: WEBP }))
  usePersonaStore.setState({ byHost: { h1: { status: 'ready', collection: c } } })
  return c.items
}

function pick(entries: unknown, name = 'personas.json') {
  const text = typeof entries === 'string' ? entries : JSON.stringify(entries)
  fireEvent.change(screen.getByTestId('persona-import-file'), { target: { files: [new File([text], name, { type: 'application/json' })] } })
}

describe('PersonaImport', () => {
  beforeEach(() => {
    cleanup()
    clearDraftMemory()
    // an ordinary save: the page still shows what was written, so the answer carries a base
    vi.mocked(savePersonas).mockReset().mockResolvedValue({ hostId: 'h1', mirror: NO_MIRROR, base: { hostId: 'h1', revision: 5, stamp: { at: 9, host: 'h1' } } })
    vi.mocked(drawRecipe).mockReset().mockResolvedValue(WEBP)
    useHostStore.setState({ hosts: { h1: { id: 'h1' } as never }, hostOrder: ['h1'], runtime: { h1: { status: 'connected' } as never } })
    usePersonaSourceStore.setState({ hostId: null })
    usePeerStore.setState({ byHost: {}, refresh: vi.fn(async () => {}) })
  })

  const mount = () => render(<PersonaImport fallbackHostId="h1" disabled={false} />)

  it('shows what the file would change before anything is drawn or saved', async () => {
    seed(['hiro', 'emma'])
    mount()
    pick([{ id: 'hiro', name: 'Hiro Two' }, { id: 'olga', name: 'Olga', avatar: recipe('o') }, { id: 'emma', name: 'Emma' }])
    expect((await screen.findByTestId('persona-import-counts')).textContent).toMatch(/新增 1.*變更 1.*不變 1.*略過 0|1 new, 1 changed, 1 unchanged, 0 skipped/)
    expect(screen.getByTestId('persona-import-counts').textContent).toMatch(/重畫 1|1 avatars/)
    expect(drawRecipe).not.toHaveBeenCalled()
    expect(savePersonas).not.toHaveBeenCalled()
  })

  it('plans on the collection and the base of the moment the file has been read, never one of each from different moments', async () => {
    seed(['hiro'])
    mount()
    pick([{ id: 'emma', name: 'Emma' }])
    // the file is still being read: the shown collection (another source) changes under it
    usePersonaStore.setState({ byHost: { h1: { status: 'ready', collection: makeCollection(['olga'], { at: 9, host: 'h1' }, 9) } } })
    fireEvent.click(await screen.findByTestId('persona-import-confirm'))
    await waitFor(() => expect(savePersonas).toHaveBeenCalledTimes(1))
    const [saved, base] = vi.mocked(savePersonas).mock.calls[0]
    expect(saved.map((p) => p.code)).toEqual(['olga', 'emma']) // merged into the collection now shown
    expect(base).toMatchObject({ hostId: 'h1', revision: 9 }) // and saved against that collection's base
  })

  it('refuses a file over the size limit before reading it', async () => {
    seed(['hiro'])
    mount()
    const big = new File(['[]'], 'big.json', { type: 'application/json' })
    Object.defineProperty(big, 'size', { value: MAX_IMPORT_BYTES + 1 })
    const read = vi.spyOn(FileReader.prototype, 'readAsText')
    fireEvent.change(screen.getByTestId('persona-import-file'), { target: { files: [big] } })
    expect((await screen.findByTestId('persona-import-problem')).textContent).toMatch(/太大|too large/)
    expect(read).not.toHaveBeenCalled()
    expect(screen.queryByTestId('persona-import-summary')).toBeNull()
    read.mockRestore()
  })

  it('drops the summary of the file before the moment another is picked, so it cannot be confirmed meanwhile', async () => {
    seed(['hiro']); mount()
    pick([{ id: 'emma', name: 'Emma' }])
    await screen.findByTestId('persona-import-summary')
    // the second file is slow to read
    const real = FileReader.prototype.readAsText
    const spy = vi.spyOn(FileReader.prototype, 'readAsText').mockImplementation(function (this: FileReader) { /* never finishes */ void this })
    pick([{ id: 'olga', name: 'Olga' }], 'second.json')
    expect(screen.queryByTestId('persona-import-summary')).toBeNull()
    expect(screen.queryByTestId('persona-import-confirm')).toBeNull()
    spy.mockRestore(); void real
  })

  it('keeps only the file picked last when an earlier one finishes reading later', async () => {
    seed(['hiro']); mount()
    const real = FileReader.prototype.readAsText
    const finish: Array<() => void> = []
    const spy = vi.spyOn(FileReader.prototype, 'readAsText').mockImplementation(function (this: FileReader, blob: Blob) {
      finish.push(() => real.call(this, blob))
    })
    pick([{ id: 'emma', name: 'Emma' }], 'first.json')
    pick([{ id: 'olga', name: 'Olga' }], 'second.json')
    spy.mockRestore()
    finish[1](); await screen.findByTestId('persona-import-summary')
    finish[0]() // the first, abandoned file is read last
    await act(async () => { await new Promise((r) => setTimeout(r, 50)) })
    fireEvent.click(screen.getByTestId('persona-import-confirm'))
    await waitFor(() => expect(savePersonas).toHaveBeenCalledTimes(1))
    expect(vi.mocked(savePersonas).mock.calls[0][0].map((p) => p.code)).toEqual(['hiro', 'olga'])
  })

  it('lists each entry it would skip, with the reason', async () => {
    seed(['hiro']); mount()
    pick([{ id: 'Bad Code', name: 'x' }, { id: 'emma', name: 'Emma' }, { id: 'emma', name: 'Emma again' }])
    const list = await screen.findByTestId('persona-import-skipped')
    expect(list.textContent).toContain('Bad Code')
    expect(list.textContent).toMatch(/代碼不合規定|code breaks/)
    expect(list.textContent).toMatch(/兩次|twice/)
  })

  it('saves nothing when the user cancels', async () => {
    seed(['hiro']); mount()
    pick([{ id: 'emma', name: 'Emma' }])
    fireEvent.click(await screen.findByTestId('persona-import-cancel'))
    expect(screen.queryByTestId('persona-import-summary')).toBeNull()
    expect(savePersonas).not.toHaveBeenCalled()
    expect(drawRecipe).not.toHaveBeenCalled()
  })

  it('draws the marked avatars, then saves the merged collection on the base the plan was made on', async () => {
    const items = seed(['hiro'])
    mount()
    pick([{ id: 'emma', name: 'Emma', avatar: recipe('e') }])
    fireEvent.click(await screen.findByTestId('persona-import-confirm'))
    await waitFor(() => expect(savePersonas).toHaveBeenCalledTimes(1))
    const [saved, base, fallback] = vi.mocked(savePersonas).mock.calls[0]
    expect(saved.map((p) => p.code)).toEqual(['hiro', 'emma'])
    expect(saved.find((p) => p.code === 'emma')).toMatchObject({ avatar: recipe('e'), avatar_data: WEBP })
    expect(saved.find((p) => p.code === 'hiro')).toEqual(items[0]) // untouched
    expect(base).toMatchObject({ hostId: 'h1', revision: 4 })
    expect(fallback).toBe('h1')
    await waitFor(() => expect(getOutcome().note).toMatch(/已存在|Saved/))
    expect(screen.queryByTestId('persona-import-summary')).toBeNull()
  })

  it('saves against the base from when the file was chosen, not the one of the moment it is confirmed', async () => {
    seed(['hiro']); mount()
    pick([{ id: 'emma', name: 'Emma' }])
    await screen.findByTestId('persona-import-confirm')
    // meanwhile another save moved the shown collection on to revision 9
    usePersonaStore.setState({ byHost: { h1: { status: 'ready', collection: makeCollection(['hiro', 'olga'], { at: 9, host: 'h1' }, 9) } } })
    fireEvent.click(screen.getByTestId('persona-import-confirm'))
    await waitFor(() => expect(savePersonas).toHaveBeenCalledTimes(1))
    expect(vi.mocked(savePersonas).mock.calls[0][1]).toMatchObject({ hostId: 'h1', revision: 4 })
  })

  it('holds the page save lock from the click until the save is over', async () => {
    seed(['hiro']); mount()
    let release!: () => void
    vi.mocked(drawRecipe).mockImplementation(() => new Promise<string>((r) => { release = () => r(WEBP) }))
    pick([{ id: 'emma', name: 'Emma', avatar: recipe('e') }])
    fireEvent.click(await screen.findByTestId('persona-import-confirm'))
    await waitFor(() => expect(drawRecipe).toHaveBeenCalled())
    expect(savePending()).toBe(true) // the form and the other saves are locked while images are drawn
    await act(async () => { release() })
    await waitFor(() => expect(savePending()).toBe(false))
  })

  it('saves anyway when an avatar cannot be drawn, keeps the old one, and names the code', async () => {
    seed(['hiro']); mount()
    vi.mocked(drawRecipe).mockRejectedValue(new Error('no'))
    pick([{ id: 'hiro', name: 'Hiro', avatar: recipe('new') }])
    fireEvent.click(await screen.findByTestId('persona-import-confirm'))
    await waitFor(() => expect(savePersonas).toHaveBeenCalledTimes(1))
    expect(vi.mocked(savePersonas).mock.calls[0][0][0]).toMatchObject({ avatar: recipe('old'), avatar_data: WEBP })
    await waitFor(() => expect(getOutcome().note).toContain('hiro'))
  })

  it('tells the page the import was saved, and says so when the form that was open was rebuilt', async () => {
    const onImported = vi.fn(() => true)
    seed(['hiro'])
    render(<PersonaImport fallbackHostId="h1" disabled={false} onImported={onImported} />)
    vi.mocked(savePersonas).mockResolvedValue({ hostId: 'h1', mirror: NO_MIRROR, base: { hostId: 'h1', revision: 5, stamp: { at: 9, host: 'h1' } } })
    pick([{ id: 'emma', name: 'Emma' }])
    fireEvent.click(await screen.findByTestId('persona-import-confirm'))
    await waitFor(() => expect(onImported).toHaveBeenCalledTimes(1))
    await waitFor(() => expect(getOutcome().note).toMatch(/表單已改成|open form now shows/))
  })

  it('does not call the page when the save failed', async () => {
    const onImported = vi.fn(() => true)
    seed(['hiro'])
    render(<PersonaImport fallbackHostId="h1" disabled={false} onImported={onImported} />)
    vi.mocked(savePersonas).mockRejectedValue(new PersonaStaleError())
    pick([{ id: 'emma', name: 'Emma' }])
    fireEvent.click(await screen.findByTestId('persona-import-confirm'))
    await waitFor(() => expect(getOutcome().error).toBeTruthy())
    expect(onImported).not.toHaveBeenCalled()
  })

  it('does not report plain success when another collection won while the import was written', async () => {
    seed(['hiro']); mount()
    // savePersonas answers base: null when the shown collection is no longer the one it wrote
    vi.mocked(savePersonas).mockResolvedValue({ hostId: 'h1', mirror: NO_MIRROR, base: null })
    pick([{ id: 'emma', name: 'Emma' }])
    fireEvent.click(await screen.findByTestId('persona-import-confirm'))
    await waitFor(() => expect(getOutcome().note).toMatch(/別處又有更新|something newer/))
    expect(getOutcome().note).not.toMatch(/^已存在|^Saved on/)
  })

  it('does not offer to import a file that would change nothing', async () => {
    const items = seed(['hiro'])
    mount()
    pick([{ id: 'hiro', name: items[0].name }])
    expect((await screen.findByTestId('persona-import-nothing'))).toBeTruthy()
    expect((screen.getByTestId('persona-import-confirm') as HTMLButtonElement).disabled).toBe(true)
  })

  it('says so when the file is not a list of personas, and shows no summary', async () => {
    seed(['hiro']); mount()
    pick('{"hello": 1}')
    expect((await screen.findByTestId('persona-import-problem')).textContent).toMatch(/不是角色清單|not a list/)
    expect(screen.queryByTestId('persona-import-summary')).toBeNull()
    pick('not json at all')
    await waitFor(() => expect(screen.getByTestId('persona-import-problem')).toBeTruthy())
  })

  it('asks the user to choose the file again when the collection moved after the plan was made', async () => {
    seed(['hiro']); mount()
    vi.mocked(savePersonas).mockRejectedValue(new PersonaStaleError())
    pick([{ id: 'emma', name: 'Emma' }])
    fireEvent.click(await screen.findByTestId('persona-import-confirm'))
    await waitFor(() => expect(getOutcome().error).toMatch(/重新選|again/))
    expect(savePending()).toBe(false)
  })

  it('asks first when the source has no persona yet, and sends nothing until confirmed', async () => {
    usePeerStore.setState({
      byHost: { h1: { ...emptyPeerHostEntry(), fetchedAt: 5, rows: {} } },
      refresh: vi.fn(async () => {}),
    })
    seed([]); mount()
    pick([{ id: 'emma', name: 'Emma' }])
    fireEvent.click(await screen.findByTestId('persona-import-confirm'))
    expect(await screen.findByTestId('persona-first-save')).toBeTruthy()
    expect(savePersonas).not.toHaveBeenCalled()
    expect(drawRecipe).not.toHaveBeenCalled()
    fireEvent.click(screen.getByTestId('persona-first-save-confirm'))
    await waitFor(() => expect(savePersonas).toHaveBeenCalledTimes(1))
  })

  it('does not ask again when the source already has personas', async () => {
    seed(['hiro']); mount()
    pick([{ id: 'emma', name: 'Emma' }])
    fireEvent.click(await screen.findByTestId('persona-import-confirm'))
    await waitFor(() => expect(savePersonas).toHaveBeenCalledTimes(1))
    expect(screen.queryByTestId('persona-first-save')).toBeNull()
  })

  it('is off while the page is locked or read-only', () => {
    render(<PersonaImport fallbackHostId="h1" disabled />)
    expect((screen.getByTestId('persona-import') as HTMLButtonElement).disabled).toBe(true)
  })
})
