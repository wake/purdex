import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor, cleanup, act } from '@testing-library/react'

vi.mock('../../../lib/names/rename-preview', () => ({ fetchRenamePreview: vi.fn() }))

import { fetchRenamePreview } from '../../../lib/names/rename-preview'
import { usePeerStore, emptyPeerHostEntry } from '../../../stores/usePeerStore'
import { makePersona } from '../../../lib/names/__fixtures__/personas'
import { FirstSaveConfirm } from './FirstSaveConfirm'

const row = (i: number) => [`s${i}`, { address: `h1/x-${i}`, ref: `_aaaa0${i}`, title: '', titleSource: '', deliverable: true, reason: '', tmuxInstance: '', agent: null }] as const
// two session rows in the inventory: the floor the App can work out by itself
const inventory = () =>
  usePeerStore.setState({ byHost: { h1: { ...emptyPeerHostEntry(), fetchedAt: 5, rows: Object.fromEntries([row(1), row(2)]) } }, refresh: vi.fn(async () => {}) })
const text = () => screen.getByTestId('persona-first-save-text').textContent ?? ''
// the host label ("h1") has a digit of its own, so look for the count's own wording
const counted = () => /\d+ of the|\d+ 個/.test(text())
const mount = () => render(<FirstSaveConfirm hostId="h1" personas={[makePersona('hiro')]} onConfirm={() => {}} onCancel={() => {}} />)

describe('FirstSaveConfirm', () => {
  beforeEach(() => { cleanup(); inventory(); vi.mocked(fetchRenamePreview).mockReset() })

  it("shows the daemon's count, without 'at least', when it answers", async () => {
    vi.mocked(fetchRenamePreview).mockResolvedValue(7)
    mount()
    await waitFor(() => expect(text()).toContain('7'))
    expect(text()).not.toMatch(/至少|at least/)
    expect(fetchRenamePreview).toHaveBeenCalledWith('h1', [expect.objectContaining({ code: 'hiro' })], expect.anything())
  })

  it('falls back to the inventory floor, with "at least", when the daemon cannot answer (older daemon, 503, offline)', async () => {
    vi.mocked(fetchRenamePreview).mockResolvedValue(null)
    mount()
    await waitFor(() => expect(text()).toContain('2'))
    expect(text()).toMatch(/至少|at least/)
  })

  it('names no number when the daemon says 0 exactly, even though the inventory has rows', async () => {
    vi.mocked(fetchRenamePreview).mockResolvedValue(0)
    mount()
    await waitFor(() => expect(fetchRenamePreview).toHaveBeenCalled())
    await act(async () => { await new Promise((r) => setTimeout(r, 30)) }) // let both reads settle
    expect(counted()).toBe(false)
  })

  it('names no number before both reads are back', async () => {
    let answer: (n: number) => void = () => {}
    vi.mocked(fetchRenamePreview).mockReturnValue(new Promise<number>((r) => { answer = r }))
    mount()
    expect(counted()).toBe(false)
    answer(4)
    await waitFor(() => expect(text()).toContain('4'))
  })
})
