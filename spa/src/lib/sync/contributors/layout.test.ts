// =============================================================================
// Sync Architecture — LayoutContributor Tests
// =============================================================================

import { describe, it, expect, beforeEach } from 'vitest'
import { createLayoutContributor } from './layout'
import { useLayoutStore } from '../../../stores/useLayoutStore'
import type { FullPayload } from '../types'

// ---------------------------------------------------------------------------
// Default state
// ---------------------------------------------------------------------------

const DEFAULT_REGIONS = {
  'primary-sidebar': { views: [], width: 240, mode: 'collapsed' as const },
  'primary-panel': { views: [], width: 200, mode: 'collapsed' as const },
  'secondary-panel': { views: [], width: 200, mode: 'collapsed' as const },
  'secondary-sidebar': { views: [], width: 240, mode: 'collapsed' as const },
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

function resetStore() {
  useLayoutStore.setState({ regions: DEFAULT_REGIONS })
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

describe('createLayoutContributor', () => {
  let contributor: ReturnType<typeof createLayoutContributor>

  beforeEach(() => {
    resetStore()
    contributor = createLayoutContributor()
  })

  // -------------------------------------------------------------------------
  // Identity & strategy
  // -------------------------------------------------------------------------

  it('has id "layout"', () => {
    expect(contributor.id).toBe('layout')
  })

  it('has strategy "full"', () => {
    expect(contributor.strategy).toBe('full')
  })

  // -------------------------------------------------------------------------
  // getVersion
  // -------------------------------------------------------------------------

  it('getVersion returns 1', () => {
    expect(contributor.getVersion()).toBe(1)
  })

  // -------------------------------------------------------------------------
  // serialize
  // -------------------------------------------------------------------------

  it('serialize returns FullPayload with version 1', () => {
    const payload = contributor.serialize() as FullPayload
    expect(payload.version).toBe(1)
    expect(payload.data).toBeDefined()
  })

  it('serialize only includes expected data fields (no functions)', () => {
    const payload = contributor.serialize() as FullPayload
    const keys = Object.keys(payload.data)

    expect(keys).toContain('regions')

    // Must NOT contain action functions
    expect(keys).not.toContain('setRegionMode')
    expect(keys).not.toContain('setRegionWidth')
    expect(keys).not.toContain('toggleRegion')
    expect(keys).not.toContain('reconcileViews')

    // All values must be non-function
    for (const key of keys) {
      expect(typeof payload.data[key]).not.toBe('function')
    }
  })

  it('serialize reflects current store state', () => {
    useLayoutStore.getState().setRegionMode('primary-sidebar', 'pinned')
    const payload = contributor.serialize() as FullPayload
    const regions = payload.data.regions as Record<string, { mode: string }>
    expect(regions['primary-sidebar'].mode).toBe('pinned')
  })

  // -------------------------------------------------------------------------
  // deserialize — full-replace
  // -------------------------------------------------------------------------

  it('deserialize with full-replace overwrites store state', () => {
    const remoteRegions = {
      'primary-sidebar': { views: ['view-a'], width: 300, mode: 'pinned' as const },
      'primary-panel': { views: [], width: 200, mode: 'collapsed' as const },
      'secondary-panel': { views: [], width: 200, mode: 'collapsed' as const },
      'secondary-sidebar': { views: [], width: 240, mode: 'hidden' as const },
    }

    const incoming: FullPayload = {
      version: 1,
      data: { regions: remoteRegions },
    }

    contributor.deserialize(incoming, { type: 'full-replace' })

    const state = useLayoutStore.getState()
    expect(state.regions['primary-sidebar'].mode).toBe('pinned')
    expect(state.regions['primary-sidebar'].width).toBe(300)
    expect(state.regions['secondary-sidebar'].mode).toBe('hidden')
  })

  // -------------------------------------------------------------------------
  // deserialize — field-merge
  // -------------------------------------------------------------------------

  it('deserialize with field-merge only applies resolved remote fields', () => {
    useLayoutStore.setState({
      regions: {
        ...DEFAULT_REGIONS,
        'primary-sidebar': { views: ['local-view'], width: 240, mode: 'collapsed' as const },
      },
    })

    const incoming: FullPayload = {
      version: 1,
      data: {
        regions: {
          'primary-sidebar': { views: ['remote-view'], width: 350, mode: 'pinned' as const },
        },
      },
    }

    contributor.deserialize(incoming, {
      type: 'field-merge',
      resolved: { regions: 'remote' },
    })

    const state = useLayoutStore.getState()
    expect(state.regions['primary-sidebar'].mode).toBe('pinned')
    expect(state.regions['primary-sidebar'].width).toBe(350)
  })

  it('deserialize with field-merge keeps local when resolved local', () => {
    useLayoutStore.setState({
      regions: {
        ...DEFAULT_REGIONS,
        'primary-sidebar': { views: ['local-view'], width: 240, mode: 'collapsed' as const },
      },
    })

    const incoming: FullPayload = {
      version: 1,
      data: {
        regions: {
          'primary-sidebar': { views: ['remote-view'], width: 350, mode: 'pinned' as const },
        },
      },
    }

    contributor.deserialize(incoming, {
      type: 'field-merge',
      resolved: { regions: 'local' },
    })

    const state = useLayoutStore.getState()
    // regions should remain local (unchanged)
    expect(state.regions['primary-sidebar'].mode).toBe('collapsed')
    expect(state.regions['primary-sidebar'].width).toBe(240)
  })
})
