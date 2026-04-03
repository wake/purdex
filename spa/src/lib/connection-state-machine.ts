// spa/src/lib/connection-state-machine.ts — Pure class, no React dependency
import type { HealthResult } from './host-connection'

const FAST_RETRY_COUNT = 3

export class ConnectionStateMachine {
  private checkFn: () => Promise<HealthResult>
  private onStateChange: (result: HealthResult) => void
  private stopped = false
  private backgroundTimer: ReturnType<typeof setTimeout> | null = null
  private epoch = 0

  constructor(
    checkFn: () => Promise<HealthResult>,
    onStateChange: (result: HealthResult) => void,
  ) {
    this.checkFn = checkFn
    this.onStateChange = onStateChange
  }

  /** Trigger a FAST_RETRY cycle (called on WS close or manual retry). */
  async trigger(): Promise<void> {
    this.stopBackground()
    this.epoch++
    const myEpoch = this.epoch
    this.stopped = false

    let lastResult: HealthResult | null = null

    // FAST_RETRY: up to 3 immediate attempts
    for (let i = 0; i < FAST_RETRY_COUNT; i++) {
      if (this.stopped || this.epoch !== myEpoch) return
      lastResult = await this.checkFn()
      if (this.stopped || this.epoch !== myEpoch) return
      this.onStateChange(lastResult)

      if (lastResult.daemon === 'connected') {
        return // recovered
      }
    }

    if (!lastResult || this.stopped || this.epoch !== myEpoch) return

    // Classify by last result
    if (lastResult.daemon === 'unreachable') {
      // L1: background continuous retry
      this.startBackground()
    }
    // L2 (refused): stop — no background retry
  }

  /** Start background continuous retry for L1. */
  private startBackground() {
    if (this.stopped) return
    const myEpoch = this.epoch
    this.backgroundTimer = setTimeout(async () => {
      if (this.stopped || this.epoch !== myEpoch) return
      const result = await this.checkFn()
      if (this.stopped || this.epoch !== myEpoch) return
      this.onStateChange(result)

      if (result.daemon === 'connected') {
        return // recovered
      }
      this.startBackground()
    }, 100)
  }

  private stopBackground() {
    if (this.backgroundTimer) {
      clearTimeout(this.backgroundTimer)
      this.backgroundTimer = null
    }
  }

  stop() {
    this.stopped = true
    this.stopBackground()
  }
}
