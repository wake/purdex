// spa/src/lib/nex/worker-rebuild.test.ts — "rebuild as worker": the request, the
// checked pane swap, the single flight and the refusal messages.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { rebuildAsWorker, rebuildErrorMessage } from './worker-rebuild'
import { HandoffApiError, nexWorkerRebuild } from './handoff-api'
import { useTabStore } from '../../stores/useTabStore'
import { useShownHostsStore } from '../../stores/useShownHostsStore'
import { useNexHostStore } from '../../stores/useNexHostStore'
import { useWorkerSettingsStore } from '../../stores/useWorkerSettingsStore'
import { createTab, type PaneContent } from '../../types/tab'
import { getPrimaryPane } from '../pane-tree'
import en from '../../locales/en.json'

vi.mock('./handoff-api', async (o) => ({ ...(await o<typeof import('./handoff-api')>()), nexWorkerRebuild: vi.fn() }))

const H = 'h', OLD = 'exc_old', NEW = 'exc_new'
const mocked = vi.mocked(nexWorkerRebuild)
const t = (k: string, p?: Record<string, string | number>) =>
  ((en as Record<string, string>)[k] ?? k).replace(/\{\{(\w+)\}\}/g, (_, n) => String(p?.[n]))
const expectOld = (c: PaneContent) => c.kind === 'execution' && c.executionId === OLD

let tabId = '', paneId = ''
const args = (extra = {}) => ({ hostId: H, sessionId: 'S', cwd: '/w', tabId, paneId, expect: expectOld, ...extra })
const paneContent = () => getPrimaryPane(useTabStore.getState().tabs[tabId].layout).content

beforeEach(() => {
  mocked.mockReset().mockResolvedValue({ execution_id: NEW, state: 'running' })
  const tab = createTab({ kind: 'execution', executionId: OLD, host: H })
  tabId = tab.id
  paneId = getPrimaryPane(tab.layout).id
  useTabStore.setState({ tabs: { [tab.id]: tab }, tabOrder: [tab.id], activeTabId: tab.id })
  useShownHostsStore.setState({ ids: [H] })
})

describe('rebuildAsWorker', () => {
  it('posts the body without absent optionals and swaps the pane to the new execution', async () => {
    const out = await rebuildAsWorker(args())
    expect(mocked).toHaveBeenCalledWith(H, { session_id: 'S', cwd: '/w' })
    expect(out.swapped).toBe(true)
    expect(out.result.execution_id).toBe(NEW)
    expect(paneContent()).toMatchObject({ kind: 'execution', executionId: NEW, host: H })
  })

  it('sends profile and replace_execution_id when given', async () => {
    await rebuildAsWorker(args({ profile: 'handoff', replaceExecutionId: OLD }))
    expect(mocked).toHaveBeenCalledWith(H, { session_id: 'S', cwd: '/w', profile: 'handoff', replace_execution_id: OLD })
  })

  // Permission channel plan Task 7: a rebuild keeps the mode — the row's profile is resent as today, and an asking
  // row (handoff_ask) also gets the current timeout, under the same two conditions as a handoff.
  describe('the approval timeout of an asking row', () => {
    const PERMS = { profiles: ['handoff_ask'], answer: { method: 'POST', path: '/api/nex/v1/executions/{id}/permissions/{request_id}' }, timeout: { max_s: 86400 } }
    const seedNex = (permissions: Record<string, unknown>) => useNexHostStore.setState({
      byHost: { [H]: { info: null, capabilities: { sandbox_profiles: ['handoff_ask', 'handoff'], permissions } as never, phase: 'ready', error: null, fetchedAt: 0, generation: 1, fingerprint: 'f' } },
    })
    beforeEach(() => {
      seedNex(PERMS)
      useWorkerSettingsStore.setState({ permissionTimeoutMin: 15 })
    })
    afterEach(() => {
      useWorkerSettingsStore.setState({ permissionTimeoutMin: 0 })
      useNexHostStore.setState({ byHost: {} })
    })

    it('a handoff_ask row resends its profile plus permission_timeout_s', async () => {
      await rebuildAsWorker(args({ profile: 'handoff_ask', replaceExecutionId: OLD }))
      expect(mocked.mock.calls[0][1]).toStrictEqual({ session_id: 'S', cwd: '/w', profile: 'handoff_ask', replace_execution_id: OLD, permission_timeout_s: 900 })
    })

    it('a handoff row sends no timeout', async () => {
      await rebuildAsWorker(args({ profile: 'handoff' }))
      expect(mocked.mock.calls[0][1]).toStrictEqual({ session_id: 'S', cwd: '/w', profile: 'handoff' })
    })

    it('a handoff_ask row sends no timeout when the host lacks permissions.timeout, or the setting is 不逾時', async () => {
      seedNex({ profiles: PERMS.profiles, answer: PERMS.answer })
      await rebuildAsWorker(args({ profile: 'handoff_ask' }))
      expect(mocked.mock.calls[0][1]).toStrictEqual({ session_id: 'S', cwd: '/w', profile: 'handoff_ask' })
      seedNex(PERMS)
      useWorkerSettingsStore.setState({ permissionTimeoutMin: 0 })
      await rebuildAsWorker(args({ profile: 'handoff_ask' }))
      expect(mocked.mock.calls[1][1]).toStrictEqual({ session_id: 'S', cwd: '/w', profile: 'handoff_ask' })
    })
  })

  it('swaps to a rejected execution too (it then shows start failed)', async () => {
    mocked.mockResolvedValue({ execution_id: NEW, state: 'rejected', reject_reason: 'x' })
    const out = await rebuildAsWorker(args())
    expect(out.swapped).toBe(true)
    expect(paneContent()).toMatchObject({ executionId: NEW })
  })

  it('a double call is one request; the second is refused', async () => {
    let release!: () => void
    mocked.mockReturnValue(new Promise((r) => { release = () => r({ execution_id: NEW, state: 'running' }) }))
    const first = rebuildAsWorker(args())
    await expect(rebuildAsWorker(args())).rejects.toMatchObject({ code: 'handoff_in_progress' })
    release()
    await first
    expect(mocked).toHaveBeenCalledTimes(1)
  })

  it('swapped is false when the pane moved on (CAS fails)', async () => {
    const out = await rebuildAsWorker(args({ expect: () => false }))
    expect(out.swapped).toBe(false)
    expect(paneContent()).toMatchObject({ executionId: OLD })
  })

  it('swapped is false when the host is no longer shown', async () => {
    useShownHostsStore.setState({ ids: [] })
    expect((await rebuildAsWorker(args())).swapped).toBe(false)
  })

  it('propagates a refusal', async () => {
    mocked.mockRejectedValue(new HandoffApiError(409, 'session_owned', { owner: 'worker' }))
    await expect(rebuildAsWorker(args())).rejects.toBeInstanceOf(HandoffApiError)
  })
})

describe('rebuildErrorMessage', () => {
  it('maps session_owned by owner', () => {
    expect(rebuildErrorMessage(new HandoffApiError(409, 'session_owned', { owner: 'worker' }), t)).toBe(en['worker.rebuild.owned_worker'])
    expect(rebuildErrorMessage(new HandoffApiError(409, 'session_owned', { owner: 'terminal' }), t)).toBe(en['worker.rebuild.owned_terminal'])
  })
  it('maps anything else to failed', () => {
    expect(rebuildErrorMessage(new HandoffApiError(500, 'delegate_failed', {}), t)).toContain('Rebuild failed')
    expect(rebuildErrorMessage(new Error('boom'), t)).toContain('Rebuild failed')
  })
})
