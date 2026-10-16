import { describe, it, expect, beforeEach } from 'vitest'
import { renderHook, act } from '@testing-library/react'
import { useTabDisplay } from './useTabDisplay'
import { useFleetStore, type FleetSnapshot } from '../lib/fleet/store'
import { useI18nStore } from '../stores/useI18nStore'
import { createTab } from '../types/tab'

const snapshot = (alias: string): FleetSnapshot => ({
  hostId: 'p', term: 1, order: ['h1'], hosts: { h1: { id: 'h1', alias } as never },
})

beforeEach(() => {
  useFleetStore.getState().reset()
  useI18nStore.setState({ t: (k: string) => k })
})

describe('useTabDisplay for a watch-host tab', () => {
  const tab = { ...createTab({ kind: 'watch-host', fleetHostId: 'h1' }), id: 'watch-host-h1' }

  it('the label follows the fleet: the snapshot arrives after mount, and a rename is picked up', () => {
    const { result } = renderHook(() => useTabDisplay(tab))
    expect(result.current.displayTitle).toBe('h1') // no snapshot yet: the raw fleet id
    act(() => useFleetStore.setState({ snapshot: snapshot('mlab') }))
    expect(result.current.displayTitle).toBe('mlab')
    act(() => useFleetStore.setState({ snapshot: snapshot('mlab-2') }))
    expect(result.current.displayTitle).toBe('mlab-2')
  })
})
