// Regression (CLAUDE.md tab-hosted rule): the attachments of the session input - the inserted text and the "attached" chips -
// survive switching to another tab and back. The real TabContent unmounts the pane (keepAliveCount 0).
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { render, screen, fireEvent, cleanup, act } from '@testing-library/react'
import { TabContent } from '../TabContent'
import { SessionInput } from './SessionInput'
import { registerModule, clearModuleRegistry, type PaneRendererProps } from '../../lib/module-registry'
import { useUISettingsStore } from '../../stores/useUISettingsStore'
import { useShownHostsStore } from '../../stores/useShownHostsStore'
import { useHostConfigStore } from '../../stores/useHostConfigStore'
import { createTab } from '../../types/tab'
import type { Tab } from '../../types/tab'
import { clearAllDrafts, draftKey, readDraft } from '../../lib/conversations/draft-memory'
import { clearAllAttachments, readAttachments } from '../../lib/conversations/attachment-memory'
import { clearAllSendQueues } from '../../lib/conversations/send-queue'

const mocks = vi.hoisted(() => ({ fetch: vi.fn(), upload: vi.fn() }))
vi.mock('../../lib/host-api', () => ({ pinnedHostFetch: mocks.fetch, agentUploadToPath: mocks.upload }))

const H = 'h', SID = '11111111-2222-4333-8444-555555555555'

function SessionRenderer({ pane }: PaneRendererProps) {
  return <SessionInput paneKey={pane.id} hostId={H} sessionId={SID} sessionCode="dev001" capabilities={{ send: 'prompt' }} onSwitchToTerminal={() => {}} />
}
const Other = () => <div data-testid="other-tab" />

const sessionTab: Tab = { ...createTab({ kind: 'execution', executionId: 'exc_1', host: H }), id: 't-session' }
const dashTab: Tab = { ...createTab({ kind: 'dashboard' }), id: 't-dash' }
const all = [sessionTab, dashTab]
const box = () => screen.getByRole('textbox') as HTMLTextAreaElement
const paneId = (sessionTab.layout as { pane: { id: string } }).pane.id
const key = draftKey(paneId, H, SID)

beforeEach(() => {
  cleanup()
  mocks.upload.mockReset().mockImplementation((_h: string, f: File) => Promise.resolve({ filename: f.name, path: `/up/dev001/${f.name}` }))
  clearModuleRegistry()
  registerModule({ id: 'nex', name: 'Nex', panes: [{ kind: 'execution', component: SessionRenderer }] })
  registerModule({ id: 'dashboard', name: 'Dashboard', panes: [{ kind: 'dashboard', component: Other }] })
  useUISettingsStore.setState({ keepAliveCount: 0 })
  useShownHostsStore.setState({ ids: [H] })
  useHostConfigStore.setState({ byHost: {}, ensureLoaded: async () => {} })
})
afterEach(() => { cleanup(); clearAllDrafts(); clearAllAttachments(); clearAllSendQueues() })

describe('session input attachments across tab switches', () => {
  it('keeps the draft text and the chips when the reader switches to another tab and back', async () => {
    const { rerender } = render(<TabContent activeTab={sessionTab} allTabs={all} />)
    fireEvent.change(box(), { target: { value: 'look' } })
    const file = new File(['x'], 'shot.png', { type: 'image/png' })
    fireEvent.paste(box(), { clipboardData: { files: [file], getData: () => '', types: ['Files'] } })
    await act(async () => { await Promise.resolve(); await Promise.resolve() })
    expect(screen.getByTestId('attachment-chip')).toBeInTheDocument()

    rerender(<TabContent activeTab={dashTab} allTabs={all} />)
    expect(screen.queryByTestId('session-input')).toBeNull() // really unmounted
    expect(readDraft(key)).toBe('look\n[Image: source: /up/dev001/shot.png]')
    expect(readAttachments(key)).toHaveLength(1)

    rerender(<TabContent activeTab={sessionTab} allTabs={all} />)
    expect(box().value).toBe('look\n[Image: source: /up/dev001/shot.png]')
    expect(screen.getByTestId('attachment-chip')).toHaveTextContent('shot.png')
  })
})
