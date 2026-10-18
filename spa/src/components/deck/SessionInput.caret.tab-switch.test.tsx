// Regression (CLAUDE.md tab-hosted rule, #3393): an upload that lands while the reader is on another tab still puts its placeholder where
// the caret was when the file came in. The real TabContent unmounts the input (keepAliveCount 0); the insertion point lives outside it.
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
import { clearAllDrafts, draftKey, readDraft, writeDraft } from '../../lib/conversations/draft-memory'
import { clearAllAttachments } from '../../lib/conversations/attachment-memory'
import { clearAllUploads } from '../../lib/conversations/attachment-upload'
import { clearAllAnchors } from '../../lib/conversations/insertion-anchor'
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
const flush = () => act(async () => { for (let i = 0; i < 4; i++) await Promise.resolve() })

let land: () => void = () => {}

beforeEach(() => {
  URL.createObjectURL = vi.fn(() => 'blob:test/x'); URL.revokeObjectURL = vi.fn()
  cleanup()
  mocks.upload.mockReset().mockImplementation((_h: string, f: File) => new Promise((res) => { land = () => res({ filename: f.name, path: `/up/dev001/${f.name}` }) }))
  clearModuleRegistry()
  registerModule({ id: 'nex', name: 'Nex', panes: [{ kind: 'execution', component: SessionRenderer }] })
  registerModule({ id: 'dashboard', name: 'Dashboard', panes: [{ kind: 'dashboard', component: Other }] })
  useUISettingsStore.setState({ keepAliveCount: 0 })
  useShownHostsStore.setState({ ids: [H] })
  useHostConfigStore.setState({ byHost: {}, ensureLoaded: async () => {} })
})
afterEach(() => { cleanup(); clearAllUploads(); clearAllDrafts(); clearAllAttachments(); clearAllSendQueues(); clearAllAnchors() })

describe('the insertion point across tab switches', () => {
  it('an upload that lands while another tab is shown still lands at the caret, after the edits made meanwhile', async () => {
    const { rerender } = render(<TabContent activeTab={sessionTab} allTabs={all} />)
    fireEvent.change(box(), { target: { value: 'helloworld' } })
    box().setSelectionRange(5, 5)
    fireEvent.drop(screen.getByTestId('session-input-drop'), { dataTransfer: { files: [new File(['x'], 'shot.png', { type: 'image/png' })], types: ['Files'] } })

    rerender(<TabContent activeTab={dashTab} allTabs={all} />)
    expect(screen.queryByTestId('session-input')).toBeNull() // really unmounted
    writeDraft(key, '>> helloworld') // an edit of the draft while the input is away (a recall, an undo...)
    await act(async () => { land(); await Promise.resolve() })
    await flush()
    expect(readDraft(key)).toBe('>> hello [Image #1] world')

    rerender(<TabContent activeTab={sessionTab} allTabs={all} />)
    expect(box().value).toBe('>> hello [Image #1] world')
  })
})
