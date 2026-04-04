// spa/src/lib/connection-state-machine.ts — Pure class, no React dependency
import type { HealthResult } from './host-connection'

const FAST_RETRY_COUNT = 3
const L1_RETRY_DELAY_MS = 100       // L1: 不間斷重連（實際間隔 ≈ 6s timeout + 100ms）
const L2_RETRY_DELAY_MS = 3000      // L2: 每 3 秒嘗試一次
const L2_RETRY_TIMEOUT_MS = 180_000 // L2: 3 分鐘後停止嘗試

export class ConnectionStateMachine {
  private checkFn: () => Promise<HealthResult>
  private onStateChange: (result: HealthResult) => void
  private stopped = false
  private backgroundTimer: ReturnType<typeof setTimeout> | null = null
  private backgroundDeadline: number | null = null
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

    // Classify by last result → background retry
    if (lastResult.daemon === 'unreachable') {
      // L1: 不間斷背景重連（無期限）
      this.backgroundDeadline = null
      this.startBackground(L1_RETRY_DELAY_MS)
    } else if (lastResult.daemon === 'refused') {
      // L2: 每 3 秒嘗試，3 分鐘後停止
      this.backgroundDeadline = Date.now() + L2_RETRY_TIMEOUT_MS
      this.startBackground(L2_RETRY_DELAY_MS)
    }
  }

  /** Start background retry with configurable delay. */
  private startBackground(delayMs: number) {
    if (this.stopped) return
    const myEpoch = this.epoch
    this.backgroundTimer = setTimeout(async () => {
      if (this.stopped || this.epoch !== myEpoch) return

      // L2 deadline check
      if (this.backgroundDeadline && Date.now() >= this.backgroundDeadline) {
        return // 超過期限，停止嘗試
      }

      const result = await this.checkFn()
      if (this.stopped || this.epoch !== myEpoch) return
      this.onStateChange(result)

      if (result.daemon === 'connected') {
        return // recovered
      }
      this.startBackground(delayMs)
    }, delayMs)
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
