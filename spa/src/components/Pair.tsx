import { useEffect, useState } from 'react'
import { usePairStore, type HostWithTokenStatus, type PeerEnvelope } from '../stores/usePairStore'

export function Pair() {
  const { hostList, ourAlias, fetchHostsWithTokenStatus, adoptAlias, setOurAlias } = usePairStore()
  const [driftedAlias, setDriftedAlias] = useState<string | null>(null)
  const [loading, setLoading] = useState(false)

  useEffect(() => {
    const load = async () => {
      setLoading(true)
      await fetchHostsWithTokenStatus()

      // Check drift: fetch self envelope and compare alias
      try {
        const response = await fetch('/api/peers/self/envelope')
        if (response.ok) {
          const envelope: PeerEnvelope = await response.json()
          setOurAlias(envelope.alias)

          // Detect drift
          if (ourAlias && ourAlias !== envelope.alias) {
            setDriftedAlias(envelope.alias)
          }
        }
      } catch (err) {
        console.error('Failed to fetch self envelope:', err)
      }

      setLoading(false)
    }

    load()
  }, [fetchHostsWithTokenStatus, ourAlias, setOurAlias])

  const hostsWithToken = hostList.filter((h) => h.has_token)
  const peerListingOnlyHosts = hostList.filter((h) => !h.has_inbound_token)

  const handleAdoptAlias = async () => {
    if (!driftedAlias || !hostsWithToken.length) return
    const targetHost = hostsWithToken[0]
    await adoptAlias(targetHost.host_id, driftedAlias)
    setDriftedAlias(null)
  }

  return (
    <div className="flex-1 flex flex-col gap-4 p-4">
      <h1 className="text-2xl font-bold">Pairing</h1>

      {loading && <div className="text-text-secondary">Loading...</div>}

      <div className="grid grid-cols-2 gap-4 flex-1">
        {/* Section A: Hosts with token */}
        <div className="border border-border rounded-lg p-4">
          <h2 className="text-lg font-semibold mb-4">Hosts with token</h2>
          {hostsWithToken.length === 0 ? (
            <p className="text-text-secondary text-sm">No hosts with token</p>
          ) : (
            <ul className="space-y-2">
              {hostsWithToken.map((host) => (
                <li key={host.host_id} className="text-sm flex items-center justify-between">
                  <span>{host.name}</span>
                  <span className="text-text-tertiary">✓</span>
                </li>
              ))}
            </ul>
          )}

          {/* Drift indicator and adopt button */}
          {driftedAlias && (
            <div className="mt-4 p-3 bg-yellow-500/10 border border-yellow-500/30 rounded">
              <p className="text-xs text-yellow-700 dark:text-yellow-300 mb-2">
                Drift detected: {ourAlias} → {driftedAlias}
              </p>
              <button
                onClick={handleAdoptAlias}
                disabled={!driftedAlias}
                className="w-full px-2 py-1 text-xs bg-yellow-600 hover:bg-yellow-700 disabled:opacity-50 disabled:cursor-not-allowed text-white rounded transition"
              >
                Adopt new alias
              </button>
            </div>
          )}
        </div>

        {/* Section B: Peer-listing-only hosts */}
        <div className="border border-border rounded-lg p-4">
          <h2 className="text-lg font-semibold mb-4">Peer-listing-only hosts</h2>
          {peerListingOnlyHosts.length === 0 ? (
            <p className="text-text-secondary text-sm">No peer-listing-only hosts</p>
          ) : (
            <ul className="space-y-2">
              {peerListingOnlyHosts.map((host) => (
                <li key={host.host_id} className="text-sm flex items-center justify-between">
                  <span>{host.name}</span>
                  <span className="text-text-tertiary">○</span>
                </li>
              ))}
            </ul>
          )}
        </div>
      </div>
    </div>
  )
}
