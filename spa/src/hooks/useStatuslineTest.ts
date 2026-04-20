import { useCallback, useEffect, useRef, useState } from 'react'
import { hostFetch } from '../lib/host-api'
import { statuslineTestBus } from '../lib/statusline-test-bus'
import { debugStatuslineTest } from '../lib/statusline-test-debug'
import { useAgentStore } from '../stores/useAgentStore'
import { compositeKey } from '../lib/composite-key'

export type StageStatus = 'untested' | 'running' | 'passed' | 'failed' | 'skipped'

export interface StageState {
  status: StageStatus
  elapsedMs?: number
  error?: string
}

export interface StagesState {
  1: StageState
  2: StageState
  3: StageState
  4: StageState
  5: StageState
}

export interface StatuslineTestState {
  stages: StagesState
  running: boolean
  lastRunAt: number | null
  nonce: string | null
}

const INITIAL: StatuslineTestState = {
  stages: {
    1: { status: 'untested' },
    2: { status: 'untested' },
    3: { status: 'untested' },
    4: { status: 'untested' },
    5: { status: 'untested' },
  },
  running: false,
  lastRunAt: null,
  nonce: null,
}

// Server per-stage deadline is 2s × 3 = 6s worst case (stage1 exec timeout +
// stage2 + stage3 channel waits). Giving the client budget a cushion above
// that prevents spurious "timeout" failures on loaded systems where the
// proxy subprocess is slow to spawn.
//
// Note: this bounds only the SSE round-trip (`work`). The post-SSE stage-4
// grace wait (STAGE4_GRACE_MS) runs after `work` resolves, so the absolute
// worst-case run duration is OVERALL_TIMEOUT_MS + STAGE4_GRACE_MS = 10s.
const OVERALL_TIMEOUT_MS = 8000

// After SSE completes, wait this long for the WS-delivered agent.status to
// reach the dispatcher. SSE and WS travel over separate sockets, so the server
// can finish signalling stage 3 + done before the broadcast message traverses
// the WS writer pump → network → browser → dispatcher. Without this grace
// window the bus subscriber gets torn down prematurely and stages 4/5 spin
// forever.
const STAGE4_GRACE_MS = 2000

// Stage 4 lifecycle:
//   pending   → initial; stage 3 hasn't emitted yet
//   armed     → stage 3 passed, we're now waiting for the bus event
//   fired     → bus or early-hit resolved stage 4 (success path)
//   cancelled → upstream failure short-circuited the pipeline
// Keeping this as a single union (instead of a boolean pair) makes the
// post-SSE grace guard a single equality check and prevents the two flags
// from drifting out of sync when transitions fire in unexpected orders.
type Stage4State = 'pending' | 'armed' | 'fired' | 'cancelled'

type StageNum = 1 | 2 | 3 | 4 | 5

interface ServerStageEvent {
  type: 'init' | 'stage' | 'done'
  stage?: number
  status?: 'passed' | 'failed'
  elapsed_ms?: number
  error?: string
  nonce?: string
}

