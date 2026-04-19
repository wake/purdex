import { useCallback, useEffect, useRef, useState } from 'react'
import { hostFetch } from '../lib/host-api'
import { statuslineTestBus } from '../lib/statusline-test-bus'
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
const OVERALL_TIMEOUT_MS = 8000

type StageNum = 1 | 2 | 3 | 4 | 5

interface ServerStageEvent {
  type: 'stage' | 'done'
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

    const markStage = (n: StageNum, patch: StageState) => {
      if (!mountedRef.current) return
      setState((s) => ({ ...s, stages: { ...s.stages, [n]: patch } }))
    }

    const markFailThenSkipRest = (failedAt: StageNum, reason: string) => {
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

    const work = (async () => {
      const res = await hostFetch(hostId, '/api/agent/cc/statusline/test', { method: 'POST' })
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
          if (ev.type === 'done') return
          if (ev.type !== 'stage' || !ev.stage || ev.stage < 1 || ev.stage > 3) continue

          const n = ev.stage as 1 | 2 | 3
          if (ev.status === 'failed') {
            markFailThenSkipRest(n, ev.error ?? 'stage failed')
            return
          }
          markStage(n, { status: 'passed', elapsedMs: ev.elapsed_ms })

          if (n === 1 && ev.nonce) {
            const noncedVal = ev.nonce
            setState((s) => ({ ...s, nonce: noncedVal }))
            markStage(2, { status: 'running' })
            unsubBus = statuslineTestBus.subscribe(noncedVal, ({ nonce: got }) => {
              if (!mountedRef.current) return
              markStage(4, { status: 'passed' })
              const key = compositeKey(hostId, got)
              if (useAgentStore.getState().ccStatus[key]) {
                markStage(5, { status: 'passed' })
              }
            })
            // If the dispatcher already fired (setCcStatus + emit) before we
            // subscribed — possible because the WS broadcast can race the SSE
            // stream — the store will already have the entry. Treat store
            // presence as equivalent to a bus hit (the dispatcher always sets
            // store before emitting).
            const earlyKey = compositeKey(hostId, noncedVal)
            if (useAgentStore.getState().ccStatus[earlyKey]) {
              markStage(4, { status: 'passed' })
              markStage(5, { status: 'passed' })
            }
          } else if (n === 2) {
            setState((s) => s.stages[3].status === 'untested'
              ? { ...s, stages: { ...s.stages, 3: { status: 'running' } } }
              : s)
          } else if (n === 3) {
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

    unsubBus?.()
    runningRef.current = false
    if (mountedRef.current) {
      setState((s) => ({ ...s, running: false, lastRunAt: Date.now() }))
    }
  }, [hostId])

  return { state, run }
}
