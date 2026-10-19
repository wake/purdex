// The status bar's peer id shows the person (name pool v2 spec §9.3): `[avatar] Name` where it showed `name [ref]`; the tooltip and the copy action keep the
// full address and ref, because what gets pasted into a handoff hours later must not be the prettier string. A peer that is no persona reads as it always did.
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, cleanup, waitFor } from '@testing-library/react'
import { StatusBar } from './StatusBar'
import { usePersonaStore } from '../stores/usePersonaStore'
import { useUploadStore } from '../stores/useUploadStore'
import { makeCollection, makePersona } from '../lib/names/__fixtures__/personas'
import { copyTextMock, setupStores, seedPeers, seedCwd, sessionTab } from './StatusBar.test-helpers'

vi.mock('../lib/copy-text', () => ({ copyText: vi.fn(async () => {}) }))

function seedPersona(code: string, avatar = '') {
  const c = makeCollection([code])
  c.items[0] = makePersona(code, { name: 'Beta', avatar_data: avatar })
  usePersonaStore.setState({ byHost: { h1: { status: 'ready', collection: c } } })
}

beforeEach(() => {
  cleanup()
  setupStores()
  useUploadStore.setState({ sessions: {} })
  usePersonaStore.setState({ byHost: {} })
  seedPeers()
  seedCwd('/Users/wake/Workspace/wake/purdex')
})

describe('the status bar peer id of a persona', () => {
  it('shows the face and the name, and keeps the address with its ref in the tooltip', () => {
    seedPersona('b0', 'data:image/png;base64,AA==') // the fixture peer is `mlab/purdex-b0 [q34psn]`
    render(<StatusBar activeTab={sessionTab()} />)
    const seg = screen.getByTestId('status-seg-peer-id')
    expect(seg.textContent).toBe('Beta')
    expect(seg.querySelector('img')).not.toBeNull()
    expect(seg.getAttribute('title')).toContain('mlab/purdex-b0 [q34psn]')
    // the name must not hide the segment's own tooltip behind an address-only one
    expect(seg.querySelector('[title]')).toBeNull()
  })

  it('copies the full address and ref, not the name', async () => {
    seedPersona('b0')
    render(<StatusBar activeTab={sessionTab()} />)
    fireEvent.click(screen.getByTestId('status-seg-peer-id'))
    await waitFor(() => expect(copyTextMock).toHaveBeenCalledWith('mlab/purdex-b0 [q34psn]'))
  })

  it('a peer that is no persona keeps the name with its ref', () => {
    seedPersona('zzz')
    render(<StatusBar activeTab={sessionTab()} />)
    expect(screen.getByTestId('status-seg-peer-id').textContent).toBe('purdex-b0 [q34psn]')
  })
})
