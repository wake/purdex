import { create } from 'zustand'

export interface HostWithTokenStatus {
  host_id: string
  name: string
  has_token: boolean
  has_inbound_token: boolean
}

export interface PeerEnvelope {
  host_id: string
  alias: string
}

interface PairStore {
  ourAlias: string | null
  hostList: HostWithTokenStatus[]
  error: string | null
  loading: boolean
  fetchHostsWithTokenStatus: () => Promise<void>
  setOurAlias: (alias: string) => void
  adoptAlias: (hostId: string, newAlias: string) => Promise<void>
  verifyPeer: (hostId: string, inboundToken: string) => Promise<boolean>
}

export const usePairStore = create<PairStore>((set) => ({
  ourAlias: null,
  hostList: [],
  error: null,
  loading: false,

  fetchHostsWithTokenStatus: async () => {
    set({ loading: true, error: null })
    try {
      const response = await fetch('/api/peers/hosts')
      if (!response.ok) {
        set({ error: `Failed to fetch hosts: ${response.status}`, loading: false })
        return
      }
      const hostList = await response.json()
      set({ hostList, loading: false })
    } catch (err) {
      set({ error: String(err), loading: false })
    }
  },

  setOurAlias: (alias) => {
    set({ ourAlias: alias })
  },

  adoptAlias: async (hostId, newAlias) => {
    set({ error: null })
    try {
      const response = await fetch(`/api/peers/${hostId}/adopt`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ alias: newAlias }),
      })
      if (!response.ok) {
        set({ error: `Failed to adopt alias: ${response.status}` })
        return
      }
      const result = await response.json()
      set({ ourAlias: result.alias })
    } catch (err) {
      set({ error: String(err) })
    }
  },

  verifyPeer: async (hostId, inboundToken) => {
    try {
      // Get host info to construct the URL
      const state = usePairStore.getState()
      const host = state.hostList.find((h) => h.host_id === hostId)
      if (!host) {
        return false
      }

      // Call remote peer's verify endpoint
      const response = await fetch(`https://${host.name}/api/peers/verify`, {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
          Authorization: `Bearer ${inboundToken}`,
        },
      })
      if (!response.ok) {
        return false
      }
      const result = await response.json()
      return result.verified === true
    } catch (err) {
      console.error('Verify peer error:', err)
      return false
    }
  },
}))
