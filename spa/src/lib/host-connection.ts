// spa/src/lib/host-connection.ts — Health check with L1/L2/L3 classification
//
// /api/health is a pure liveness endpoint (no auth, returns {"ok": true}).
// tmux status is NOT included here — it comes via /api/ready (behind auth)
// or via host-events WS "tmux" event during normal operation.

export interface HealthResult {
  daemon: 'connected' | 'refused' | 'unreachable'
  tmux: 'ok' | 'unavailable'
  latency: number | null
}

const HEALTH_TIMEOUT_MS = 3000

export async function checkHealth(baseUrl: string): Promise<HealthResult> {
  const controller = new AbortController()
  const timer = setTimeout(() => controller.abort(), HEALTH_TIMEOUT_MS)

  try {
    const start = performance.now()
    const res = await fetch(`${baseUrl}/api/health`, { signal: controller.signal })
    const latency = Math.round(performance.now() - start)
    await res.json() // consume body
    return {
      daemon: 'connected',
      // tmux status unknown from health — will be updated via WS or /api/ready
      tmux: 'unavailable',
      latency,
    }
  } catch (err: unknown) {
    if (err instanceof Error && err.name === 'AbortError') {
      return { daemon: 'unreachable', tmux: 'unavailable', latency: null }
    }
    return { daemon: 'refused', tmux: 'unavailable', latency: null }
  } finally {
    clearTimeout(timer)
  }
}
