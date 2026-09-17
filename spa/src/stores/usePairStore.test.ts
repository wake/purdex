import { describe, it, expect, vi, beforeEach } from 'vitest'
import { usePairStore } from './usePairStore'

// Mock the fetch API
global.fetch = vi.fn()

describe('usePairStore', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    usePairStore.setState({ hostList: [], ourAlias: null, error: null, loading: false })
  })

  describe('fetchHostsWithTokenStatus', () => {
    it('fetches hosts and populates hostList', async () => {
      const mockHosts = [
        { host_id: 'h1', name: 'mlab', has_token: true, has_inbound_token: true },
        { host_id: 'h2', name: 'air26', has_token: false, has_inbound_token: true },
      ]
      ;(global.fetch as any).mockResolvedValueOnce({
        ok: true,
        json: async () => mockHosts,
      })

      await usePairStore.getState().fetchHostsWithTokenStatus()

      expect(usePairStore.getState().hostList).toEqual(mockHosts)
      expect(usePairStore.getState().error).toBeNull()
    })

    it('sets error on fetch failure', async () => {
      ;(global.fetch as any).mockResolvedValueOnce({
        ok: false,
        status: 500,
      })

      await usePairStore.getState().fetchHostsWithTokenStatus()

      expect(usePairStore.getState().error).toBeTruthy()
    })
  })

  describe('setOurAlias', () => {
    it('updates ourAlias state', () => {
      usePairStore.getState().setOurAlias('new-alias')
      expect(usePairStore.getState().ourAlias).toBe('new-alias')
    })
  })

  describe('adoptAlias', () => {
    it('posts new alias and updates store', async () => {
      ;(global.fetch as any).mockResolvedValueOnce({
        ok: true,
        json: async () => ({ alias: 'adopted-alias' }),
      })

      await usePairStore.getState().adoptAlias('h1', 'adopted-alias')

      expect(global.fetch).toHaveBeenCalledWith(
        '/api/peers/h1/adopt',
        expect.objectContaining({
          method: 'POST',
          body: JSON.stringify({ alias: 'adopted-alias' }),
        })
      )
      expect(usePairStore.getState().ourAlias).toBe('adopted-alias')
    })

    it('sets error on failure', async () => {
      ;(global.fetch as any).mockResolvedValueOnce({
        ok: false,
        status: 400,
      })

      await usePairStore.getState().adoptAlias('h1', 'bad-alias')

      expect(usePairStore.getState().error).toBeTruthy()
    })
  })

  describe('verifyPeer', () => {
    it('calls remote peer verify endpoint and returns true on success', async () => {
      ;(global.fetch as any).mockResolvedValueOnce({
        ok: true,
        json: async () => ({ verified: true, our_alias: 'my-alias' }),
      })

      const result = await usePairStore.getState().verifyPeer('h2', 'token-123')

      expect(result).toBe(true)
      expect(global.fetch).toHaveBeenCalledWith(
        expect.stringContaining('/api/peers/verify'),
        expect.objectContaining({
          method: 'POST',
          headers: expect.objectContaining({
            Authorization: 'Bearer token-123',
          }),
        })
      )
    })

    it('returns false on verification failure', async () => {
      ;(global.fetch as any).mockResolvedValueOnce({
        ok: false,
        status: 401,
      })

      const result = await usePairStore.getState().verifyPeer('h2', 'bad-token')

      expect(result).toBe(false)
    })
  })
})
