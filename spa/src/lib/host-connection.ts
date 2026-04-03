// spa/src/lib/host-connection.ts — Health check with L1/L2/L3 classification

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
    const data = await res.json()
    return {
      daemon: 'connected',
      tmux: data.tmux ? 'ok' : 'unavailable',
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
