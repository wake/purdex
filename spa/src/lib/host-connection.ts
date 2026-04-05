// spa/src/lib/host-connection.ts — Health check with two-phase negotiation

export interface HealthResult {
  daemon: 'connected' | 'refused' | 'unreachable' | 'auth-error'
  tmux: 'ok' | 'unavailable'
  latency: number | null
  mode: 'pairing' | 'pending' | 'normal'
  ticket?: string
}

const PHASE1_TIMEOUT_MS = 6000
const PHASE2_TIMEOUT_MS = 5000

export async function checkHealth(
  baseUrl: string,
  getToken?: () => string | undefined,
): Promise<HealthResult> {
  const ctrl1 = new AbortController()
  const timer1 = setTimeout(() => ctrl1.abort(), PHASE1_TIMEOUT_MS)
  try {
    const start = performance.now()
    const res = await fetch(`${baseUrl}/api/health`, { signal: ctrl1.signal })
    const latency = Math.round(performance.now() - start)
    const body = await res.json()
    const mode = (body.mode ?? 'normal') as 'pairing' | 'pending' | 'normal'

    const token = getToken?.()
    if (!token) {
      if (mode === 'pairing') {
        return { daemon: 'connected', tmux: 'unavailable', latency, mode }
      }
      return { daemon: 'auth-error', tmux: 'unavailable', latency, mode }
    }

    const ctrl2 = new AbortController()
    const timer2 = setTimeout(() => ctrl2.abort(), PHASE2_TIMEOUT_MS)
    try {
      const ticketRes = await fetch(`${baseUrl}/api/ws-ticket`, {
        method: 'POST',
        headers: { Authorization: `Bearer ${token}` },
        signal: ctrl2.signal,
      })
      if (ticketRes.status === 401) {
        return { daemon: 'auth-error', tmux: 'unavailable', latency, mode }
      }
      if (ticketRes.status === 503) {
        return { daemon: 'auth-error', tmux: 'unavailable', latency, mode }
      }
      if (!ticketRes.ok) {
        return { daemon: 'connected', tmux: 'unavailable', latency, mode }
      }
      const { ticket } = await ticketRes.json()
      return { daemon: 'connected', tmux: 'unavailable', latency, mode, ticket }
    } catch {
      return { daemon: 'connected', tmux: 'unavailable', latency, mode }
    } finally {
      clearTimeout(timer2)
    }
  } catch (err: unknown) {
    if (err instanceof Error && err.name === 'AbortError') {
      return { daemon: 'unreachable', tmux: 'unavailable', latency: null, mode: 'normal' }
    }
    return { daemon: 'refused', tmux: 'unavailable', latency: null, mode: 'normal' }
  } finally {
    clearTimeout(timer1)
  }
}
