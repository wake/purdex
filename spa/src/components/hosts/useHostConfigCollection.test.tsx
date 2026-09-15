// spa/src/components/hosts/useHostConfigCollection.test.tsx — the shared
// mechanics of the daemon-backed collection editors (Projects and Commands).
//
// The behaviour under test is the one the two sections used to get wrong
// separately: a row action is a transformation of whatever the store holds
// when it RUNS, and two of them never overlap.
import { describe, it, expect, beforeEach, vi } from 'vitest'
import { act, renderHook, waitFor } from '@testing-library/react'
import { useHostConfigCollection } from './useHostConfigCollection'
import { useHostStore } from '../../stores/useHostStore'
import { emptyHostConfigEntry, useHostConfigStore } from '../../stores/useHostConfigStore'
import { HostConfigConflictError, type HostProject } from '../../lib/host-config-api'
import { MAX_CONFIG_ITEMS } from '../../lib/host-config-validate'

const H = 'h1'
const P1: HostProject = { id: 'p1', name: 'Purdex', slug: 'purdex', path: '/a' }
const P2: HostProject = { id: 'p2', name: 'Ploom', slug: 'ploom', path: '/b' }
const P3: HostProject = { id: 'p3', name: 'Nexen', slug: 'nexen', path: '/c' }

const saveProjects = vi.fn()
/** Resolve the PUT that is waiting, oldest first. */
const settlers: Array<() => void> = []

function seed(projects: HostProject[], status: 'ready' | 'unsupported' = 'ready') {
  useHostConfigStore.setState({
    byHost: { [H]: { ...emptyHostConfigEntry(status), projects } },
    load: vi.fn(async () => {}),
    saveProjects,
  })
}

/** A save that only lands when the test says so, writing to the store like the real one. */
function deferSaves() {
  saveProjects.mockImplementation((hostId: string, items: HostProject[]) => new Promise<void>((resolve) => {
    settlers.push(() => {
      useHostConfigStore.setState((s) => ({ byHost: { ...s.byHost, [hostId]: { ...s.byHost[hostId], projects: items } } }))
      resolve()
    })
  }))
}

beforeEach(() => {
  settlers.length = 0
  saveProjects.mockReset().mockImplementation(async (hostId: string, items: HostProject[]) => {
    useHostConfigStore.setState((s) => ({ byHost: { ...s.byHost, [hostId]: { ...s.byHost[hostId], projects: items } } }))
  })
  useHostStore.setState({
    hosts: { [H]: { id: H, name: 'mlab', ip: '1.2.3.4', port: 7860, order: 0 } },
    hostOrder: [H],
    runtime: { [H]: { status: 'connected' } },
  })
  seed([P1, P2, P3])
})

const items = () => useHostConfigStore.getState().byHost[H].projects

