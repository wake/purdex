import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor, cleanup, act } from '@testing-library/react'

vi.mock('../../../lib/names/persona-draw', async (orig) => ({ ...(await orig<typeof import('../../../lib/names/persona-draw')>()), drawRecipe: vi.fn() }))

import { drawRecipe, PersonaDrawError } from '../../../lib/names/persona-draw'
import { clearAvatarOptionsMemory } from '../../../lib/names/avatar-options-memory'
import { AvatarPicker } from './AvatarPicker'

const DATA = 'data:image/webp;base64,AAAA'

function setup(recipe = { source: '' } as { source: string; seed?: string; options?: Record<string, unknown>; url?: string }) {
  const onChange = vi.fn()
  const view = render(<AvatarPicker slot="hiro" recipe={recipe} disabled={false} onChange={onChange} />)
  return { onChange, ...view }
}

describe('AvatarPicker', () => {
  beforeEach(() => {
    cleanup()
    clearAvatarOptionsMemory()
    vi.mocked(drawRecipe).mockReset().mockResolvedValue(DATA)
  })

  it('draws a DiceBear style from its seed and options and hands back the recipe with the image', async () => {
    const { onChange } = setup()
    fireEvent.change(screen.getByTestId('avatar-source'), { target: { value: 'lorelei' } })
    fireEvent.change(screen.getByTestId('avatar-seed'), { target: { value: 'hiro-seed' } })
    await waitFor(() => expect(onChange).toHaveBeenLastCalledWith({ source: 'lorelei', seed: 'hiro-seed' }, DATA))
    expect(vi.mocked(drawRecipe).mock.calls.at(-1)![0]).toMatchObject({ source: 'lorelei', seed: 'hiro-seed' })
  })

  it('takes DiceBear options as JSON and refuses text that is not a JSON object, without drawing', async () => {
    const { onChange } = setup({ source: 'micah', seed: 's' })
    expect(drawRecipe).not.toHaveBeenCalled() // opening a persona draws nothing
    fireEvent.change(screen.getByTestId('avatar-options'), { target: { value: '{"backgroundColor": ' } })
    expect(screen.getByTestId('avatar-options-error')).toBeTruthy()
    fireEvent.change(screen.getByTestId('avatar-options'), { target: { value: '[1,2]' } })
    expect(screen.getByTestId('avatar-options-error')).toBeTruthy()
    await act(async () => { await new Promise((r) => setTimeout(r, 500)) })
    expect(drawRecipe).not.toHaveBeenCalled()
    expect(onChange).not.toHaveBeenCalled()
    fireEvent.change(screen.getByTestId('avatar-options'), { target: { value: '{"flip": true}' } })
    expect(screen.queryByTestId('avatar-options-error')).toBeNull()
    await waitFor(() => expect(onChange).toHaveBeenLastCalledWith({ source: 'micah', seed: 's', options: { flip: true } }, DATA))
  })

  it('keeps half-typed options when the page is left and re-entered', () => {
    const first = setup({ source: 'micah', seed: 's' })
    fireEvent.change(screen.getByTestId('avatar-options'), { target: { value: '{"half": ' } })
    first.unmount()
    setup({ source: 'micah', seed: 's' })
    expect((screen.getByTestId('avatar-options') as HTMLTextAreaElement).value).toBe('{"half": ')
  })

  it('fetches a photo only when asked, not on every keystroke', async () => {
    const { onChange } = setup({ source: 'photo' })
    fireEvent.change(screen.getByTestId('avatar-url'), { target: { value: 'https://picsum.photos/seed/x/200' } })
    await act(async () => { await new Promise((r) => setTimeout(r, 500)) })
    expect(drawRecipe).not.toHaveBeenCalled()
    fireEvent.click(screen.getByTestId('avatar-fetch'))
    await waitFor(() => expect(onChange).toHaveBeenLastCalledWith({ source: 'photo', url: 'https://picsum.photos/seed/x/200' }, DATA))
  })

  it('says why a photo could not be drawn, in words', async () => {
    vi.mocked(drawRecipe).mockRejectedValue(new PersonaDrawError('upload-needed', 'x'))
    const { onChange } = setup({ source: 'photo', url: 'https://placecats.com/300/300' })
    fireEvent.click(screen.getByTestId('avatar-fetch'))
    expect((await screen.findByTestId('avatar-error')).textContent).toMatch(/上傳|upload/i)
    expect(onChange).not.toHaveBeenCalled()
  })

  it('draws an uploaded file once and keeps only the recipe {source: upload}', async () => {
    const { onChange } = setup({ source: 'upload' })
    const file = new File(['x'], 'me.png', { type: 'image/png' })
    fireEvent.change(screen.getByTestId('avatar-file'), { target: { files: [file] } })
    await waitFor(() => expect(onChange).toHaveBeenLastCalledWith({ source: 'upload' }, DATA))
    expect(vi.mocked(drawRecipe).mock.calls[0][1]).toMatchObject({ file })
  })

  it('clears the image when the source is set back to initials', async () => {
    const { onChange } = setup({ source: 'lorelei', seed: 's' })
    fireEvent.change(screen.getByTestId('avatar-source'), { target: { value: '' } })
    expect(onChange).toHaveBeenLastCalledWith({ source: '' }, '')
  })

  it('applies only the newest draw when an older one finishes late', async () => {
    let finishFirst!: (v: string) => void
    vi.mocked(drawRecipe)
      .mockImplementationOnce(() => new Promise<string>((r) => { finishFirst = r }))
      .mockResolvedValue('data:image/webp;base64,NEW')
    const { onChange } = setup()
    fireEvent.change(screen.getByTestId('avatar-source'), { target: { value: 'lorelei' } })
    fireEvent.change(screen.getByTestId('avatar-seed'), { target: { value: 'one' } })
    await waitFor(() => expect(drawRecipe).toHaveBeenCalledTimes(1))
    fireEvent.change(screen.getByTestId('avatar-seed'), { target: { value: 'two' } })
    await waitFor(() => expect(onChange).toHaveBeenLastCalledWith({ source: 'lorelei', seed: 'two' }, 'data:image/webp;base64,NEW'))
    await act(async () => { finishFirst('data:image/webp;base64,OLD') })
    expect(onChange).toHaveBeenLastCalledWith({ source: 'lorelei', seed: 'two' }, 'data:image/webp;base64,NEW')
  })

  it('drops a draw that is still out when the picker goes away', async () => {
    let finish!: (v: string) => void
    vi.mocked(drawRecipe).mockImplementation(() => new Promise<string>((r) => { finish = r }))
    const { onChange, unmount } = setup({ source: 'photo', url: 'https://picsum.photos/seed/x/200' })
    fireEvent.click(screen.getByTestId('avatar-fetch'))
    await waitFor(() => expect(drawRecipe).toHaveBeenCalledTimes(1))
    unmount() // another persona was opened
    await act(async () => { finish(DATA) })
    expect(onChange).not.toHaveBeenCalled()
  })

  it('is read-only while a save is in flight', () => {
    render(<AvatarPicker slot="hiro" recipe={{ source: 'lorelei', seed: 's' }} disabled onChange={vi.fn()} />)
    expect((screen.getByTestId('avatar-source') as HTMLSelectElement).disabled).toBe(true)
    expect((screen.getByTestId('avatar-seed') as HTMLInputElement).disabled).toBe(true)
  })
})