export function useStatuslineTest(hostId: string) {
  const [state, setState] = useState<StatuslineTestState>(INITIAL)
  const mountedRef = useRef(true)

  useEffect(() => () => { mountedRef.current = false }, [])

  const runningRef = useRef(false)

  const run = useCallback(async () => {
    if (!mountedRef.current) return
    // Re-entrancy guard: caller might skip the button's disabled state
    // (programmatic calls, double-click races). Drop the second call instead
    // of interleaving two runs over the same state slot.
    if (runningRef.current) return
    runningRef.current = true

    setState({
      ...INITIAL,
      running: true,
      stages: {
        1: { status: 'running' },
        2: { status: 'untested' },
        3: { status: 'untested' },
        4: { status: 'untested' },
        5: { status: 'untested' },
      },
    })

    let unsubBus: (() => void) | null = null
    let reader: ReadableStreamDefaultReader<Uint8Array> | null = null
    let activeNonce: string | null = null

    // stage4Signal resolves when the bus subscriber fires (or the early-hit
    // fallback catches a pre-subscribe store entry). The post-SSE grace block
    // races this against STAGE4_GRACE_MS; if the timeout wins we know the WS
    // event is missing and can fail stage 4 with a real error instead of
    // leaving a permanent spinner.
    let stage4State: Stage4State = 'pending'
    let stage4Resolver: (() => void) | null = null
    const stage4Signal = new Promise<void>((resolve) => { stage4Resolver = resolve })
    const fireStage4 = () => {
      // Idempotent — bus subscriber + early-hit may both invoke this; only
      // the first transition out of pending/armed actually resolves the signal.
      if (stage4State === 'fired' || stage4State === 'cancelled') return
      stage4State = 'fired'
      const r = stage4Resolver
      stage4Resolver = null
      r?.()
    }

    const markStage = (n: StageNum, patch: StageState) => {
      if (!mountedRef.current) return
      setState((s) => ({ ...s, stages: { ...s.stages, [n]: patch } }))
    }

    const markFailThenSkipRest = (failedAt: StageNum, reason: string) => {
      // Any failure short-circuits the rest of the pipeline — suppress the
      // post-SSE stage-4 grace wait so we don't hang waiting for a bus event
      // that will never come. Don't clobber an already-fired state, though:
      // the grace-timeout path calls this *after* the bus might have fired.
      if (stage4State !== 'fired') stage4State = 'cancelled'
      if (!mountedRef.current) return
      setState((s) => {
        const next = { ...s.stages }
        next[failedAt] = { status: 'failed', error: reason }
        for (let n = (failedAt + 1) as StageNum; n <= 5; n = (n + 1) as StageNum) {
          if (next[n].status !== 'passed') next[n] = { status: 'skipped' }
        }
        return { ...s, stages: next }
      })
    }

    const armNonce = async (noncedVal: string, sendReadyAck: boolean) => {
      activeNonce = noncedVal
      debugStatuslineTest('hook.stage1-passed', { nonce: noncedVal, hostId, phase: sendReadyAck ? 'init' : 'stage1-fallback' })
      setState((s) => ({ ...s, nonce: noncedVal }))
      unsubBus = statuslineTestBus.subscribe(noncedVal, ({ nonce: got, hostId: eventHostId }) => {
        debugStatuslineTest('hook.bus-callback', { nonce: got, eventHostId, mounted: mountedRef.current })
        if (eventHostId !== hostId) return
        if (!mountedRef.current) return
        markStage(4, { status: 'passed' })
        const key = compositeKey(hostId, got)
        const hasStoreEntry = !!useAgentStore.getState().ccStatus[key]
        debugStatuslineTest('hook.stage5-check', { key, hasStoreEntry })
        if (hasStoreEntry) {
          markStage(5, { status: 'passed' })
        }
        fireStage4()
      })
      const earlyKey = compositeKey(hostId, noncedVal)
      const earlyHit = !!useAgentStore.getState().ccStatus[earlyKey]
      debugStatuslineTest('hook.early-hit-check', { earlyKey, earlyHit })
      if (earlyHit) {
        markStage(4, { status: 'passed' })
        markStage(5, { status: 'passed' })
        fireStage4()
        unsubBus?.()
        unsubBus = null
      }
      if (!sendReadyAck) return true
      hostFetch(hostId, '/api/agent/cc/statusline/test/ready', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ nonce: noncedVal }),
        })
        .then((ready) => {
        if (!ready.ok) {
          debugStatuslineTest('hook.ready-ack-fallback', { nonce: noncedVal, status: ready.status })
        }
        })
        .catch((err) => {
          debugStatuslineTest('hook.ready-ack-fallback', {
            nonce: noncedVal,
            error: err instanceof Error ? err.message : String(err),
          })
        })
      return true
    }

    const work = (async () => {
      const res = await hostFetch(hostId, '/api/agent/cc/statusline/test', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ client_protocol: 'ready-v1' }),
      })
      if (!res.ok || !res.body) {
        markFailThenSkipRest(1, `HTTP ${res.status}`)
        return
      }
      reader = res.body.getReader()
      const decoder = new TextDecoder()
      let buffer = ''
      for (;;) {
        const { done, value } = await reader.read()
        if (done) break
        buffer += decoder.decode(value, { stream: true })
        const parts = buffer.split('\n\n')
        buffer = parts.pop() ?? ''
        for (const part of parts) {
          const dataLine = part.split('\n').find((l) => l.startsWith('data: '))
          if (!dataLine) continue
          let ev: ServerStageEvent
          try { ev = JSON.parse(dataLine.slice(6)) } catch { continue }
          if (ev.type === 'init' && ev.nonce) {
            const ok = await armNonce(ev.nonce, true)
            if (!ok) {
              return
            }
            continue
          }
          if (ev.type === 'done') return
          if (ev.type !== 'stage' || !ev.stage || ev.stage < 1 || ev.stage > 3) continue

          const n = ev.stage as 1 | 2 | 3
          if (ev.status === 'failed') {
            markFailThenSkipRest(n, ev.error ?? 'stage failed')
            return
          }
          markStage(n, { status: 'passed', elapsedMs: ev.elapsed_ms })

          if (n === 1) {
            if (ev.nonce && !activeNonce) {
              const ok = await armNonce(ev.nonce, false)
              if (!ok) return
            }
            markStage(2, { status: 'running' })
          } else if (n === 2) {
            setState((s) => s.stages[3].status === 'untested'
              ? { ...s, stages: { ...s.stages, 3: { status: 'running' } } }
              : s)
          } else if (n === 3) {
            // Only arm the post-SSE grace wait if stage 4 hasn't already
            // fired via early-hit / racing bus event — otherwise we'd block
            // on a signal that already resolved to no-op and waste the
            // grace window.
            if (stage4State === 'pending') stage4State = 'armed'
            setState((s) => {
              const next = { ...s.stages }
              if (next[4].status === 'untested') next[4] = { status: 'running' }
              if (next[5].status === 'untested') next[5] = { status: 'running' }
              return { ...s, stages: next }
            })
          }
        }
      }
    })()

    let timeoutId: ReturnType<typeof setTimeout> | undefined
    const timeout = new Promise<'timeout'>((resolve) => {
      timeoutId = setTimeout(() => resolve('timeout'), OVERALL_TIMEOUT_MS)
    })

    const outcome = await Promise.race([work.then(() => 'done' as const), timeout])
    if (timeoutId !== undefined) clearTimeout(timeoutId)

    if (outcome === 'timeout') {
      // Release the SSE reader so the browser closes the HTTP connection;
      // otherwise the fetch stream stays open until the server finishes
      // or its own timeouts fire.
      reader?.cancel().catch(() => { /* already closed — ignore */ })
    }

    if (outcome === 'timeout' && mountedRef.current) {
      setState((s) => {
        const next = { ...s.stages }
        let firstPending: StageNum | null = null
        for (let n = 1 as StageNum; n <= 5; n = (n + 1) as StageNum) {
          if (next[n].status !== 'passed') { firstPending = n; break }
        }
        if (firstPending) {
          next[firstPending] = { status: 'failed', error: `timeout at stage ${firstPending}` }
          for (let n = (firstPending + 1) as StageNum; n <= 5; n = (n + 1) as StageNum) {
            if (next[n].status !== 'passed') next[n] = { status: 'skipped' }
          }
        }
        return { ...s, stages: next }
      })
    }

    debugStatuslineTest('hook.sse-outcome', { outcome, stage4State, mounted: mountedRef.current })

    // SSE finished cleanly but stage 4 is still pending → give the WS a
    // grace window, otherwise the spinner for stages 4/5 would hang forever.
    if (outcome === 'done' && mountedRef.current && stage4State === 'armed') {
      debugStatuslineTest('hook.grace-start', { graceMs: STAGE4_GRACE_MS })
      let graceId: ReturnType<typeof setTimeout> | undefined
      const graceTimeout = new Promise<'grace-timeout'>((resolve) => {
        graceId = setTimeout(() => resolve('grace-timeout'), STAGE4_GRACE_MS)
      })
      const graceOutcome = await Promise.race([
        stage4Signal.then(() => 'fired' as const),
        graceTimeout,
      ])
      if (graceId !== undefined) clearTimeout(graceId)
      debugStatuslineTest('hook.grace-result', { graceOutcome })
      if (graceOutcome === 'grace-timeout' && mountedRef.current) {
        // Detach the bus before marking failure so a late-arriving WS event
        // can't overwrite the failed state.
        unsubBus?.()
        unsubBus = null
        markFailThenSkipRest(
          4,
          `WS event not received within ${STAGE4_GRACE_MS}ms after stream completed`,
        )
      }
    }

    unsubBus?.()
    runningRef.current = false
    if (mountedRef.current) {
      setState((s) => ({ ...s, running: false, lastRunAt: Date.now() }))
    }
  }, [hostId])

  return { state, run }
}