describe('useHostConfigCollection', () => {
  it('serializes two actions fired in one tick and applies each to the list the previous one left', async () => {
    deferSaves()
    const { result } = renderHook(() => useHostConfigCollection<HostProject>(H, 'projects'))

    let both!: Promise<boolean[]>
    act(() => {
      both = Promise.all([result.current.move('p1', 1), result.current.remove('p3')])
    })
    await act(async () => {})

    // One PUT at a time: the second action has not even been planned yet.
    expect(saveProjects).toHaveBeenCalledTimes(1)
    expect(saveProjects).toHaveBeenLastCalledWith(H, [P2, P1, P3])

    await act(async () => { settlers.shift()?.() })
    await waitFor(() => expect(saveProjects).toHaveBeenCalledTimes(2))
    // Planned from the moved list, not from the one the first click saw.
    expect(saveProjects).toHaveBeenLastCalledWith(H, [P2, P1])

    await act(async () => { settlers.shift()?.() })
    await both
    expect(items()).toEqual([P2, P1])
  })

  it('addresses rows by id, so a list that shifted under an action still moves the right row', async () => {
    const { result } = renderHook(() => useHostConfigCollection<HostProject>(H, 'projects'))
    // Something else (another client's copy, a conflict reload) reordered the list.
    act(() => { useHostConfigStore.setState((s) => ({ byHost: { ...s.byHost, [H]: { ...s.byHost[H], projects: [P3, P1, P2] } } })) })
    await act(async () => { await result.current.move('p1', -1) })
    expect(saveProjects).toHaveBeenCalledWith(H, [P1, P3, P2])
  })

  it('a row that is already gone, or an edge move, writes nothing', async () => {
    const { result } = renderHook(() => useHostConfigCollection<HostProject>(H, 'projects'))
    await act(async () => { await result.current.remove('nope') })
    await act(async () => { await result.current.move('p1', -1) })
    expect(saveProjects).not.toHaveBeenCalled()
  })

  it('upsert replaces by id, appends a new item, and refuses one past the limit', async () => {
    const { result } = renderHook(() => useHostConfigCollection<HostProject>(H, 'projects'))
    await act(async () => { await result.current.upsert({ ...P2, name: 'Renamed' }) })
    expect(saveProjects).toHaveBeenLastCalledWith(H, [P1, { ...P2, name: 'Renamed' }, P3])

    seed(Array.from({ length: MAX_CONFIG_ITEMS }, (_, i) => ({ ...P1, id: `x${i}` })))
    let ok = true
    await act(async () => { ok = await result.current.upsert({ ...P1, id: 'new' }) })
    expect(ok).toBe(false)
    expect(result.current.saveError?.text).toContain(String(MAX_CONFIG_ITEMS))
    expect(saveProjects).toHaveBeenCalledTimes(1)
  })

  it('keeps the dialog open until the host takes the item, and closes it when it does', async () => {
    const { result } = renderHook(() => useHostConfigCollection<HostProject>(H, 'projects'))
    saveProjects.mockRejectedValueOnce(new Error('daemon said no'))

    act(() => { result.current.openEditor({ ...P1, name: 'Edited' }) })
    expect(result.current.isNew).toBe(false)
    await act(async () => { result.current.submit({ ...P1, name: 'Edited' }) })
    // The save failed, so the dialog stays open with the error inside it.
    expect(result.current.editing).not.toBeNull()
    expect(result.current.saveError).toMatchObject({ target: 'dialog' })

    await act(async () => { result.current.submit({ ...P1, name: 'Edited' }) })
    expect(result.current.editing).toBeNull()
    expect(items()).toEqual([{ ...P1, name: 'Edited' }, P2, P3])
  })

  it('a new item reads as new, and the dialog clears only its own error on the way out', async () => {
    const { result } = renderHook(() => useHostConfigCollection<HostProject>(H, 'projects'))
    saveProjects.mockRejectedValueOnce(new Error('dialog save failed'))

    act(() => { result.current.openEditor({ id: 'p9', name: '', slug: '', path: '' }) })
    expect(result.current.isNew).toBe(true)
    await act(async () => { result.current.submit({ id: 'p9', name: 'New', slug: 'new', path: '/n' }) })
    expect(result.current.saveError).toMatchObject({ target: 'dialog' })
    act(() => { result.current.closeEditor() })
    expect(result.current.editing).toBeNull()
    expect(result.current.saveError).toBeNull()

    // A list failure is not the dialog's to clear.
    saveProjects.mockRejectedValueOnce(new Error('row action failed'))
    await act(async () => { await result.current.remove('p2') })
    act(() => { result.current.closeEditor() })
    expect(result.current.saveError).toMatchObject({ target: 'list' })
  })

  it('a delete asks first and only the confirmation writes', async () => {
    const { result } = renderHook(() => useHostConfigCollection<HostProject>(H, 'projects'))
    act(() => { result.current.askDelete('p2') })
    expect(result.current.deleting).toBe('p2')
    act(() => { result.current.cancelDelete() })
    expect(result.current.deleting).toBeNull()
    expect(saveProjects).not.toHaveBeenCalled()

    act(() => { result.current.askDelete('p2') })
    await act(async () => { result.current.confirmDelete('p2') })
    expect(result.current.deleting).toBeNull()
    expect(items()).toEqual([P1, P3])
  })

  it('an offline or too-old host is not editable and says why', () => {
    seed([P1], 'unsupported')
    const { result, rerender } = renderHook(() => useHostConfigCollection<HostProject>(H, 'projects'))
    expect(result.current.editable).toBe(false)
    expect(result.current.notice).toMatchObject({ key: 'host_config.unsupported' })

    act(() => { useHostStore.setState({ runtime: { [H]: { status: 'disconnected' } } }) })
    rerender()
    expect(result.current.notice).toMatchObject({ key: 'host_config.offline' })
  })

  it('reports a failure against the surface the action came from', async () => {
    saveProjects.mockRejectedValue(new HostConfigConflictError({ items: [P1], revision: 9 }))
    const { result } = renderHook(() => useHostConfigCollection<HostProject>(H, 'projects'))
    await act(async () => { await result.current.remove('p2') })
    expect(result.current.saveError).toMatchObject({ target: 'list' })
    expect(result.current.saveError?.text).toContain('Changed elsewhere')

    await act(async () => { await result.current.upsert(P1) })
    expect(result.current.saveError).toMatchObject({ target: 'dialog' })
  })
})
