import { describe, it, expect, vi, beforeEach } from 'vitest'

const getJson = vi.fn()
vi.mock('./handoff-api', async (orig) => ({ ...(await orig<typeof import('./handoff-api')>()), getJson: (h: string, p: string) => getJson(h, p) }))
import { listConversations } from './conversations-api'

describe('listConversations URL', () => {
  beforeEach(() => { getJson.mockReset(); getJson.mockResolvedValue({ conversations: [] }) })
  it('has no scope param by default', async () => {
    await listConversations('h', 'ended')
    expect(getJson).toHaveBeenCalledWith('h', '/api/nex/conversations?state=ended')
  })
  it('appends scope=test / scope=normal', async () => {
    await listConversations('h', 'gone', 'test')
    expect(getJson).toHaveBeenLastCalledWith('h', '/api/nex/conversations?state=gone&scope=test')
    await listConversations('h', 'ended', 'normal')
    expect(getJson).toHaveBeenLastCalledWith('h', '/api/nex/conversations?state=ended&scope=normal')
  })
})
